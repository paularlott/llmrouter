package router

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/claude"
	"github.com/paularlott/mcp/ai/gemini"
	"github.com/paularlott/mcp/ai/ollama"
	"github.com/paularlott/mcp/ai/openai"
)

// upstream is a recording fake for one provider's native API. Each one speaks
// exactly the wire format that provider's mcp/ai client sends, so reaching it
// at all proves the inbound Ollama request was translated to that native shape.
type upstream struct {
	name       string // provider name
	gotPath    string
	gotBody    map[string]any
	reply      []byte
	replyCT    string
}

func newUpstream(t *testing.T, name string, reply []byte) (*upstream, *httptest.Server) {
	t.Helper()
	u := &upstream{name: name, reply: reply}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		u.gotPath = r.URL.Path
		u.gotBody = parsed
		if u.replyCT != "" {
			w.Header().Set("Content-Type", u.replyCT)
		}
		_, _ = w.Write(u.reply)
	}))
	t.Cleanup(srv.Close)
	return u, srv
}

// buildCrossFormatRouter wires four real provider clients (one per upstream
// type) into a Router, each owning a distinct model. Inbound Ollama requests
// for those models must route to the matching provider and be translated.
func buildCrossFormatRouter(t *testing.T) (*Router, map[string]*upstream) {
	t.Helper()

	openaiReply, _ := json.Marshal(map[string]any{
		"id": "chatcmpl-x", "object": "chat.completion", "model": "m-openai",
		"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "reply-openai"}, "finish_reason": "stop"}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3},
	})
	ollamaReply, _ := json.Marshal(map[string]any{
		"model": "m-ollama", "created_at": "2024-01-01T00:00:00Z",
		"message": map[string]any{"role": "assistant", "content": "reply-ollama"},
		"done_reason": "stop", "done": true, "prompt_eval_count": 1, "eval_count": 2,
	})
	claudeReply, _ := json.Marshal(map[string]any{
		"id": "msg_x", "type": "message", "role": "assistant", "model": "m-claude",
		"content": []map[string]any{{"type": "text", "text": "reply-claude"}},
		"stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 1, "output_tokens": 2},
	})
	// Gemini delegates chat to its OpenAI-compat /openai/ endpoint, so its
	// upstream speaks OpenAI shape — but with a distinct reply to prove the
	// right upstream was reached.
	geminiReply, _ := json.Marshal(map[string]any{
		"id": "chatcmpl-g", "object": "chat.completion", "model": "m-gemini",
		"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "reply-gemini"}, "finish_reason": "stop"}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3},
	})

	upOllama, ollamaSrv := newUpstream(t, "ollama", ollamaReply)
	upOpenAI, openaiSrv := newUpstream(t, "openai", openaiReply)
	upClaude, claudeSrv := newUpstream(t, "claude", claudeReply)
	upGemini, geminiSrv := newUpstream(t, "gemini", geminiReply)

	clientOllama, err := ollama.New(openai.Config{BaseURL: ollamaSrv.URL})
	if err != nil {
		t.Fatalf("ollama client: %v", err)
	}
	clientOpenAI, err := openai.New(openai.Config{BaseURL: openaiSrv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("openai client: %v", err)
	}
	clientClaude, err := claude.New(openai.Config{BaseURL: claudeSrv.URL, APIKey: "k", MaxTokens: 256})
	if err != nil {
		t.Fatalf("claude client: %v", err)
	}
	clientGemini, err := gemini.New(openai.Config{BaseURL: geminiSrv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("gemini client: %v", err)
	}

	r := &Router{
		Providers:   map[string]*Provider{},
		ModelMap:    map[string][]string{},
		ModelTags:   map[string][]string{},
		ModelContext: map[string]int{},
		logger:      &testLogger{},
	}

	add := func(name, providerType, model string, c ai.Client) {
		p := &Provider{Name: name, ProviderType: providerType, Client: c, Enabled: true, Weight: 1}
		p.Healthy.Store(true)
		r.Providers[name] = p
		r.ModelMap[model] = []string{name}
	}
	add("ollama-p", "ollama", "m-ollama", clientOllama)
	add("openai-p", "openai", "m-openai", clientOpenAI)
	add("claude-p", "claude", "m-claude", clientClaude)
	add("gemini-p", "gemini", "m-gemini", clientGemini)

	return r, map[string]*upstream{
		"ollama": upOllama, "openai": upOpenAI, "claude": upClaude, "gemini": upGemini,
	}
}

// TestOllamaIn_RoutesToAllProviderTypes is the cross-format guarantee: an
// Ollama-format chat request arriving at the router is translated and forwarded
// to each upstream provider in that provider's NATIVE wire format, and the
// native reply is translated back out to Ollama format.
func TestOllamaIn_RoutesToAllProviderTypes(t *testing.T) {
	r, ups := buildCrossFormatRouter(t)

	cases := []struct {
		model         string
		provider      string
		wantPath      string // native path the upstream must have been hit on
		nativeChecker string // a key whose presence marks the native body shape
		wantReply     string
	}{
		{"m-openai", "openai", "/chat/completions", "messages", "reply-openai"},
		{"m-ollama", "ollama", "/api/chat", "messages", "reply-ollama"},
		{"m-claude", "claude", "/messages", "max_tokens", "reply-claude"},
		{"m-gemini", "gemini", "/openai/chat/completions", "messages", "reply-gemini"},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			// Inbound Ollama /api/chat request.
			body := []byte(`{"model":"` + tc.model + `","stream":false,"messages":[{"role":"user","content":"translated-question"}]}`)
			req := httptest.NewRequest("POST", "/api/chat", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			r.HandleOllamaChat(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("%s: want 200, got %d body %s", tc.provider, rec.Code, rec.Body.String())
			}

			// Response must come back in Ollama format with the native reply carried through.
			var got ollamaChatResponse
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("%s: decode ollama response: %v", tc.provider, err)
			}
			if !got.Done || got.Message.Content != tc.wantReply {
				t.Fatalf("%s: ollama response = %#v, want done+content %q", tc.provider, got, tc.wantReply)
			}

			// The upstream must have been reached on its NATIVE path, proving translation.
			up := ups[tc.provider]
			if up.gotPath != tc.wantPath {
				t.Fatalf("%s: upstream path = %q, want %q", tc.provider, up.gotPath, tc.wantPath)
			}
			if _, ok := up.gotBody[tc.nativeChecker]; !ok {
				t.Fatalf("%s: upstream body missing native marker %q: %#v", tc.provider, tc.nativeChecker, up.gotBody)
			}
			// The user content must have survived translation to the native body.
			if !bodyContainsUserContent(up.gotBody, "translated-question") {
				t.Fatalf("%s: user content not found in native upstream body: %#v", tc.provider, up.gotBody)
			}
		})
	}
}

// bodyContainsUserContent checks the native request body carried the user text,
// looking inside messages[].content for both string and part-array shapes.
func bodyContainsUserContent(body map[string]any, want string) bool {
	msgs, ok := body["messages"].([]any)
	if !ok {
		return false
	}
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		switch c := mm["content"].(type) {
		case string:
			if c == want {
				return true
			}
		case []any:
			for _, part := range c {
				if pm, ok := part.(map[string]any); ok {
					if t, ok := pm["text"].(string); ok && t == want {
						return true
					}
				}
			}
		}
	}
	return false
}
