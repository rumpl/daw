package mcpbridge

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rumpl/daw/internal/adapter"
)

func TestPrepareKeepsHostCommandPrivateAndAddsRuntimeContext(t *testing.T) {
	b := New()
	workspace := t.TempDir()
	prepared, err := b.Prepare([]adapter.MCPServer{{
		PluginID: "sample", Name: "sample-tools", Command: "/host/plugin/mcp",
		Args: []string{"--serve"}, Env: []string{"PLUGIN_SECRET=value"}, WorkingDir: "nested",
	}}, workspace, "chat-1", "session-context")
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 1 || prepared[0].Command != "/home/agent/.local/lib/daw-runner" || len(prepared[0].Args) != 2 {
		t.Fatalf("prepared server = %#v", prepared)
	}
	if strings.Contains(fmt.Sprintf("%#v", prepared[0]), "/host/plugin/mcp") || len(prepared[0].Env) != 0 {
		t.Fatalf("host command leaked to sandbox: %#v", prepared[0])
	}
	server, ok := b.acquire(prepared[0].Args[1])
	if !ok {
		t.Fatal("capability was not registered")
	}
	if server.Command != "/host/plugin/mcp" || server.WorkingDir != filepath.Join(workspace, "nested") {
		t.Fatalf("host launch = %#v", server)
	}
	joined := strings.Join(server.Env, "\n")
	for _, value := range []string{"PLUGIN_SECRET=value", "DAW_CHAT_ID=chat-1", "DAW_SESSION_CONTEXT=session-context"} {
		if !strings.Contains(joined, value) {
			t.Fatalf("host launch env %q lacks %q", joined, value)
		}
	}
	if _, ok := b.acquire(prepared[0].Args[1]); ok {
		t.Fatal("active capability was acquired twice")
	}
	b.release(prepared[0].Args[1])
	if _, ok := b.acquire(prepared[0].Args[1]); !ok {
		t.Fatal("released capability could not support a supervised restart")
	}
	b.Revoke("chat-1")
	if _, ok := b.acquire(prepared[0].Args[1]); ok {
		t.Fatal("revoked capability was accepted")
	}
}

func TestBridgeRelaysCommandStdio(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	b := New()
	prepared, err := b.Prepare([]adapter.MCPServer{{Command: "/bin/sh", Args: []string{"-c", "cat"}}}, "", "chat", "context")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: b}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	token := prepared[0].Args[1]
	_, _ = fmt.Fprintf(conn, "CONNECT /%s HTTP/1.1\r\nHost: mcp-command\r\n\r\n", token)
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT = %#v, %v", response, err)
	}
	want := "{\"jsonrpc\":\"2.0\"}\n"
	if _, err := io.WriteString(conn, want); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(response.Body, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("relayed output = %q, want %q", got, want)
	}
}
