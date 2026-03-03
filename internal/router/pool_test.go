package router

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	scriptling "github.com/paularlott/scriptling"
)

// TestPool_SizeIsVmPoolSize verifies the pool is pre-warmed with exactly vmPoolSize VMs.
func TestPool_SizeIsVmPoolSize(t *testing.T) {
	_, sr := newSmartTestRouter(t, `import router; router.set_model("model-a")`)
	if len(sr.pool) != vmPoolSize {
		t.Fatalf("want pool size %d, got %d", vmPoolSize, len(sr.pool))
	}
}

// TestPool_VMReturnedAfterRoute verifies the pool is full again after a route call.
func TestPool_VMReturnedAfterRoute(t *testing.T) {
	_, sr := newSmartTestRouter(t, `import router; router.set_model("model-a")`)
	sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if len(sr.pool) != vmPoolSize {
		t.Fatalf("want pool size %d after route, got %d", vmPoolSize, len(sr.pool))
	}
}

// TestPool_ConcurrentRequests runs vmPoolSize+2 concurrent routes and verifies all succeed.
// The extra 2 requests must wait for a VM to become available.
func TestPool_ConcurrentRequests(t *testing.T) {
	const total = vmPoolSize + 2
	_, sr := newSmartTestRouter(t, `import router; router.set_model("model-a")`)

	var wg sync.WaitGroup
	var succeeded atomic.Int32
	wg.Add(total)
	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()
			result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
			if result.Model == "model-a" {
				succeeded.Add(1)
			}
		}()
	}
	wg.Wait()

	if int(succeeded.Load()) != total {
		t.Fatalf("want %d successful routes, got %d", total, succeeded.Load())
	}
	if len(sr.pool) != vmPoolSize {
		t.Fatalf("want pool fully returned (%d), got %d", vmPoolSize, len(sr.pool))
	}
}

