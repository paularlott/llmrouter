package router

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/paularlott/llmrouter/internal/types"
)

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

func newAuthTestRouter(t *testing.T, server types.ServerConfig) *Router {
	t.Helper()
	if server.Host == "" {
		server.Host = "127.0.0.1"
	}
	if server.Port == 0 {
		server.Port = 12345
	}
	r, err := NewRouter(&types.Config{Server: server}, &testLogger{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

func postMCP(r *Router, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initializeBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestMCPResourceURL(t *testing.T) {
	cases := []struct {
		cfg  types.ServerConfig
		want string
	}{
		{types.ServerConfig{PublicURL: "https://llm.example.com/"}, "https://llm.example.com/mcp"},
		{types.ServerConfig{Host: "127.0.0.1", Port: 8080}, "http://127.0.0.1:8080/mcp"},
		{types.ServerConfig{Host: "0.0.0.0", Port: 8080}, "http://localhost:8080/mcp"},
		{types.ServerConfig{Host: "::1", Port: 8080}, "http://[::1]:8080/mcp"},
	}
	for _, c := range cases {
		if got := mcpResourceURL(c.cfg); got != c.want {
			t.Errorf("mcpResourceURL(%+v) = %q, want %q", c.cfg, got, c.want)
		}
	}
}

// Without a token or OIDC issuer the endpoint stays open, as before, and no
// protected resource metadata is published.
func TestMCPEndpointUnauthenticatedWhenUnconfigured(t *testing.T) {
	r := newAuthTestRouter(t, types.ServerConfig{})
	if rec := postMCP(r, ""); rec.Code != http.StatusOK {
		t.Fatalf("POST /mcp = %d %s", rec.Code, rec.Body.String())
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("metadata without auth configured = %d, want 404", rec.Code)
	}
}

// A static token protects /mcp as an OAuth protected resource: a missing or
// wrong token gets 401 with a WWW-Authenticate challenge naming the RFC 9728
// metadata, which is served at both well-known paths.
func TestMCPEndpointStaticToken(t *testing.T) {
	r := newAuthTestRouter(t, types.ServerConfig{Token: "s3cret", PublicURL: "https://llm.example.com"})

	for _, bearer := range []string{"", "wrong"} {
		rec := postMCP(r, bearer)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("POST /mcp bearer=%q = %d, want 401", bearer, rec.Code)
		}
		challenge := rec.Header().Get("WWW-Authenticate")
		if !strings.Contains(challenge, `resource_metadata="https://llm.example.com/.well-known/oauth-protected-resource/mcp"`) {
			t.Errorf("challenge = %q", challenge)
		}
		if bearer != "" && !strings.Contains(challenge, `error="invalid_token"`) {
			t.Errorf("challenge for a wrong token lacks invalid_token: %q", challenge)
		}
	}
	if rec := postMCP(r, "s3cret"); rec.Code != http.StatusOK {
		t.Fatalf("POST /mcp with token = %d %s", rec.Code, rec.Body.String())
	}

	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		var meta map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &meta)
		if meta["resource"] != "https://llm.example.com/mcp" || meta["resource_name"] != "LLM Router" {
			t.Errorf("GET %s metadata = %v", path, meta)
		}
	}

	// GET and DELETE /mcp are routed (and protected) too.
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, "/mcp", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s /mcp without token = %d, want 401", method, rec.Code)
		}
	}

	// Non-MCP routes keep the plain bearer-token middleware.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/models without token = %d, want 401", rec.Code)
	}
}

// With an OIDC issuer, the issuer's JWT access tokens for this resource are
// accepted alongside the static token; wrong audience is refused and a
// missing required scope gets 403 insufficient_scope.
func TestMCPEndpointOIDC(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var idp *httptest.Server
	idp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": idp.URL, "jwks_uri": idp.URL + "/jwks"})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
				"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}}})
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(idp.Close)

	const resource = "https://llm.example.com/mcp"
	r := newAuthTestRouter(t, types.ServerConfig{
		Token:     "s3cret",
		PublicURL: "https://llm.example.com",
		OIDC:      types.OIDCConfig{Issuer: idp.URL, RequiredScopes: []string{"mcp"}},
	})

	sign := func(claims map[string]any) string {
		header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "typ": "at+jwt"})
		body, _ := json.Marshal(claims)
		input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
		sum := sha256.Sum256([]byte(input))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		return input + "." + base64.RawURLEncoding.EncodeToString(sig)
	}
	exp := time.Now().Add(time.Hour).Unix()

	good := sign(map[string]any{"iss": idp.URL, "sub": "alice", "aud": resource, "exp": exp, "scope": "mcp"})
	if rec := postMCP(r, good); rec.Code != http.StatusOK {
		t.Fatalf("valid JWT = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postMCP(r, "s3cret"); rec.Code != http.StatusOK {
		t.Fatalf("static token alongside OIDC = %d %s", rec.Code, rec.Body.String())
	}

	wrongAud := sign(map[string]any{"iss": idp.URL, "sub": "alice", "aud": "https://other.example.com/mcp", "exp": exp, "scope": "mcp"})
	if rec := postMCP(r, wrongAud); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong audience = %d, want 401", rec.Code)
	}

	noScope := sign(map[string]any{"iss": idp.URL, "sub": "alice", "aud": resource, "exp": exp, "scope": "other"})
	rec := postMCP(r, noScope)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "insufficient_scope") {
		t.Errorf("missing scope = %d %q, want 403 insufficient_scope", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}

	mrec := httptest.NewRecorder()
	r.ServeHTTP(mrec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil))
	var meta map[string]any
	_ = json.Unmarshal(mrec.Body.Bytes(), &meta)
	as, _ := meta["authorization_servers"].([]any)
	if len(as) != 1 || as[0] != idp.URL {
		t.Errorf("metadata authorization_servers = %v", meta["authorization_servers"])
	}
}

// execute_script reports the script's own failure as a tool result with
// isError set (visible to and correctable by the model), not as a JSON-RPC
// protocol error.
func TestExecuteScriptFailureIsToolError(t *testing.T) {
	s, err := NewMCPServerWithScriptling(&types.Config{Scripting: types.ScriptingConfig{ExecScript: true}}, &testLogger{})
	if err != nil {
		t.Fatalf("NewMCPServerWithScriptling: %v", err)
	}
	for name, code := range map[string]string{
		"return_error": "import scriptling.mcp.tool as tool\ntool.return_error('bad input')\n",
		"runtime":      "x = 1 / 0\n",
	} {
		resp := mcpRequest(t, s, map[string]interface{}{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]interface{}{"name": "execute_script", "arguments": map[string]interface{}{"code": code}},
		})
		if resp["error"] != nil {
			t.Errorf("%s: JSON-RPC error %v, want an isError result", name, resp["error"])
			continue
		}
		result, _ := resp["result"].(map[string]interface{})
		if result["isError"] != true {
			t.Errorf("%s: result = %v, want isError true", name, result)
		}
	}
}
