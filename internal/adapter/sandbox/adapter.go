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
	"log/slog"
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
	sbx "github.com/rumpl/go-sbx"
)

type Config struct {
	Client         *sbx.Client
	Workspace      string
	Kit            string
	Template       string
	PluginDir      string
	IndexFile      string
	CallbackOrigin string
	CallbackToken  string
	CPUs           int
	Memory         string
	ReadyTimeout   time.Duration
	Logger         *slog.Logger
}

type Adapter struct {
	client         *sbx.Client
	workspace      string
	kit            string
	template       string
	pluginDir      string
	indexFile      string
	callbackOrigin string
	callbackToken  string
	cpus           int
	memory         string
	readyTimeout   time.Duration
	log            *slog.Logger

	provisionMu    sync.Mutex
	sessionOps     sync.Mutex
	legacyOnce     sync.Once
	legacyErr      error
	mu             sync.Mutex
	records        map[string]*record
	legacyImported bool
	connections    map[string]*connection
	active         map[string]int
	closed         bool
}

type record struct {
	SessionID  string                  `json:"sessionId"`
	Sandbox    string                  `json:"sandbox"`
	WorkingDir string                  `json:"workingDir"`
	Summary    protocol.SessionSummary `json:"summary"`
	Attributes map[string]string       `json:"attributes,omitempty"`
}

type indexFile struct {
	Version        int       `json:"version"`
	LegacyImported bool      `json:"legacyImported,omitempty"`
	Sessions       []*record `json:"sessions"`
}

type connection struct {
	runner sandboxrunner.Runner
	remote *remote.Adapter
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
		callbackToken: config.CallbackToken, cpus: config.CPUs, memory: config.Memory,
		readyTimeout: config.ReadyTimeout, log: config.Logger,
		records: map[string]*record{}, connections: map[string]*connection{}, active: map[string]int{},
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
			a.discardConnection(context.WithoutCancel(ctx), conn.runner.Name)
		} else {
			a.releaseConnection(context.WithoutCancel(ctx), conn.runner.Name, false)
		}
		return nil, err
	}

	meta := chat.Meta()
	rec := &record{
		SessionID: chat.SessionID(), Sandbox: conn.runner.Name, WorkingDir: request.WorkingDir,
		Summary: protocol.SessionSummary{
			SessionID: chat.SessionID(), Title: meta.Title, WorkingDir: request.WorkingDir,
			CreatedAt: meta.CreatedAt, Attributes: cloneMap(meta.Attributes),
		},
		Attributes: cloneMap(meta.Attributes),
	}
	a.mu.Lock()
	if existing := a.records[chat.SessionID()]; existing != nil {
		rec = existing
		rec.Sandbox = conn.runner.Name
		rec.WorkingDir = request.WorkingDir
		rec.Summary.Attributes = cloneMap(meta.Attributes)
		rec.Attributes = cloneMap(meta.Attributes)
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
	return &managedChat{Chat: chat, manager: a, conn: conn, sessionID: chat.SessionID()}, nil
}

