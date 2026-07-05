package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	mcp_lib "github.com/paularlott/mcp"
	"github.com/paularlott/webchat"
)

// chatHost adapts the running Router to webchat.Host. One instance is built
// per chat request (cheap — it's just a handful of pointers + an HTTP client
// reference) so per-request timeouts/cancellation stay simple.
//
// The chat UI never speaks to the underlying LLM directly: every request is
// funneled through the router's own /v1/chat/completions endpoint over a
// loopback HTTP call. That way auth, smart-routing, retries and observability
// work identically for chat-driven and API-driven traffic.
type chatHost struct {
	router  *Router
	baseURL string
	token   string
	client  *http.Client
}

// newChatHost builds a host that talks back to the given router via loopback.
// baseURL must include scheme+host+port (e.g. "http://127.0.0.1:8080"); token
// is the API token the loopback request presents (server.Token).
func newChatHost(r *Router, baseURL, token string) *chatHost {
	return &chatHost{
		router:  r,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 0}, // streaming — no overall timeout
	}
}

// Models satisfies webchat.Host. It mirrors the data the admin UI sees so the
// chat picker is always in sync with what providers actually offer.
func (h *chatHost) Models(ctx context.Context) ([]webchat.Model, error) {
	out := []webchat.Model{}
	for _, m := range h.router.getModels() {
		out = append(out, webchat.Model{
			ID:       m.ID,
			Provider: strings.Join(m.Providers, ", "),
		})
	}
	return out, nil
}

// ListTools returns every federated tool the router currently exposes to MCP
// clients. The chat frontend filters the list per the user's session prefs.
func (h *chatHost) ListTools(ctx context.Context) ([]webchat.Tool, error) {
	if h.router.mcpServer == nil || h.router.mcpServer.server == nil {
		return nil, nil
	}
	tools := h.router.mcpServer.server.ListToolsWithContext(ctx)
	out := make([]webchat.Tool, 0, len(tools))
	for _, t := range tools {
		var schema map[string]interface{}
		if s, ok := t.InputSchema.(map[string]interface{}); ok {
			schema = s
		} else if t.InputSchema != nil {
			if b, err := json.Marshal(t.InputSchema); err == nil {
				_ = json.Unmarshal(b, &schema)
			}
		}
		out = append(out, webchat.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out, nil
}

// CallTool invokes a federated tool. The arguments are exactly what the model
// produced (after user confirmation). Errors come back as a Go error so the
// frontend can surface them — MCP signals tool errors via the *ToolError type
// returned from CallTool, not via the response itself.
func (h *chatHost) CallTool(ctx context.Context, name string, arguments json.RawMessage) (webchat.ToolResult, error) {
	if h.router.mcpServer == nil {
		return webchat.ToolResult{}, fmt.Errorf("MCP server not available")
	}
	var argsMap map[string]any
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &argsMap); err != nil {
			return webchat.ToolResult{}, fmt.Errorf("invalid tool arguments: %w", err)
		}
	}
	resp, err := h.router.mcpServer.server.CallTool(ctx, name, argsMap)
	if err != nil {
		// *ToolError surfaces as a non-2xx to MCP clients; for the chat UI we
		// treat it as a successful call returning an error payload, so the
		// model can react to it instead of seeing a transport failure.
		if isToolError(err) {
			return webchat.ToolResult{Content: err.Error(), IsError: true}, nil
		}
		return webchat.ToolResult{}, err
	}
	return webchat.ToolResult{Content: toolResponseText(resp)}, nil
}

// isToolError reports whether err is an MCP *ToolError (any code). We use a
// type assertion against the interface rather than importing the concrete
// type to keep the mcp library version loosely coupled.
func isToolError(err error) bool {
	type toolErr interface{ Code() int }
	_, ok := err.(toolErr)
	return ok
}

// toolResponseText flattens an MCP tool response into a single string the
// model can consume. Multi-content responses concatenate text parts with
// newlines; non-text parts are represented as a "[<type>:N bytes]" placeholder.
func toolResponseText(r *mcp_lib.ToolResponse) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range r.Content {
		if i > 0 {
			b.WriteString("\n")
		}
		switch {
		case c.Text != "":
			b.WriteString(c.Text)
		case c.Data != "":
			b.WriteString("[" + c.Type + ":" + fmt.Sprintf("%d", len(c.Data)) + " bytes]")
		default:
			b.WriteString("[" + c.Type + "]")
		}
	}
	return b.String()
}

