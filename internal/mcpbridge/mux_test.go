package mcpbridge_test

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/mcpbridge"
	"github.com/rumpl/daw/internal/stdiomux"
)

func TestBridgeAcrossStdioMux(t *testing.T) {
	bridge := mcpbridge.New()
	prepared, err := bridge.Prepare([]adapter.MCPServer{{Command: "/bin/sh", Args: []string{"-c", "cat"}}}, "", "chat", "context")
	if err != nil {
		t.Fatal(err)
	}

	hostRead, runnerWrite := io.Pipe()
	runnerRead, hostWrite := io.Pipe()
	host, err := stdiomux.New(hostRead, hostWrite, stdiomux.Host)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := stdiomux.New(runnerRead, runnerWrite, stdiomux.Runner)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	defer runner.Close()
	httpServer := &http.Server{Handler: bridge}
	go func() { _ = httpServer.Serve(host) }()
	defer httpServer.Close()

	conn, err := runner.DialContext(t.Context(), "tcp", "mcp-command")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	token := prepared[0].Args[1]
	_, _ = fmt.Fprintf(conn, "CONNECT /%s HTTP/1.1\r\nHost: mcp-command\r\n\r\n", token)
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.Status)
	}
	want := "hello\n"
	if _, err := io.WriteString(conn, want); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(response.Body, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("got %q", got)
	}
}
