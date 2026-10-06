package router

import (
	"net/http"
	"net/http/httptest"

	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
)

// inRoutingScriptKey marks a context as belonging to a running routing script,
// so a script that calls its own (or another) virtual model does not recurse.
type inRoutingScriptKey struct{}

// inProcessTransport serves HTTP requests directly from the router, with no
// network hop. The server token (if configured) is attached so the request
// passes the same auth middleware as an external client.
type inProcessTransport struct {
	router *Router
}

func (t *inProcessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.router.mux == nil {
		return nil, http.ErrServerClosed
	}
	req = req.Clone(req.Context())
	if t.router.config != nil && t.router.config.Server.Token != "" {
		token := t.router.config.Server.Token
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	t.router.ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

type inProcessPool struct{ client *http.Client }

func (p inProcessPool) GetHTTPClient() *http.Client { return p.client }

// scriptAI returns the AI client given to routing scripts by router.ai(). It
// talks to this router in-process, using the Ollama client type so that both
// chat and decision-model (System One) calls work. Responses are buffered, so
// streaming calls return once complete.
func (r *Router) scriptAI() (ai.Client, error) {
	r.scriptAIOnce.Do(func() {
		r.scriptAIClient, r.scriptAIErr = ai.NewClient(ai.Config{
			Provider: ai.ProviderOllama,
			Config: openai.Config{
				BaseURL:  "http://llmrouter.internal",
				HTTPPool: inProcessPool{client: &http.Client{Transport: &inProcessTransport{router: r}}},
			},
		})
	})
	return r.scriptAIClient, r.scriptAIErr
}