// ListPrompts / GetPrompt / ListResources / ReadResource all proxy to the MCP
// server. They share the same namespace as any other MCP client.
func (h *chatHost) ListPrompts(ctx context.Context) ([]webchat.Prompt, error) {
	if h.router.mcpServer == nil {
		return nil, nil
	}
	prompts := h.router.mcpServer.server.ListPrompts(ctx)
	out := make([]webchat.Prompt, 0, len(prompts))
	for _, p := range prompts {
		info := webchat.Prompt{Name: p.Name, Description: p.Description}
		for _, a := range p.Arguments {
			info.Arguments = append(info.Arguments, webchat.PromptArgument{
				Name:        a.Name,
				Description: a.Description,
				Required:    a.Required,
			})
		}
		out = append(out, info)
	}
	return out, nil
}

func (h *chatHost) GetPrompt(ctx context.Context, name string, args map[string]string) (webchat.PromptResult, error) {
	if h.router.mcpServer == nil {
		return webchat.PromptResult{}, fmt.Errorf("MCP server not available")
	}
	resp, err := h.router.mcpServer.server.GetPrompt(ctx, name, args)
	if err != nil {
		return webchat.PromptResult{}, err
	}
	out := webchat.PromptResult{Description: resp.Description}
	for _, m := range resp.Messages {
		out.Messages = append(out.Messages, webchat.PromptMessage{
			Role:    webchat.Role(m.Role),
			Content: m.Content.Text,
		})
	}
	return out, nil
}

func (h *chatHost) ListResources(ctx context.Context) ([]webchat.Resource, error) {
	if h.router.mcpServer == nil {
		return nil, nil
	}
	// Static resources + templates merged into one list for the picker.
	resources := h.router.mcpServer.server.ListResources(ctx)
	templates := h.router.mcpServer.server.ListResourceTemplates(ctx)
	out := make([]webchat.Resource, 0, len(resources)+len(templates))
	for _, r := range resources {
		out = append(out, webchat.Resource{
			URI:         r.URI,
			Name:        r.Name,
			Description: r.Description,
			MimeType:    r.MimeType,
		})
	}
	for _, t := range templates {
		out = append(out, webchat.Resource{
			URI:         t.URITemplate,
			Template:    true,
			Name:        t.Name,
			Description: t.Description,
			MimeType:    t.MimeType,
		})
	}
	return out, nil
}

func (h *chatHost) ReadResource(ctx context.Context, uri string) (webchat.ResourceResult, error) {
	if h.router.mcpServer == nil {
		return webchat.ResourceResult{}, fmt.Errorf("MCP server not available")
	}
	resp, err := h.router.mcpServer.server.ReadResource(ctx, uri)
	if err != nil {
		return webchat.ResourceResult{}, err
	}
	out := webchat.ResourceResult{URI: uri}
	if len(resp.Contents) > 0 {
		out.Text = resp.Contents[0].Text
		out.Blob = resp.Contents[0].Blob
		out.MimeType = resp.Contents[0].MimeType
	}
	return out, nil
}

// Complete satisfies webchat.Host by POSTing the conversation to the router's
// own /v1/chat/completions endpoint (streaming) and translating OpenAI SSE
// chunks into webchat events on the supplied channel. Returns nil on clean
// end-of-stream; errors are also surfaced via an EventError on the channel so
// the client always sees a definitive end.
// Complete satisfies webchat.Host by building an OpenAI-compatible request
// from the webchat message format, POSTing to the router's own
// /v1/chat/completions endpoint, and translating the OpenAI SSE stream
// into webchat events. The request-building and stream-translation logic
// lives in the webchat package (OpenAIChatRequest + TranslateOpenAIStream)
// so other hosts that also talk to OpenAI-compatible APIs can reuse it.
func (h *chatHost) Complete(ctx context.Context, req webchat.CompleteRequest, events chan<- webchat.Event) error {
	body := webchat.OpenAIChatRequest(req)
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if h.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
	}

	return webchat.TranslateOpenAIStream(ctx, resp.Body, events)
}

// Compile-time assertion that chatHost satisfies webchat.Host.
var _ webchat.Host = (*chatHost)(nil)
