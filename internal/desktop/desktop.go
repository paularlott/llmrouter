//go:build !server

// Package desktop hosts a minimal webview shell around the existing HTTP
// server. The server (router + handlers) is built exactly as in headless mode;
// the desktop just opens a webview window that loads the server's loopback URL.
//
// Uses glaze (github.com/crgimenes/glaze) — a pure-Go webview binding built on
// purego. No CGO: the OS webview (WKWebView / WebKitGTK / WebView2) is loaded
// via dlopen at runtime. This means desktop builds cross-compile trivially
// (CGO_ENABLED=0) and auto-detect the available WebKitGTK version on Linux.
//
// This file has a //go:build !server guard so the glaze dependency is fully
// eliminated from server-only binaries. The entry point is wired from
// cmd/root_desktop.go (also !server), which sets RootCmd.Run via init().
package desktop

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"strconv"
	"time"

	"github.com/paularlott/cli"
	"github.com/paularlott/llmrouter/internal/server"
	"github.com/paularlott/llmrouter/log"

	"github.com/crgimenes/glaze"
)

// Run builds the server, binds a TCP listener, then opens a webview window
// pointing at the loopback URL. When the window is closed the webview returns
// and we shut the server down.
func Run(ctx context.Context, cmd *cli.Command) error {
	r, config, err := server.BuildServerForDesktop(cmd)
	if err != nil {
		return err
	}

	logger := log.GetLogger()

	r.StartBackgroundTasks()
	defer r.StopBackgroundTasks()

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port))
	if err != nil {
		logger.Error("failed to bind listener", "error", err)
		return err
	}
	defer listener.Close()

	httpServer := &http.Server{Handler: r}
	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
		}
	}()

	go r.InitMCPServers()
	go r.RefreshModels(ctx)

	addr := listener.Addr().(*net.TCPAddr)
	host := config.Server.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	url := fmt.Sprintf("http://%s/", net.JoinHostPort(host, strconv.Itoa(addr.Port)))
	logger.Info("desktop mode: server listening", "host", config.Server.Host, "port", addr.Port, "url", url)

	// macOS requires NSApplication apps to run inside a .app bundle context.
	// A bare binary crashes with SIGTRAP (unrecoverable Objective-C exception).
	// Detect this before calling glaze and show a helpful message.
	if runtime.GOOS == "darwin" {
		exe, _ := os.Executable()
		if !strings.Contains(exe, ".app/Contents/MacOS/") {
			fmt.Fprintln(os.Stderr, "\nDesktop mode on macOS requires running from inside a .app bundle.")
			fmt.Fprintln(os.Stderr, "Run 'llmrouter server' for headless mode, or double-click the .app from a release.")
			return nil
		}
	}

	// Open the webview window.
	w, err := glaze.New(false)
	if err != nil {
		logger.Error("desktop mode unavailable", "error", err)
		fmt.Fprintln(os.Stderr, "\nDesktop mode is not available on this system.")
		fmt.Fprintln(os.Stderr, "Run 'llmrouter server' to start the headless server instead.")
		return err
	}
	defer w.Destroy()

	w.SetTitle("LLM Router")
	w.SetSize(1280, 800, glaze.HintNone)

	// If admin is disabled (non-localhost + no password), show a security
	// warning instead of navigating to a 404.
	if config.Server.AdminPassword == "" &&
		config.Server.Host != "127.0.0.1" && config.Server.Host != "localhost" && config.Server.Host != "::1" {
		logger.Warn("admin UI disabled: bound to non-localhost without admin password")
		w.SetHtml(`<html><body style="font-family:system-ui;padding:3rem;max-width:40rem;margin:auto;color:#1e293b">
		<h1>🔒 Admin UI Disabled</h1>
		<p>The server is bound to <code>` + config.Server.Host + `</code> (network-accessible) without an admin password.</p>
		<p>The API still works at <code>` + url + `</code>. To enable the admin UI, either bind to localhost or set a password:</p>
		<pre style="background:#f1f5f9;padding:1rem;border-radius:.5rem">
# In ~/.llmrouter/config.toml:
[server]
host = "127.0.0.1"       # localhost only (default)
# OR
admin_password = "secret" # enable admin with login</pre>
		</body></html>`)
	} else {
		w.Navigate(url)
	}
	w.Run()

	// Window closed — shut down the HTTP server cleanly.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	r.Shutdown()
	logger.Info("desktop stopped")

	return nil
}
