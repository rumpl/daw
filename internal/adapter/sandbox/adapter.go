// Package sandbox routes each persistent DAW session to its own Docker
// Sandbox. Sandboxes are stopped when their live chat closes and restarted on
// resume; the shared host workspace remains mounted at its original path.
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker-agent/pkg/version"
	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/adapter/remote"
	"github.com/rumpl/daw/internal/protocol"
	"github.com/rumpl/daw/internal/sandboxrunner"
	"github.com/rumpl/daw/internal/stdiomux"
	sbx "github.com/rumpl/go-sbx"
)

type Config struct {
	Client            *sbx.Client
	Workspace         string
	Kit               string
	Template          string
	PluginDir         string
	IndexFile         string
	CallbackOrigin    string
	CallbackToken     string
	CallbackHandler   http.Handler
	SessionStoreToken string
	CPUs              int
	Memory            string
	ReadyTimeout      time.Duration
	// ModelsGateway reads the host's current gateway setting. The host owns the
	// value; the sandbox backend only mirrors it into each runner so the
	// docker-agent inside the sandbox resolves models the same way.
	ModelsGateway func(context.Context) (string, error)
	Logger        *slog.Logger
}

type Adapter struct {
	client            *sbx.Client
	workspace         string
	kit               string
	template          string
	pluginDir         string
	indexFile         string
	callbackOrigin    string
	callbackToken     string
	callbackHandler   http.Handler
	sessionStoreToken string
	cpus              int
	memory            string
	readyTimeout      time.Duration
	modelsGateway     func(context.Context) (string, error)
	log               *slog.Logger

	provisionMu sync.Mutex
	sessionOps  sync.Mutex
	mu          sync.Mutex
	records     map[string]*record
	connections map[string]*connection
	active      map[string]int
	closed      bool
}

const currentTransport = "stdio-v1"

type record struct {
	SessionID  string `json:"sessionId"`
	Sandbox    string `json:"sandbox"`
	WorkingDir string `json:"workingDir"`
	Transport  string `json:"transport"`
}

type indexFile struct {
	Version  int       `json:"version"`
	Sessions []*record `json:"sessions"`
}

type connection struct {
	runner sandboxrunner.Runner
	remote *remote.Adapter
	peer   *stdiomux.Mux
	server *http.Server
}

func New(config Config) (*Adapter, error) {
	if config.Client == nil {
		config.Client = sbx.New()
	}
	if strings.TrimSpace(config.Workspace) == "" {
		return nil, errors.New("sandbox adapter: workspace is required")
	}
	if strings.TrimSpace(config.Kit) == "" {
		return nil, errors.New("sandbox adapter: kit is required")
	}
	workspace, err := filepath.Abs(config.Workspace)
	if err != nil {
		return nil, err
	}
	kit, err := filepath.Abs(config.Kit)
	if err != nil {
		return nil, err
	}
	for label, path := range map[string]string{"workspace": workspace, "kit": kit} {
		info, statErr := os.Stat(path)
		if statErr != nil || !info.IsDir() {
			return nil, fmt.Errorf("sandbox adapter: %s directory is not usable: %s", label, path)
		}
	}
	pluginDir := ""
	if strings.TrimSpace(config.PluginDir) != "" {
		pluginDir, err = filepath.Abs(config.PluginDir)
		if err != nil {
			return nil, err
		}
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = 2 * time.Minute
	}
	a := &Adapter{
		client: config.Client, workspace: workspace, kit: kit, template: strings.TrimSpace(config.Template), pluginDir: pluginDir,
		indexFile: config.IndexFile, callbackOrigin: config.CallbackOrigin,
		callbackToken: config.CallbackToken, callbackHandler: config.CallbackHandler,
		sessionStoreToken: config.SessionStoreToken, cpus: config.CPUs, memory: config.Memory,
		readyTimeout: config.ReadyTimeout, log: config.Logger,
		modelsGateway: config.ModelsGateway,
		records:       map[string]*record{}, connections: map[string]*connection{}, active: map[string]int{},
	}
	if err := a.load(); err != nil {
		return nil, fmt.Errorf("load sandbox session index: %w", err)
	}
	return a, nil
}

