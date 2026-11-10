package router

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paularlott/llmrouter/internal/types"
	mcp_lib "github.com/paularlott/mcp"
)

// fakeMercureHub accepts publishes and holds subscription streams open,
// counting how many are open now and the most there have been, and how many
// connections clients hold to it.
type fakeMercureHub struct {
	url   string
	open  atomic.Int64
	peak  atomic.Int64
	conns atomic.Int64
}

func newFakeMercureHub(t *testing.T) *fakeMercureHub {
	t.Helper()
	h := &fakeMercureHub{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = fmt.Fprint(w, "urn:uuid:00000000-0000-0000-0000-000000000001")
			return
		}
		n := h.open.Add(1)
		defer h.open.Add(-1)
		for {
			p := h.peak.Load()
			if n <= p || h.peak.CompareAndSwap(p, n) {
				break
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, ":\n\n")
		http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			h.conns.Add(1)
		case http.StateClosed, http.StateHijacked:
			h.conns.Add(-1)
		}
	}
	srv.Start()
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	h.url = srv.URL + "/.well-known/mercure"
	return h
}

// waitClosed waits until no subscription is open; it fails unless one was
// opened first.
func (h *fakeMercureHub) waitClosed(t *testing.T) {
	t.Helper()
	if h.peak.Load() == 0 {
		t.Fatal("the script never opened a subscription")
	}
	deadline := time.Now().Add(10 * time.Second)
	for h.open.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d subscription(s) the script left open are still open", h.open.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitDisconnected waits until clients hold no connection to the hub.
func (h *fakeMercureHub) waitDisconnected(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.conns.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d connection(s) to the hub are still open", h.conns.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// leaveOpen is script that subscribes and publishes, and does not close the
// client: the subscription ends with the run's context, but the connection
// the publish leaves idle closes only with the client.
func leaveOpen(hubURL string) string {
	return fmt.Sprintf(`
import scriptling.mercure as mercure
hub = mercure.Client(%q)
sub = hub.subscribe("https://example.com/left-open")
hub.publish("https://example.com/left-open", "hello")
`, hubURL)
}

func TestRoutingScriptMercureClosedAfterRun(t *testing.T) {
	h := newFakeMercureHub(t)
	_, sr := newSmartTestRouter(t, leaveOpen(h.url)+`
import router
router.set_model("model-b")
`)
	for i := 0; i < 2; i++ {
		if got := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"}); got.Model != "model-b" {
			t.Fatalf("run %d: want model-b, got %q", i, got.Model)
		}
		h.waitClosed(t)
		// The pooled interpreter lives on: only the end of the run closes
		// the client, and with it the connection.
		h.waitDisconnected(t)
	}
	if len(sr.pool) != vmPoolSize {
		t.Fatalf("want the pool full (%d), got %d", vmPoolSize, len(sr.pool))
	}
}

func TestExecuteScriptMercureClosedAfterRun(t *testing.T) {
	h := newFakeMercureHub(t)
	server := mcp_lib.NewServer("test", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{ExecScript: true}, &testLogger{}, server)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()

	resp, err := server.CallTool(context.Background(), "execute_script", map[string]any{"code": leaveOpen(h.url) + `print("ok")`})
	if err != nil || resp == nil {
		t.Fatalf("execute_script: %v", err)
	}
	h.waitClosed(t)
	h.waitDisconnected(t)
}

// A folder tool runs in an interpreter scriptling's MCP handler makes for the
// call and closes when it returns: the client the script left open, and its
// connection to the hub, close with it.
func TestFolderToolMercureClosedAfterCall(t *testing.T) {
	h := newFakeMercureHub(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "publisher.toml"), []byte(`description = "Publishes and leaves its client open"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "publisher.py"), []byte(fmt.Sprintf(`
import scriptling.mercure as mercure
import scriptling.mcp.tool as tool
hub = mercure.Client(%q)
hub.publish("https://example.com/news", "hello")
tool.return_string("ok")
`, h.url)), 0o644); err != nil {
		t.Fatal(err)
	}
	server := mcp_lib.NewServer("test", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{ToolsDir: dir}, &testLogger{}, server)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()

	resp, err := server.CallTool(context.Background(), "publisher", nil)
	if err != nil || resp == nil {
		t.Fatalf("publisher tool: %v", err)
	}
	if h.conns.Load() == 0 {
		t.Fatal("the tool's client holds no connection to the hub")
	}
	h.waitDisconnected(t)
}