// TestPool_WaitsForVM verifies that a request blocks when the pool is empty and
// proceeds once a VM is returned.
func TestPool_WaitsForVM(t *testing.T) {
	_, sr := newSmartTestRouter(t, `import router; router.set_model("model-a")`)

	// Drain the pool
	vms := make([]*scriptling.Scriptling, vmPoolSize)
	for i := range vms {
		vms[i] = <-sr.pool
	}
	if len(sr.pool) != 0 {
		t.Fatal("pool should be empty")
	}

	done := make(chan RouteResult, 1)
	go func() {
		done <- sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	}()

	// Confirm the goroutine is blocked (no result yet)
	select {
	case <-done:
		t.Fatal("route should be blocked while pool is empty")
	case <-time.After(50 * time.Millisecond):
	}

	// Return one VM — the blocked route should now complete
	sr.pool <- vms[0]
	select {
	case result := <-done:
		if result.Model != "model-a" {
			t.Fatalf("want model-a, got %q", result.Model)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("route did not complete after VM was returned")
	}
}

// TestPool_ContextCancelWhileWaiting verifies that a cancelled context returns the default model.
func TestPool_ContextCancelWhileWaiting(t *testing.T) {
	_, sr := newSmartTestRouter(t, `import router; router.set_model("model-a")`)

	// Drain the pool
	for i := 0; i < vmPoolSize; i++ {
		<-sr.pool
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan RouteResult, 1)
	go func() {
		done <- sr.Route(ctx, &ChatCompletionRequest{Model: "auto"})
	}()

	cancel()
	select {
	case result := <-done:
		if result.Model != sr.defaultModel {
			t.Fatalf("want default model on cancel, got %q", result.Model)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("route did not return after context cancel")
	}
}

// TestPool_VarIsolation verifies that output_model from one request does not
// leak into the next request when the script sets no model.
func TestPool_VarIsolation(t *testing.T) {
	_, sr := newSmartTestRouter(t, `import router; router.set_model("model-b")`)

	// First request sets output_model = "model-b"
	r1 := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if r1.Model != "model-b" {
		t.Fatalf("first request: want model-b, got %q", r1.Model)
	}

	// Swap to a script that sets no model — should fall back to default, not "model-b"
	sr.mu.Lock()
	sr.scriptSrc = `# no set_model`
	sr.pool = sr.buildPool()
	sr.mu.Unlock()

	r2 := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if r2.Model != sr.defaultModel {
		t.Fatalf("second request: want default %q (no leak), got %q", sr.defaultModel, r2.Model)
	}
}

// TestPool_ProviderHintIsolation verifies output_provider does not leak between requests.
func TestPool_ProviderHintIsolation(t *testing.T) {
	_, sr := newSmartTestRouter(t, `import router; router.set_model("model-b", hint="p2")`)

	r1 := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if r1.ProviderHint != "p2" {
		t.Fatalf("first request: want hint p2, got %q", r1.ProviderHint)
	}

	// New script sets model but no hint
	sr.mu.Lock()
	sr.scriptSrc = `import router; router.set_model("model-b")`
	sr.pool = sr.buildPool()
	sr.mu.Unlock()

	r2 := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if r2.ProviderHint != "" {
		t.Fatalf("second request: want no hint (no leak), got %q", r2.ProviderHint)
	}
}

// TestPool_FileWatchRebuildsPool writes a real script file, starts a SmartRouter with
// the file watcher, overwrites the file, and verifies the pool is rebuilt with the new script.
func TestPool_FileWatchRebuildsPool(t *testing.T) {
	r, _ := newSmartTestRouter(t, "") // router only, no SR yet

	// Write initial script
	scriptFile, err := os.CreateTemp(t.TempDir(), "router-*.py")
	if err != nil {
		t.Fatal(err)
	}
	scriptFile.WriteString(`import router; router.set_model("model-a")`)
	scriptFile.Close()

	sr, err := newSmartRouter(scriptFile.Name(), "model-a", nil, "", r, &testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Stop()

	// Confirm initial script works
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-a" {
		t.Fatalf("before file change: want model-a, got %q", result.Model)
	}

	// Capture the current pool pointer
	sr.mu.RLock()
	oldPool := sr.pool
	sr.mu.RUnlock()

	// Overwrite the script file
	if err := os.WriteFile(scriptFile.Name(), []byte(`import router; router.set_model("model-b")`), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce (100ms) + reload; poll up to 2s
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sr.mu.RLock()
		newPool := sr.pool
		sr.mu.RUnlock()
		if newPool != oldPool {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	sr.mu.RLock()
	newPool := sr.pool
	sr.mu.RUnlock()
	if newPool == oldPool {
		t.Fatal("pool was not rebuilt after script file change")
	}

	// New script should route to model-b
	result = sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("after file change: want model-b, got %q", result.Model)
	}

	// Pool should be full again
	if len(sr.pool) != vmPoolSize {
		t.Fatalf("want pool size %d after rebuild, got %d", vmPoolSize, len(sr.pool))
	}
}
func TestPool_RebuildOnScriptChange(t *testing.T) {
	_, sr := newSmartTestRouter(t, `import router; router.set_model("model-a")`)

	r1 := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if r1.Model != "model-a" {
		t.Fatalf("before rebuild: want model-a, got %q", r1.Model)
	}

	// Simulate script change: update src and rebuild pool
	sr.mu.Lock()
	sr.scriptSrc = `import router; router.set_model("model-b")`
	sr.pool = sr.buildPool()
	sr.mu.Unlock()

	r2 := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if r2.Model != "model-b" {
		t.Fatalf("after rebuild: want model-b, got %q", r2.Model)
	}
}

// TestPool_RequestDataPerRequest verifies each request sees its own request_json.
func TestPool_RequestDataPerRequest(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
req = router.get_request()
if len(req["tools"]) > 0:
    router.set_model("model-b")
else:
    router.set_model("model-a")
`)

	withTool := &ChatCompletionRequest{
		Model: "auto",
		Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "calc"}}},
	}
	withoutTool := &ChatCompletionRequest{Model: "auto"}

	r1 := sr.Route(context.Background(), withTool)
	r2 := sr.Route(context.Background(), withoutTool)

	if r1.Model != "model-b" {
		t.Fatalf("with tool: want model-b, got %q", r1.Model)
	}
	if r2.Model != "model-a" {
		t.Fatalf("without tool: want model-a, got %q", r2.Model)
	}
}

// TestLibDir_LibraryAvailableInScript verifies that a .py file in libdir is importable
// by the routing script.
func TestLibDir_LibraryAvailableInScript(t *testing.T) {
	r, _ := newSmartTestRouter(t, "") // router only

	dir := t.TempDir()
	if err := os.WriteFile(dir+"/mylib.py", []byte(`
def pick():
    return "model-b"
`), 0644); err != nil {
		t.Fatal(err)
	}

	scriptFile, err := os.CreateTemp(t.TempDir(), "router-*.py")
	if err != nil {
		t.Fatal(err)
	}
	scriptFile.WriteString(`
import router
import mylib
router.set_model(mylib.pick())
`)
	scriptFile.Close()

	sr, err := newSmartRouter(scriptFile.Name(), "model-a", nil, dir, r, &testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Stop()

	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (from libdir library), got %q", result.Model)
	}
}

// TestLibDir_WatchRebuildsPool verifies that modifying a library file in libdir
// triggers a pool rebuild.
func TestLibDir_WatchRebuildsPool(t *testing.T) {
	r, _ := newSmartTestRouter(t, "")

	dir := t.TempDir()
	if err := os.WriteFile(dir+"/mylib.py", []byte(`
def pick():
    return "model-a"
`), 0644); err != nil {
		t.Fatal(err)
	}

	scriptFile, err := os.CreateTemp(t.TempDir(), "router-*.py")
	if err != nil {
		t.Fatal(err)
	}
	scriptFile.WriteString(`
import router
import mylib
router.set_model(mylib.pick())
`)
	scriptFile.Close()

	sr, err := newSmartRouter(scriptFile.Name(), "model-a", nil, dir, r, &testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Stop()

	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-a" {
		t.Fatalf("before lib change: want model-a, got %q", result.Model)
	}

	sr.mu.RLock()
	oldPool := sr.pool
	sr.mu.RUnlock()

	// Update the library to return model-b
	if err := os.WriteFile(dir+"/mylib.py", []byte(`
def pick():
    return "model-b"
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce + reload
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sr.mu.RLock()
		newPool := sr.pool
		sr.mu.RUnlock()
		if newPool != oldPool {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	sr.mu.RLock()
	rebuilt := sr.pool != oldPool
	sr.mu.RUnlock()
	if !rebuilt {
		t.Fatal("pool was not rebuilt after libdir file change")
	}

	result = sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("after lib change: want model-b, got %q", result.Model)
	}
}

// TestLibDir_NoLibDirNoWatcher verifies that omitting libdir leaves scriptLibs empty
// and does not panic.
func TestLibDir_NoLibDirNoWatcher(t *testing.T) {
	r, _ := newSmartTestRouter(t, "")

	scriptFile, err := os.CreateTemp(t.TempDir(), "router-*.py")
	if err != nil {
		t.Fatal(err)
	}
	scriptFile.WriteString(`import router; router.set_model("model-a")`)
	scriptFile.Close()

	sr, err := newSmartRouter(scriptFile.Name(), "model-a", nil, "", r, &testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Stop()

	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
}