func (a *Adapter) Info(context.Context) (adapter.Info, error) {
	commit := version.Commit
	if commit == "unknown" {
		commit = ""
	}
	return adapter.Info{
		AgentVersion: version.Version, AgentCommit: commit, ModelsAvailable: true,
		ModelsHint: "Models are resolved when a session sandbox starts.",
	}, nil
}

func (a *Adapter) ChatOptions(context.Context, string, []adapter.MCPServer) ([]protocol.ModelOption, []string, []protocol.ToolOption, error) {
	// Catalog discovery requires a running Docker Agent runtime. Do not create
	// an ownerless sandbox merely to populate settings: OpenChat resolves the
	// session's model and tools, then exposes its model catalog through the chat.
	return nil, []string{"none", "low", "medium", "high", "xhigh", "max"}, nil, nil
}

func (a *Adapter) OpenChat(ctx context.Context, request adapter.OpenRequest) (adapter.Chat, error) {
	a.sessionOps.Lock()
	defer a.sessionOps.Unlock()
	var conn *connection
	var err error
	if request.ResumeSessionID != "" {
		a.mu.Lock()
		rec := a.records[request.ResumeSessionID]
		a.mu.Unlock()
		if rec == nil {
			return nil, adapter.ErrNotFound
		}
		if rec.Transport != currentTransport {
			// Sandboxes created by the removed port/callback transport contain an
			// incompatible runner binary. History is host-owned, so recreate only
			// the execution VM and retain the session lifecycle mapping.
			if _, removeErr := a.client.Command(ctx, "rm", "-f", rec.Sandbox); removeErr != nil {
				return nil, fmt.Errorf("replace legacy session sandbox: %w", removeErr)
			}
			a.mu.Lock()
			rec.Transport = currentTransport
			_ = a.saveLocked()
			a.mu.Unlock()
		}
		conn, err = a.ensureRecord(ctx, rec)
	} else {
		conn, err = a.provision(ctx, sessionSandboxName(a.workspace, request.ChatID), request.WorkingDir)
	}
	if err != nil {
		return nil, err
	}
	chat, err := conn.remote.OpenChat(ctx, request)
	if err != nil {
		if request.ResumeSessionID == "" {
			a.discardConnection(context.WithoutCancel(ctx), conn)
		} else {
			a.releaseConnection(context.WithoutCancel(ctx), conn.runner.Name, false)
		}
		return nil, err
	}

	rec := &record{SessionID: chat.SessionID(), Sandbox: conn.runner.Name, WorkingDir: request.WorkingDir, Transport: currentTransport}
	a.mu.Lock()
	if existing := a.records[chat.SessionID()]; existing != nil {
		rec = existing
		rec.Sandbox = conn.runner.Name
		rec.WorkingDir = request.WorkingDir
		rec.Transport = currentTransport
	}
	a.records[chat.SessionID()] = rec
	a.connections[conn.runner.Name] = conn
	a.active[conn.runner.Name]++
	err = a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		_ = chat.Close(context.WithoutCancel(ctx))
		a.releaseConnection(context.WithoutCancel(ctx), conn.runner.Name, true)
		return nil, err
	}
	return &managedChat{Chat: chat, manager: a, conn: conn}, nil
}

// Session history is intentionally not a capability of the lifecycle
// backend. The target router serves both operations from the host catalog.
func (a *Adapter) ListSessions(context.Context, string) ([]protocol.SessionSummary, error) {
	return nil, adapter.ErrUnsupported
}

func (a *Adapter) ReadSession(context.Context, string) (adapter.StoredSession, error) {
	return adapter.StoredSession{}, adapter.ErrUnsupported
}

// ModelsGateway reports the host's value. The host owns the setting; the
// lifecycle backend only mirrors it so the two never disagree.
func (a *Adapter) ModelsGateway(ctx context.Context) (string, error) {
	if a.modelsGateway == nil {
		return "", adapter.ErrUnsupported
	}
	return a.modelsGateway(ctx)
}

