package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp"
)

// A stored MCP server signed in with OAuth keeps working once its access token expires: the client refreshes it
// when the server refuses it, and the rotated tokens are saved, so the next client made for the server (after a
// reload or a restart) does not present a refresh token the authorization server has already replaced.
func TestMCPServer_RefreshesAndSavesOAuthTokens(t *testing.T) {
	var mu sync.Mutex
	accept, refresh := "", "refresh-0" // the access token the MCP server takes, the refresh token the token endpoint takes

	remote := mcp.NewServer("remote", "1.0")
	remote.RegisterTool(mcp.NewTool("ping", "Ping"), func(context.Context, *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		return mcp.NewToolResponseText("pong"), nil
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ok := accept != "" && r.Header.Get("Authorization") == "Bearer "+accept
		mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		remote.HandleRequest(w, r)
	}))
	t.Cleanup(srv.Close)
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		if r.PostForm.Get("refresh_token") != refresh {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		accept, refresh = "access-1", "refresh-1"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": accept, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 600})
	}))
	t.Cleanup(tok.Close)

	ctx := context.Background()
	store := storage.NewMemoryMCPStorage()
	stored := &storage.MCPServerConfig{Namespace: "idp", URL: srv.URL, AuthType: "oauth2", OAuthClientID: "client",
		OAuthTokenURL: tok.URL, OAuthAccessToken: "expired", OAuthRefreshToken: "refresh-0", OAuthResource: srv.URL, Enabled: true}
	if err := store.Create(ctx, stored); err != nil {
		t.Fatal(err)
	}
	s, err := NewMCPServer(&types.Config{}, &testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	s.tokenStore = store
	entry, _ := s.createRemoteServerEntry(types.MCPRemoteServerConfig{Namespace: "idp", URL: srv.URL, AuthType: "oauth2",
		OAuthClientID: "client", OAuthTokenURL: tok.URL, OAuthAccessToken: "expired", OAuthRefreshToken: "refresh-0",
		OAuthResource: srv.URL}, stored)

	if err := entry.Client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize with an expired access token: %v", err)
	}
	if _, err := entry.Client.CallTool(ctx, "ping", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	got, err := store.Get(ctx, "idp")
	if err != nil {
		t.Fatal(err)
	}
	if got.OAuthAccessToken != "access-1" || got.OAuthRefreshToken != "refresh-1" {
		t.Errorf("stored tokens = %q, %q; want the refreshed access-1, refresh-1", got.OAuthAccessToken, got.OAuthRefreshToken)
	}
}
