package router

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/paularlott/llmrouter/internal/types"
	mcp_lib "github.com/paularlott/mcp"
)

func execScriptServer(t *testing.T, cfg types.ScriptingConfig) *mcp_lib.Server {
	t.Helper()
	cfg.ExecScript = true
	server := mcp_lib.NewServer("test", "1.0")
	manager, err := NewScriptlingToolManager(cfg, &testLogger{}, server)
	if err != nil {
		t.Fatalf("NewScriptlingToolManager: %v", err)
	}
	t.Cleanup(manager.Shutdown)
	return server
}

func runExecScript(t *testing.T, server *mcp_lib.Server, code string) (string, bool, time.Duration) {
	t.Helper()
	started := time.Now()
	resp, err := server.CallTool(context.Background(), "execute_script", map[string]any{"code": code})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text := ""
	if len(resp.Content) > 0 {
		text = resp.Content[0].Text
	}
	return text, resp.IsError, time.Since(started)
}

func TestExecScriptStillRuns(t *testing.T) {
	server := execScriptServer(t, types.ScriptingConfig{})
	text, isErr, _ := runExecScript(t, server, `print(sum([1, 2, 3]))`)
	if isErr || strings.TrimSpace(text) != "6" {
		t.Fatalf("got %q (error %v), want 6", text, isErr)
	}
}

func TestExecScriptTimeLimit(t *testing.T) {
	server := execScriptServer(t, types.ScriptingConfig{ExecScriptTimeoutSec: 1})
	text, isErr, took := runExecScript(t, server, "while True:\n    pass\n")
	if !isErr || !strings.Contains(text, "time limit") {
		t.Fatalf("got %q (error %v), want a time limit error", text, isErr)
	}
	if took > 5*time.Second {
		t.Fatalf("run took %s, want about 1s", took)
	}
}

func TestExecScriptMemoryLimit(t *testing.T) {
	server := execScriptServer(t, types.ScriptingConfig{ExecScriptMemoryMB: 32})
	// Many small values: no single one passes the value limit, so this is
	// stopped by the memory limit.
	text, isErr, took := runExecScript(t, server, "rows = []\nwhile True:\n    rows.append('x' * 1000 + str(len(rows)))\n")
	if !isErr || !strings.Contains(strings.ToLower(text), "memory") {
		t.Fatalf("got %q (error %v), want a memory error", text, isErr)
	}
	if took > 20*time.Second {
		t.Fatalf("run took %s", took)
	}
}

func TestExecScriptValueLimit(t *testing.T) {
	server := execScriptServer(t, types.ScriptingConfig{})
	// A small value holding the same list many times over: its text form
	// would be gigabytes, so print must stop at the value limit.
	code := "c = [1]\nfor i in range(40):\n    c = [c, c]\nprint(c)\n"
	text, isErr, took := runExecScript(t, server, code)
	if !isErr || !strings.Contains(text, "byte limit") {
		t.Fatalf("got %q (error %v), want a byte limit error", truncateForTest(text), isErr)
	}
	if took > 10*time.Second {
		t.Fatalf("run took %s", took)
	}
}

func TestExecScriptResponseIsCapped(t *testing.T) {
	server := execScriptServer(t, types.ScriptingConfig{})
	text, isErr, _ := runExecScript(t, server, "for i in range(3000):\n    print('y' * 1000)\n")
	if isErr {
		t.Fatalf("unexpected error: %s", truncateForTest(text))
	}
	if len(text) > execScriptMaxResponseBytes+100 || !strings.Contains(text, "output truncated") {
		t.Fatalf("response is %d bytes, want it cut at %d with a note", len(text), execScriptMaxResponseBytes)
	}
}

func TestExecLimitsConfig(t *testing.T) {
	cases := []struct {
		cfg  types.ScriptingConfig
		want execScriptLimits
	}{
		{types.ScriptingConfig{}, execScriptLimits{60 * time.Second, 256 << 20, 8 << 20}},
		{types.ScriptingConfig{ExecScriptTimeoutSec: 5, ExecScriptMemoryMB: 4}, execScriptLimits{5 * time.Second, 4 << 20, 4 << 20}},
		{types.ScriptingConfig{ExecScriptTimeoutSec: -1, ExecScriptMemoryMB: -1}, execScriptLimits{0, 0, 8 << 20}},
	}
	for _, c := range cases {
		stm := &scriptlingToolManager{config: c.cfg}
		if got := stm.execLimits(); got != c.want {
			t.Errorf("%+v: got %+v, want %+v", c.cfg, got, c.want)
		}
	}
}

func truncateForTest(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