// SetModelsGateway mirrors the gateway into every live runner so the
// docker-agent inside each sandbox resolves models the same way as the host.
func (a *Adapter) SetModelsGateway(ctx context.Context, gatewayURL string) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return adapter.ErrClosed
	}
	live := make([]*connection, 0, len(a.connections))
	for _, conn := range a.connections {
		live = append(live, conn)
	}
	a.mu.Unlock()

	var failures []error
	for _, conn := range live {
		if conn.remote == nil {
			continue
		}
		if err := conn.remote.SetModelsGateway(ctx, gatewayURL); err != nil {
			failures = append(failures, fmt.Errorf("sandbox %s: %w", conn.runner.Name, err))
		}
	}
	return errors.Join(failures...)
}

func (a *Adapter) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	connections := make([]*connection, 0, len(a.connections))
	for _, conn := range a.connections {
		connections = append(connections, conn)
	}
	a.connections = map[string]*connection{}
	a.mu.Unlock()
	ctx := context.Background()
	for _, conn := range connections {
		a.closeConnection(ctx, conn)
	}
	return nil
}

func (a *Adapter) ensureRecord(ctx context.Context, rec *record) (*connection, error) {
	a.provisionMu.Lock()
	defer a.provisionMu.Unlock()
	a.mu.Lock()
	if conn := a.connections[rec.Sandbox]; conn != nil {
		a.mu.Unlock()
		return conn, nil
	}
	a.mu.Unlock()
	conn, err := a.provision(ctx, rec.Sandbox, rec.WorkingDir)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.connections[rec.Sandbox] = conn
	a.mu.Unlock()
	return conn, nil
}

func (a *Adapter) provision(ctx context.Context, name, workingDir string) (*connection, error) {
	if a.callbackHandler == nil || strings.TrimSpace(a.sessionStoreToken) == "" {
		return nil, errors.New("sandbox adapter: stdio callback handler and store token are required")
	}
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return nil, adapter.ErrClosed
	}
	extra := []string{}
	if !within(a.workspace, workingDir) {
		extra = append(extra, workingDir)
	}
	runner, err := sandboxrunner.Start(ctx, a.client, sandboxrunner.Options{
		Workspace: a.workspace, AdditionalWorkspaces: extra, Kit: a.kit,
		PluginDir: a.pluginDir, Name: name, Template: a.template, CPUs: a.cpus, Memory: a.memory,
		SessionStoreToken: a.sessionStoreToken,
	})
	if err != nil {
		return nil, err
	}
	if runner.Process == nil {
		return nil, errors.New("sandbox runner returned no stdio process")
	}
	peer, err := stdiomux.New(runner.Process.Stdout, runner.Process.Stdin, stdiomux.Host)
	if err != nil {
		_ = runner.Process.Close()
		return nil, err
	}
	conn := &connection{runner: runner, peer: peer, server: &http.Server{Handler: a.callbackHandler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}}
	go func() { _, _ = io.Copy(os.Stderr, runner.Process.Stderr) }()
	go func() { _ = runner.Process.Wait(); _ = peer.Close() }()
	go func() { _ = conn.server.Serve(peer) }()
	remoteAdapter, err := remote.New(remote.Config{
		Endpoint: "http://runner", Token: runner.Token, DialContext: peer.DialContext,
		CallbackOrigin: a.callbackOrigin, CallbackToken: a.callbackToken,
	})
	if err != nil {
		a.closeConnection(context.Background(), conn)
		return nil, err
	}
	conn.remote = remoteAdapter
	readyCtx, cancel := context.WithTimeout(ctx, a.readyTimeout)
	defer cancel()
	if err := waitReady(readyCtx, remoteAdapter); err != nil {
		a.closeConnection(context.Background(), conn)
		return nil, err
	}
	a.log.Info("session sandbox ready", "sandbox", name, "working_directory", workingDir, "transport", "stdio")
	a.mirrorModelsGateway(ctx, remoteAdapter, name)
	return conn, nil
}

// mirrorModelsGateway seeds a freshly started runner with the host's current
// gateway. A sandbox keeps its own docker-agent user configuration, so without
// this it would resolve models as if no gateway were set. Failure is not fatal:
// the sandbox is usable, so warn rather than tear down a working runner.
func (a *Adapter) mirrorModelsGateway(ctx context.Context, target *remote.Adapter, name string) {
	if a.modelsGateway == nil {
		return
	}
	gatewayURL, err := a.modelsGateway(ctx)
	if err != nil {
		a.log.Warn("read host models gateway for sandbox", "sandbox", name, "error", err)
		return
	}
	if err := target.SetModelsGateway(ctx, gatewayURL); err != nil {
		a.log.Warn("apply models gateway to sandbox", "sandbox", name, "error", err)
	}
}

