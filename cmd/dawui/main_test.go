package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestSandboxCallbackHandlerRoutesVirtualHosts(t *testing.T) {
	called := ""
	handler := &sandboxCallbackHandler{store: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = "store"; w.WriteHeader(http.StatusNoContent) })}
	handler.SetMCP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = "mcp"; w.WriteHeader(http.StatusNoContent) }))
	for _, test := range []struct{ host, want string }{{"session-store", "store"}, {"mcp-callback", "mcp"}} {
		req := httptest.NewRequest(http.MethodGet, "http://"+test.host+"/", nil)
		req.Host = test.host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if called != test.want || response.Code != http.StatusNoContent {
			t.Fatalf("host %q routed to %q status %d", test.host, called, response.Code)
		}
	}
}

func TestListenUnixCreatesOwnerOnlySocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix domain socket permissions are not available on Windows")
	}
	path := shortSocketPath(t)
	ln, err := listenUnix(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %o, want 600", got)
	}

	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("dial socket: %v", err)
	}
	_ = conn.Close()
}

func TestListenUnixDoesNotReplaceLiveSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix domain sockets are not available on Windows")
	}
	path := shortSocketPath(t)
	ln, err := listenUnix(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	_, err = listenUnix(context.Background(), path)
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("second listen error = %v, want EADDRINUSE", err)
	}
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

func TestListenUnixReplacesStaleSocketButNotRegularFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix domain sockets are not available on Windows")
	}
	path := shortSocketPath(t)
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}

	ln, err := listenUnix(t.Context(), path)
	if err != nil {
		t.Fatalf("replace stale socket: %v", err)
	}
	_ = ln.Close()
	_ = os.Remove(path)

	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listenUnix(t.Context(), path); err == nil {
		t.Fatal("listenUnix replaced a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("regular file was modified: %q", data)
	}
}
