package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/webchat"
)

// TestChatHostModelsUsesEmptyRouter covers Models() on a host attached to a
// router with no providers — should return an empty list, not error. This
// exercises the happy path of the adapter without requiring a fully wired
// router. The router's loopback HTTP path used by Complete() is exercised by
// integration tests of the chat UI; here we only smoke-test the surface.
func TestChatHostModelsUsesEmptyRouter(t *testing.T) {
	r := &Router{
		ModelMap:   map[string][]string{},
		ModelMapMu: sync.RWMutex{},
	}
	h := newChatHost(r, "http://127.0.0.1:0", "tok")
	models, err := h.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("expected empty models, got %+v", models)
	}
}

// TestTranslateOpenAIStream parses a canned OpenAI-style SSE stream and
// verifies the webchat events it produces: deltas, tool calls, and the
// terminal done event. Tests the shared webchat.TranslateOpenAIStream
// function directly — no chatHost instance needed.
func TestTranslateOpenAIStream(t *testing.T) {
	const sse = "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\", world!\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"search\",\"arguments\":\"{\\\"q\\\":\"}}]}},{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"hi\\\"}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"

	events := make(chan webchat.Event, 16)
	if err := webchat.TranslateOpenAIStream(context.Background(), strings.NewReader(sse), events); err != nil {
		t.Fatalf("TranslateOpenAIStream: %v", err)
	}
	close(events)

	var got []webchat.Event
	for ev := range events {
		got = append(got, ev)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 events (2 deltas + 1 tool_call + done), got %d (%+v)", len(got), got)
	}
	if got[0].Type != webchat.EventDelta || got[0].Delta != "Hello" {
		t.Fatalf("event 0: %+v", got[0])
	}
	if got[1].Type != webchat.EventDelta || got[1].Delta != ", world!" {
		t.Fatalf("event 1: %+v", got[1])
	}
	if got[2].Type != webchat.EventToolCall || got[2].ToolCall == nil {
		t.Fatalf("event 2: %+v", got[2])
	}
	if got[2].ToolCall.ID != "call_1" || got[2].ToolCall.Name != "search" {
		t.Fatalf("tool call: %+v", got[2].ToolCall)
	}
	// Arguments should be the concatenated fragments from the stream.
	if string(got[2].ToolCall.Arguments) != `{"q":"hi"}` {
		t.Fatalf("tool args: %s", got[2].ToolCall.Arguments)
	}
	if got[3].Type != webchat.EventDone || got[3].FinishReason != webchat.FinishToolCalls {
		t.Fatalf("event 3: %+v", got[3])
	}
}

// TestChatHostCompleteLoopsBack wires the host to a stub OpenAI-compatible
// server and verifies end-to-end that Complete() POSTs to /v1/chat/completions,
// receives the SSE stream, and emits the right events.
func TestChatHostCompleteLoopsBack(t *testing.T) {
	// Stub upstream: returns a fixed two-chunk stream followed by a stop.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// Write chunks; flush after each.
		chunks := []string{
			"data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n",
			"data: {\"choices\":[{\"delta\":{\"content\":\"there\"}}]}\n\n",
			"data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n",
			"data: [DONE]\n\n",
		}
		for _, c := range chunks {
			w.Write([]byte(c))
			w.(http.Flusher).Flush()
		}
	}))
	defer upstream.Close()

	r := &Router{ModelMap: map[string][]string{}, ModelMapMu: sync.RWMutex{}}
	h := newChatHost(r, upstream.URL, "tok")

	events := make(chan webchat.Event, 16)
	err := h.Complete(context.Background(), webchat.CompleteRequest{
		Model:    "m",
		Messages: []webchat.Message{{Role: webchat.RoleUser, Content: "hi"}},
	}, events)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	close(events)

	var deltas []string
	var doneCount int
	for ev := range events {
		switch ev.Type {
		case webchat.EventDelta:
			deltas = append(deltas, ev.Delta)
		case webchat.EventDone:
			doneCount++
			if ev.FinishReason != webchat.FinishStop {
				t.Errorf("finish_reason: %s", ev.FinishReason)
			}
		}
	}
	if len(deltas) != 2 || deltas[0] != "Hi" || deltas[1] != "there" {
		t.Fatalf("deltas: %+v", deltas)
	}
	if doneCount != 1 {
		t.Fatalf("done count: %d", doneCount)
	}
}

// TestHostFrom covers the empty/0.0.0.0 default mapping to 127.0.0.1.
func TestHostFrom(t *testing.T) {
	cases := map[string]string{
		"":      "127.0.0.1",
		"0.0.0.0": "127.0.0.1",
		"::":      "127.0.0.1",
		"1.2.3.4": "1.2.3.4",
	}
	for in, want := range cases {
		if got := hostFrom(in); got != want {
			t.Errorf("hostFrom(%q) = %q want %q", in, got, want)
		}
	}
}

// keep types referenced even when a subset of tests is skipped
var _ = types.Config{}
var _ = json.RawMessage(nil)