func (a *Adapter) discardConnection(ctx context.Context, conn *connection) {
	name := conn.runner.Name
	a.mu.Lock()
	delete(a.connections, name)
	a.mu.Unlock()
	a.closeTransport(conn)
	if _, err := a.client.Command(ctx, "rm", "-f", name); err != nil {
		a.log.Warn("remove unused session sandbox", "sandbox", name, "error", err)
		return
	}
	if err := sandboxrunner.RemoveToken(name); err != nil {
		a.log.Warn("remove unused session sandbox token", "sandbox", name, "error", err)
	}
}

func (a *Adapter) releaseConnection(ctx context.Context, name string, decrement bool) {
	a.mu.Lock()
	if decrement && a.active[name] > 0 {
		a.active[name]--
	}
	if a.active[name] != 0 {
		a.mu.Unlock()
		return
	}
	conn := a.connections[name]
	delete(a.connections, name)
	a.mu.Unlock()
	if conn != nil {
		a.closeConnection(ctx, conn)
	} else if _, err := a.client.Command(ctx, "stop", name); err != nil {
		a.log.Warn("stop session sandbox", "sandbox", name, "error", err)
	}
}

func (a *Adapter) closeTransport(conn *connection) {
	if conn == nil {
		return
	}
	if conn.remote != nil {
		_ = conn.remote.Close()
	}
	if conn.server != nil {
		_ = conn.server.Close()
	}
	if conn.peer != nil {
		_ = conn.peer.Close()
	}
	if conn.runner.Process != nil {
		_ = conn.runner.Process.Close()
	}
}

func (a *Adapter) closeConnection(ctx context.Context, conn *connection) {
	if conn == nil {
		return
	}
	a.closeTransport(conn)
	if _, err := a.client.Command(ctx, "stop", conn.runner.Name); err != nil {
		a.log.Warn("stop session sandbox", "sandbox", conn.runner.Name, "error", err)
	}
}

func waitReady(ctx context.Context, value *remote.Adapter) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := value.Check(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("sandbox runner: waiting for stdio readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (a *Adapter) load() error {
	if strings.TrimSpace(a.indexFile) == "" {
		return nil
	}
	data, err := os.ReadFile(a.indexFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file indexFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	for _, rec := range file.Sessions {
		if rec == nil || rec.SessionID == "" || rec.Sandbox == "" {
			continue
		}
		a.records[rec.SessionID] = rec
	}
	return nil
}

func (a *Adapter) saveLocked() error {
	if strings.TrimSpace(a.indexFile) == "" {
		return nil
	}
	file := indexFile{Version: 3, Sessions: make([]*record, 0, len(a.records))}
	for _, rec := range a.records {
		copyRecord := *rec
		file.Sessions = append(file.Sessions, &copyRecord)
	}
	sort.Slice(file.Sessions, func(i, j int) bool { return file.Sessions[i].SessionID < file.Sessions[j].SessionID })
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.indexFile), 0o700); err != nil {
		return err
	}
	temporary := a.indexFile + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, a.indexFile)
}

type managedChat struct {
	adapter.Chat
	manager *Adapter
	conn    *connection
	once    sync.Once
	err     error
}

func (c *managedChat) Close(ctx context.Context) error {
	c.once.Do(func() {
		c.err = c.Chat.Close(ctx)
		refreshCtx := context.WithoutCancel(ctx)
		c.manager.releaseConnection(refreshCtx, c.conn.runner.Name, true)
	})
	return c.err
}

func sessionSandboxName(workspace, seed string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(workspace) + "\x00" + seed))
	return "daw-session-" + hex.EncodeToString(sum[:6])
}

func within(root, candidate string) bool {
	root, rootErr := filepath.Abs(root)
	candidate, candidateErr := filepath.Abs(candidate)
	if rootErr != nil || candidateErr != nil {
		return false
	}
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

var _ adapter.Adapter = (*Adapter)(nil)
var _ adapter.Chat = (*managedChat)(nil)