func (a *Adapter) ListSessions(ctx context.Context, workingDir string) ([]protocol.SessionSummary, error) {
	a.legacyOnce.Do(func() { a.legacyErr = a.importLegacy(ctx) })
	if a.legacyErr != nil {
		return nil, a.legacyErr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]protocol.SessionSummary, 0, len(a.records))
	for _, rec := range a.records {
		summary := rec.Summary
		summary.Attributes = cloneMap(rec.Attributes)
		if workingDir != "" && summary.WorkingDir != "" && summary.WorkingDir != workingDir {
			continue
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

func (a *Adapter) ReadSession(ctx context.Context, sessionID string) (adapter.StoredSession, error) {
	a.sessionOps.Lock()
	defer a.sessionOps.Unlock()
	a.mu.Lock()
	rec := a.records[sessionID]
	a.mu.Unlock()
	if rec == nil {
		return adapter.StoredSession{}, adapter.ErrNotFound
	}
	conn, err := a.ensureRecord(ctx, rec)
	if err != nil {
		return adapter.StoredSession{}, err
	}
	stored, err := conn.remote.ReadSession(ctx, sessionID)
	a.releaseIfInactive(context.WithoutCancel(ctx), conn.runner.Name)
	return stored, err
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
		_, _ = a.client.Command(ctx, "stop", conn.runner.Name)
	}
	return nil
}

func (a *Adapter) importLegacy(ctx context.Context) error {
	a.mu.Lock()
	alreadyImported := a.legacyImported
	a.mu.Unlock()
	if alreadyImported {
		return nil
	}
	name := sandboxrunner.DefaultName(a.workspace)
	if _, err := a.client.Ports(ctx, name); err != nil {
		a.mu.Lock()
		a.legacyImported = true
		err = a.saveLocked()
		a.mu.Unlock()
		return err
	}
	conn, err := a.provision(ctx, name, a.workspace)
	if err != nil {
		return fmt.Errorf("connect legacy workspace sandbox: %w", err)
	}
	list, err := conn.remote.ListSessions(ctx, "")
	if err != nil {
		a.releaseConnection(context.WithoutCancel(ctx), name, false)
		return err
	}
	a.mu.Lock()
	for i := range list {
		if a.records[list[i].SessionID] != nil {
			continue
		}
		a.records[list[i].SessionID] = &record{
			SessionID: list[i].SessionID, Sandbox: name, WorkingDir: list[i].WorkingDir,
			Summary: list[i], Attributes: cloneMap(list[i].Attributes),
		}
	}
	a.legacyImported = true
	saveErr := a.saveLocked()
	a.mu.Unlock()
	a.releaseConnection(context.WithoutCancel(ctx), name, false)
	if len(list) != 0 {
		a.log.Info("imported legacy workspace sandbox sessions", "sandbox", name, "sessions", len(list))
	}
	return saveErr
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
	})
	if err != nil {
		return nil, err
	}
	readyCtx, cancel := context.WithTimeout(ctx, a.readyTimeout)
	defer cancel()
	if err := sandboxrunner.WaitReady(readyCtx, runner.Endpoint, runner.Token); err != nil {
		return nil, err
	}
	remoteAdapter, err := remote.New(remote.Config{
		Endpoint: runner.Endpoint, Token: runner.Token,
		CallbackOrigin: a.callbackOrigin, CallbackToken: a.callbackToken,
	})
	if err != nil {
		return nil, err
	}
	a.log.Info("session sandbox ready", "sandbox", name, "working_directory", workingDir)
	return &connection{runner: runner, remote: remoteAdapter}, nil
}

func (a *Adapter) discardConnection(ctx context.Context, name string) {
	a.mu.Lock()
	delete(a.connections, name)
	a.mu.Unlock()
	if _, err := a.client.Command(ctx, "rm", "-f", name); err != nil {
		a.log.Warn("remove unused session sandbox", "sandbox", name, "error", err)
		return
	}
	if err := sandboxrunner.RemoveToken(name); err != nil {
		a.log.Warn("remove unused session sandbox token", "sandbox", name, "error", err)
	}
}

func (a *Adapter) releaseIfInactive(ctx context.Context, name string) {
	a.mu.Lock()
	inactive := a.active[name] == 0
	a.mu.Unlock()
	if inactive {
		a.releaseConnection(ctx, name, false)
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
	delete(a.connections, name)
	a.mu.Unlock()
	if _, err := a.client.Command(ctx, "stop", name); err != nil {
		a.log.Warn("stop session sandbox", "sandbox", name, "error", err)
	}
}

func (a *Adapter) updateSummary(ctx context.Context, conn *connection, sessionID string) {
	list, err := conn.remote.ListSessions(ctx, "")
	if err != nil {
		a.log.Warn("refresh sandbox session summary", "session", sessionID, "error", err)
		return
	}
	for i := range list {
		if list[i].SessionID != sessionID {
			continue
		}
		a.mu.Lock()
		if rec := a.records[sessionID]; rec != nil {
			rec.Summary = list[i]
			rec.Attributes = cloneMap(list[i].Attributes)
			_ = a.saveLocked()
		}
		a.mu.Unlock()
		return
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
	a.legacyImported = file.LegacyImported
	for _, rec := range file.Sessions {
		if rec == nil || rec.SessionID == "" || rec.Sandbox == "" {
			continue
		}
		rec.Summary.Attributes = cloneMap(rec.Attributes)
		a.records[rec.SessionID] = rec
	}
	return nil
}

func (a *Adapter) saveLocked() error {
	if strings.TrimSpace(a.indexFile) == "" {
		return nil
	}
	file := indexFile{Version: 1, LegacyImported: a.legacyImported, Sessions: make([]*record, 0, len(a.records))}
	for _, rec := range a.records {
		copyRecord := *rec
		copyRecord.Attributes = cloneMap(rec.Attributes)
		copyRecord.Summary.Attributes = nil
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
	manager   *Adapter
	conn      *connection
	sessionID string
	once      sync.Once
	err       error
}

func (c *managedChat) Close(ctx context.Context) error {
	c.once.Do(func() {
		c.err = c.Chat.Close(ctx)
		refreshCtx := context.WithoutCancel(ctx)
		c.manager.updateSummary(refreshCtx, c.conn, c.sessionID)
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

func cloneMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

var _ adapter.Adapter = (*Adapter)(nil)
var _ adapter.Chat = (*managedChat)(nil)
