// Package mcpbridge keeps local plugin MCP commands on the host while exposing
// their stdio transports to sandbox runtimes over DAW's existing stdio mux.
package mcpbridge

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rumpl/daw/internal/adapter"
)

const capabilityTTL = 5 * time.Minute

type launch struct {
	server  adapter.MCPServer
	chatID  string
	expires time.Time
	active  bool
}

// Bridge brokers single-use capabilities for host-side MCP child processes.
type Bridge struct {
	mu       sync.Mutex
	launches map[string]launch
}

func New() *Bridge { return &Bridge{launches: map[string]launch{}} }

// Prepare replaces local command declarations with the sandbox relay command.
// The executable, environment, and host working directory remain only here.
// Callers must invoke Revoke(chatID) if opening the runtime fails.
func (b *Bridge) Prepare(servers []adapter.MCPServer, workingDir, chatID, sessionContext string) ([]adapter.MCPServer, error) {
	prepared := make([]adapter.MCPServer, len(servers))
	copy(prepared, servers)
	for i := range prepared {
		if prepared[i].Command == "" {
			continue
		}
		original := prepared[i]
		original.Env = append([]string(nil), original.Env...)
		if chatID != "" {
			original.Env = append(original.Env, "DAW_CHAT_ID="+chatID)
		}
		if sessionContext != "" {
			original.Env = append(original.Env, "DAW_SESSION_CONTEXT="+sessionContext)
		}
		if original.WorkingDir == "" {
			original.WorkingDir = workingDir
		} else {
			original.WorkingDir = filepath.Join(workingDir, filepath.FromSlash(original.WorkingDir))
		}
		token, err := b.register(original, chatID)
		if err != nil {
			return nil, err
		}
		prepared[i].Command = "/home/agent/.local/lib/daw-runner"
		prepared[i].Args = []string{"mcp-relay", token}
		prepared[i].Env = nil
		prepared[i].WorkingDir = ""
	}
	return prepared, nil
}

func (b *Bridge) register(server adapter.MCPServer, chatID string) (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create MCP relay capability: %w", err)
	}
	token := hex.EncodeToString(value[:])
	now := time.Now()
	b.mu.Lock()
	for key, item := range b.launches {
		if now.After(item.expires) {
			delete(b.launches, key)
		}
	}
	b.launches[token] = launch{server: server, chatID: chatID, expires: now.Add(capabilityTTL)}
	b.mu.Unlock()
	return token, nil
}

func (b *Bridge) acquire(token string) (adapter.MCPServer, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	item, ok := b.launches[token]
	if !ok {
		return adapter.MCPServer{}, false
	}
	if time.Now().After(item.expires) {
		delete(b.launches, token)
		return adapter.MCPServer{}, false
	}
	if item.active {
		return adapter.MCPServer{}, false
	}
	item.active = true
	b.launches[token] = item
	return item.server, true
}

func (b *Bridge) release(token string) {
	b.mu.Lock()
	if item, ok := b.launches[token]; ok {
		item.active = false
		item.expires = time.Now().Add(capabilityTTL)
		b.launches[token] = item
	}
	b.mu.Unlock()
}

// Revoke removes all command capabilities owned by a closed chat.
func (b *Bridge) Revoke(chatID string) {
	b.mu.Lock()
	for token, item := range b.launches {
		if item.chatID == chatID {
			delete(b.launches, token)
		}
	}
	b.mu.Unlock()
}

// ServeHTTP upgrades one authenticated request into a byte-transparent MCP
// stdio stream. Capabilities are single-use and contain no sandbox-controlled
// command details.
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/")
	server, ok := b.acquire(token)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	defer b.release(token)
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	if _, err := rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := rw.Flush(); err != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, server.Command, server.Args...)
	cmd.Env = append(os.Environ(), server.Env...)
	cmd.Dir = server.WorkingDir
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(stdin, rw.Reader); _ = stdin.Close(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, stdout); done <- struct{}{} }()
	<-done
	cancel()
	_ = conn.Close()
	_ = cmd.Wait()
}

// Relay connects a sandbox command transport to its host-side child process.
func Relay(ctx context.Context, token string, input io.Reader, output io.Writer) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("MCP relay capability is required")
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp4", "127.0.0.1:8082")
	if err != nil {
		return fmt.Errorf("connect to MCP relay: %w", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "CONNECT /%s HTTP/1.1\r\nHost: mcp-command\r\n\r\n", token); err != nil {
		return err
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		return fmt.Errorf("open MCP relay: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return fmt.Errorf("open MCP relay: %s", response.Status)
	}
	errCh := make(chan error, 2)
	go func() { _, copyErr := io.Copy(conn, input); errCh <- copyErr }()
	go func() { _, copyErr := io.Copy(output, response.Body); errCh <- copyErr }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}
