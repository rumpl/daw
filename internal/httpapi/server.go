package httpapi

import (
	"context"
	"crypto/rand"
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/pathsec"
	"github.com/rumpl/daw/internal/protocol"
)

// maxBodyBytes is the hard cap on any request body. Prompts are text; nothing
// legitimate here is large.
const maxBodyBytes = 256 << 10 // 256 KiB

// heartbeatInterval matches the upstream API server's SSE keep-alive cadence.
const heartbeatInterval = 20 * time.Second

// Options configures the server.
type Options struct {
	Adapter        adapter.Adapter
	Guard          *pathsec.Guard
	AppVersion     string
	TailscaleHosts []string
	AllowedTSUsers []string
	// Static serves the built frontend; nil disables the UI (API-only tests).
	Static http.Handler
	Logger *slog.Logger
	// DefaultAgent is the agent source used when a request carries no
	// agentId. Empty means "coder", docker-agent's built-in coding agent.
	DefaultAgent string
	// DefaultPosture is the safety mode every new chat starts in. Empty means
	// protocol.PostureAutonomous, this deployment's configured default (see
	// DEFAULT_SAFETY in the README). Resumed sessions keep their own stored
	// mode and are never silently re-scoped by this value.
	DefaultPosture protocol.Posture
	// SkippedRoots are configured roots that could not be canonicalized.
	SkippedRoots []string
	// WorkspaceHistoryFile persists successfully opened project paths so the
	// list is shared by every browser connected to this server. Empty disables
	// persistence (primarily useful for tests).
	WorkspaceHistoryFile string
}

// Server is the whole dashboard HTTP surface.
type Server struct {
	mux                  *http.ServeMux
	adapter              adapter.Adapter
	guard                *pathsec.Guard
	hosts                *hostPolicy
	csrf                 string
	appVersion           string
	allowedTSUsers       map[string]bool
	static               http.Handler
	log                  *slog.Logger
	started              time.Time
	skippedRoots         []string
	defaultPosture       protocol.Posture
	defaultAgent         string
	workspaceHistoryFile string
	// builtins is the set of agent names embedded in the matched module.
	// It is populated from the adapter, never from the browser.
	builtinsOnce sync.Once
	builtins     map[string]bool

	mu         sync.Mutex
	workspaces map[string]*workspaceEntry
	agents     map[string]*agentEntry
	chats      map[string]*liveChat
	bySession  map[string]string // sessionId -> chatId (canonical ownership)
	hintsWS    []protocol.WorkspaceHint
	hintsAgent []protocol.AgentSourceHint
}

type workspaceEntry struct {
	id   string
	path string
}

type agentEntry struct {
	id       string
	source   string
	kind     protocol.AgentSourceKind
	resolved *protocol.ResolvedAgent
}

// New builds the server and registers every route.
func New(opts Options) *Server {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	users := map[string]bool{}
	for _, u := range opts.AllowedTSUsers {
		u = strings.ToLower(strings.TrimSpace(u))
		if u != "" {
			users[u] = true
		}
	}
	defaultPosture := opts.DefaultPosture
	if defaultPosture == "" {
		defaultPosture = protocol.PostureAutonomous
	}
	defaultAgent := strings.TrimSpace(opts.DefaultAgent)
	if defaultAgent == "" {
		defaultAgent = "coder"
	}
	s := &Server{
		defaultAgent:         defaultAgent,
		defaultPosture:       defaultPosture,
		workspaceHistoryFile: strings.TrimSpace(opts.WorkspaceHistoryFile),
		mux:                  http.NewServeMux(),
		adapter:              opts.Adapter,
		guard:                opts.Guard,
		hosts:                newHostPolicy(opts.TailscaleHosts),
		csrf:                 newToken(),
		appVersion:           opts.AppVersion,
		allowedTSUsers:       users,
		static:               opts.Static,
		log:                  log,
		started:              time.Now(),
		skippedRoots:         opts.SkippedRoots,
		workspaces:           map[string]*workspaceEntry{},
		agents:               map[string]*agentEntry{},
		chats:                map[string]*liveChat{},
		bySession:            map[string]string{},
	}
	s.loadWorkspaceHistory()
	s.routes()
	return s
}

// CSRFToken exposes the per-process token (tests only need it via bootstrap).
func (s *Server) CSRFToken() string { return s.csrf }

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/health", s.handleHealth)
	m.HandleFunc("GET /api/bootstrap", s.handleBootstrap)
	m.HandleFunc("POST /api/workspaces/open", s.handleOpenWorkspace)
	m.HandleFunc("POST /api/agents/resolve", s.handleResolveAgent)
	m.HandleFunc("GET /api/workspaces/{workspaceId}/sessions", s.handleListSessions)
	m.HandleFunc("POST /api/chats", s.handleCreateChat)
	m.HandleFunc("POST /api/chats/resume", s.handleResumeChat)
	m.HandleFunc("GET /api/chats/{id}", s.handleGetChat)
	m.HandleFunc("GET /api/chats/{id}/events", s.handleEvents)
	m.HandleFunc("POST /api/chats/{id}/messages", s.handleMessages)
	m.HandleFunc("POST /api/chats/{id}/abort", s.handleAbort)
	m.HandleFunc("PATCH /api/chats/{id}/config", s.handleConfig)
	m.HandleFunc("GET /api/chats/{id}/models", s.handleModels)
	m.HandleFunc("GET /api/chats/{id}/commands", s.handleCommands)
	m.HandleFunc("POST /api/chats/{id}/tool-confirmation", s.handleToolConfirmation)
	m.HandleFunc("POST /api/chats/{id}/elicitation", s.handleElicitation)
	m.HandleFunc("POST /api/chats/{id}/retitle", s.handleRetitle)
	m.HandleFunc("POST /api/chats/{id}/compact", s.handleCompact)
	m.HandleFunc("GET /api/chats/{id}/stats", s.handleStats)
	m.HandleFunc("DELETE /api/chats/{id}", s.handleDispose)
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	if s.static != nil {
		m.Handle("/", s.static)
	}
}

// ServeHTTP applies the security gate in front of every route.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	handler := securityHeaders(http.HandlerFunc(s.serve))
	handler.ServeHTTP(w, r)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if !s.checkHost(r) {
		s.fail(w, http.StatusForbidden, "forbidden_host", "this host is not allowed")
		return
	}
	if !s.checkTailscaleUser(r) {
		s.fail(w, http.StatusForbidden, "forbidden_user", "this tailnet user is not allowed")
		return
	}
	if isMutation(r.Method) {
		if !s.checkOrigin(r) {
			s.fail(w, http.StatusForbidden, "forbidden_origin", "cross-site request rejected")
			return
		}
		if !s.checkCSRF(r) {
			s.fail(w, http.StatusForbidden, "forbidden_csrf", "missing or invalid CSRF token")
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// postureForOpen returns the posture to apply when opening a chat: the
// configured default for a new session, and "" (keep the session's own stored
// safety mode) when resuming.
func postureForOpen(def protocol.Posture, resumeID string) protocol.Posture {
	if resumeID != "" {
		return ""
	}
	return def
}

func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (s *Server) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.log.Debug("write response", "error", err)
	}
}

// fail emits the single JSON error shape. Messages are curated constants; raw
// Go errors (which can embed absolute paths or environment values) never reach
// the browser.
func (s *Server) fail(w http.ResponseWriter, status int, code, msg string) {
	s.json(w, status, protocol.APIError{Error: msg, Code: code})
}

// decode reads a strictly-typed body: size-limited, unknown fields rejected,
// no trailing content.
func decode[T any](w http.ResponseWriter, r *http.Request, s *Server) (T, bool) {
	var v T
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.fail(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
			return v, false
		}
		s.fail(w, http.StatusBadRequest, "invalid_body", "request body did not match the expected schema")
		return v, false
	}
	if err := dec.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		s.fail(w, http.StatusBadRequest, "invalid_body", "request body must contain exactly one JSON object")
		return v, false
	}
	return v, true
}

func newOpaqueID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// handlers
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.json(w, http.StatusOK, protocol.Health{Status: "ok",
		Uptime: int64(time.Since(s.started).Seconds())})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	info, err := s.adapter.Info(r.Context())
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "sdk_init_failed",
			"docker-agent could not be initialized; check the server log")
		return
	}
	s.mu.Lock()
	wsHints := append([]protocol.WorkspaceHint(nil), s.hintsWS...)
	agHints := append([]protocol.AgentSourceHint(nil), s.hintsAgent...)
	s.mu.Unlock()

	notices := append([]protocol.Notice(nil), info.Notices...)
	for _, sk := range s.skippedRoots {
		notices = append(notices, protocol.Notice{
			ID: "root:" + sk, Level: protocol.NoticeWarning,
			Message: fmt.Sprintf("Configured workspace root %q was skipped (missing or not a directory).", sk),
			Code:    "workspace_root_skipped"})
	}
	if s.defaultPosture == protocol.PostureAutonomous {
		notices = append(notices, protocol.Notice{
			ID: "default-autonomous", Level: protocol.NoticeWarning, Code: "default_autonomous",
			Message: "This server starts every new chat in autonomous mode: every tool call, " +
				"including shell commands, is auto-approved and runs on this host as your user. " +
				"Set DEFAULT_SAFETY=strict to require confirmation again.",
		})
	}
	notices = append(notices, protocol.Notice{
		ID: "sandbox", Level: protocol.NoticeInfo, Code: "no_sandbox",
		Message: "This dashboard embeds docker-agent in-process: tools run directly on this host " +
			"with your user's permissions. There is no sandbox. Use `docker agent run --sandbox` " +
			"in a terminal if you need isolation."})

	s.json(w, http.StatusOK, protocol.Bootstrap{
		AppVersion: s.appVersion, AgentVersion: info.AgentVersion, AgentCommit: info.AgentCommit,
		ConfigDir: info.ConfigDir, DataDir: info.DataDir, CacheDir: info.CacheDir,
		SessionDB: info.SessionDB, WorkspaceRoots: s.guard.Roots(), CSRFToken: s.csrf,
		Sandboxed: false, DefaultPosture: s.defaultPosture,
		DefaultAgent: s.defaultAgent, BuiltinAgents: info.BuiltinAgents,
		ModelsAvailable: info.ModelsAvailable, ModelsHint: info.ModelsHint,
		WorkspaceHints: wsHints, AgentSourceHints: agHints, Notices: notices,
	})
}

func (s *Server) handleOpenWorkspace(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[protocol.OpenWorkspaceRequest](w, r, s)
	if !ok {
		return
	}
	canon, err := s.guard.ResolveDir(req.Path)
	if err != nil {
		s.failPath(w, err)
		return
	}
	s.mu.Lock()
	var entry *workspaceEntry
	for _, e := range s.workspaces {
		if e.path == canon {
			entry = e
			break
		}
	}
	if entry == nil {
		entry = &workspaceEntry{id: newOpaqueID("ws"), path: canon}
		s.workspaces[entry.id] = entry
	}
	s.rememberWorkspaceLocked(canon)
	s.mu.Unlock()

	ws := protocol.Workspace{WorkspaceID: entry.id, Path: canon, Label: filepath.Base(canon)}
	if fi, err := os.Stat(filepath.Join(canon, "AGENTS.md")); err == nil && !fi.IsDir() {
		ws.AgentsMD = true
	}
	if fi, err := os.Stat(filepath.Join(canon, ".agentsignore")); err == nil && !fi.IsDir() {
		ws.AgentsIgnore = true
		ws.Notices = append(ws.Notices, protocol.Notice{ID: "agentsignore", Level: protocol.NoticeInfo,
			Message: ".agentsignore is present and is honoured by docker-agent's permission checker.",
			Code:    "agentsignore"})
	}
	s.json(w, http.StatusOK, ws)
}

func (s *Server) failPath(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pathsec.ErrOutsideRoots):
		s.fail(w, http.StatusForbidden, "outside_roots",
			"that path is outside the allowed workspace roots (see WORKSPACE_ROOTS)")
	case errors.Is(err, pathsec.ErrNotDirectory):
		s.fail(w, http.StatusBadRequest, "not_a_directory", "that path is not a directory")
	case errors.Is(err, pathsec.ErrNotFile):
		s.fail(w, http.StatusBadRequest, "not_a_file", "that path is not a regular file")
	case errors.Is(err, pathsec.ErrNotAbsolute):
		s.fail(w, http.StatusBadRequest, "not_absolute", "the path must be absolute")
	default:
		s.fail(w, http.StatusBadRequest, "path_missing", "that path does not exist or is not reachable")
	}
}

func (s *Server) rememberWorkspaceLocked(path string) {
	// Opening an existing project promotes it to the front, making this a true
	// most-recently-used list rather than merely a set of the first paths seen.
	next := make([]protocol.WorkspaceHint, 0, min(len(s.hintsWS)+1, maxWorkspaceHints))
	next = append(next, protocol.WorkspaceHint{Path: path, Label: filepath.Base(path)})
	for _, hint := range s.hintsWS {
		if hint.Path == path {
			continue
		}
		next = append(next, hint)
		if len(next) == maxWorkspaceHints {
			break
		}
	}
	s.hintsWS = next
	if s.workspaceHistoryFile != "" {
		if err := writeWorkspaceHistory(s.workspaceHistoryFile, s.hintsWS); err != nil {
			s.log.Warn("could not persist workspace history", "error", err)
		}
	}
}

func (s *Server) rememberAgentLocked(src string, kind protocol.AgentSourceKind, label string) {
	for _, h := range s.hintsAgent {
		if h.Source == src {
			return
		}
	}
	s.hintsAgent = append([]protocol.AgentSourceHint{{Source: src, Kind: kind, Label: label}}, s.hintsAgent...)
	if len(s.hintsAgent) > 10 {
		s.hintsAgent = s.hintsAgent[:10]
	}
}

// builtinAgents caches the embedded agent names reported by the adapter.
func (s *Server) builtinAgents(ctx context.Context) map[string]bool {
	s.builtinsOnce.Do(func() {
		s.builtins = map[string]bool{}
		info, err := s.adapter.Info(ctx)
		if err != nil {
			return
		}
		for _, n := range info.BuiltinAgents {
			s.builtins[n] = true
		}
	})
	return s.builtins
}

// classifyAgentSource decides whether the string names one of docker-agent's
// embedded agents, a local file we must contain inside an allowed root, or an
// OCI reference we must not fetch without an explicit user action.
//
// The built-in set comes from the matched module, never from the request, so a
// browser cannot invent a "built-in" name to bypass path containment.
func (s *Server) classifyAgentSource(ctx context.Context, src string) protocol.AgentSourceKind {
	ref := strings.TrimSpace(src)
	if s.builtinAgents(ctx)[ref] {
		return protocol.AgentSourceBuiltin
	}
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "~") || strings.HasPrefix(ref, ".") {
		return protocol.AgentSourceFile
	}
	if strings.HasSuffix(ref, ".yaml") || strings.HasSuffix(ref, ".yml") {
		return protocol.AgentSourceFile
	}
	return protocol.AgentSourceOCI
}

func (s *Server) handleResolveAgent(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[protocol.ResolveAgentRequest](w, r, s)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		s.fail(w, http.StatusBadRequest, "invalid_source", "an agent source is required")
		return
	}
	workingDir := ""
	if req.WorkspaceID != "" {
		ws, ok := s.workspace(req.WorkspaceID)
		if !ok {
			s.fail(w, http.StatusNotFound, "unknown_workspace", "unknown workspace")
			return
		}
		workingDir = ws.path
	}

	kind := s.classifyAgentSource(r.Context(), req.Source)
	source := req.Source
	if kind == protocol.AgentSourceFile {
		canon, err := s.guard.ResolveFile(req.Source)
		if err != nil {
			s.failPath(w, err)
			return
		}
		source = canon
	} else if !req.AllowRemoteFetch {
		s.fail(w, http.StatusPreconditionRequired, "confirm_remote_fetch",
			"pulling a remote agent image is an explicit trust decision; confirm to continue")
		return
	}

	resolved, err := s.adapter.ResolveAgent(r.Context(), adapter.ResolveRequest{
		Source: source, Kind: kind, WorkingDir: workingDir, AllowRemoteFetch: req.AllowRemoteFetch,
	})
	if err != nil {
		switch {
		case errors.Is(err, adapter.ErrRemoteFetch):
			s.fail(w, http.StatusPreconditionRequired, "confirm_remote_fetch",
				"pulling a remote agent image is an explicit trust decision; confirm to continue")
		default:
			s.log.Warn("resolve agent failed", "error", err)
			s.fail(w, http.StatusBadRequest, "agent_load_failed",
				"that agent configuration could not be loaded")
		}
		return
	}

	s.mu.Lock()
	var entry *agentEntry
	for _, e := range s.agents {
		if e.source == source {
			entry = e
			break
		}
	}
	if entry == nil {
		entry = &agentEntry{id: newOpaqueID("ag"), source: source, kind: kind}
		s.agents[entry.id] = entry
	}
	entry.resolved = resolved
	s.rememberAgentLocked(source, kind, resolved.Label)
	s.mu.Unlock()

	resolved.AgentID = entry.id
	resolved.Source = source
	resolved.Kind = kind
	s.json(w, http.StatusOK, resolved)
}

func (s *Server) workspace(id string) (*workspaceEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.workspaces[id]
	return e, ok
}

func (s *Server) agent(id string) (*agentEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.agents[id]
	return e, ok
}

// agentOrDefault resolves an explicit agentId, or lazily loads the server's
// default agent (docker-agent's built-in "coder") when none was supplied, so
// the browser never has to pick an agent config to start a chat.
func (s *Server) agentOrDefault(ctx context.Context, id string) (*agentEntry, error) {
	if id != "" {
		e, ok := s.agent(id)
		if !ok {
			return nil, adapter.ErrNotFound
		}
		return e, nil
	}

	kind := s.classifyAgentSource(ctx, s.defaultAgent)
	s.mu.Lock()
	for _, e := range s.agents {
		if e.source == s.defaultAgent {
			s.mu.Unlock()
			return e, nil
		}
	}
	s.mu.Unlock()

	// A non-builtin DEFAULT_AGENT still has to satisfy containment: it is
	// operator configuration, but it is still a path.
	source := s.defaultAgent
	if kind == protocol.AgentSourceFile {
		canon, err := s.guard.ResolveFile(source)
		if err != nil {
			return nil, err
		}
		source = canon
	}

	resolved, err := s.adapter.ResolveAgent(ctx, adapter.ResolveRequest{
		Source: source, Kind: kind,
		// A configured default is the operator's own explicit choice.
		AllowRemoteFetch: kind == protocol.AgentSourceOCI,
	})
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.agents {
		if e.source == source {
			return e, nil
		}
	}
	entry := &agentEntry{id: newOpaqueID("ag"), source: source, kind: kind, resolved: resolved}
	s.agents[entry.id] = entry
	resolved.AgentID = entry.id
	return entry, nil
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.workspace(r.PathValue("workspaceId"))
	if !ok {
		s.fail(w, http.StatusNotFound, "unknown_workspace", "unknown workspace")
		return
	}
	list, err := s.adapter.ListSessions(r.Context(), ws.path)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "session_list_failed",
			"the docker-agent session store could not be listed")
		return
	}
	s.mu.Lock()
	for i := range list {
		if _, live := s.bySession[list[i].SessionID]; live {
			list[i].Live = true
		}
	}
	s.mu.Unlock()
	if list == nil {
		list = []protocol.SessionSummary{}
	}
	s.json(w, http.StatusOK, list)
}

func (s *Server) handleCreateChat(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[protocol.CreateChatRequest](w, r, s)
	if !ok {
		return
	}
	s.openChat(w, r, req.WorkspaceID, req.AgentID, req.AgentName, "")
}

func (s *Server) handleResumeChat(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[protocol.ResumeChatRequest](w, r, s)
	if !ok {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		s.fail(w, http.StatusBadRequest, "invalid_session", "a session id is required")
		return
	}
	ws, ok := s.workspace(req.WorkspaceID)
	if !ok {
		s.fail(w, http.StatusNotFound, "unknown_workspace", "unknown workspace")
		return
	}
	// Validate the id against a *fresh* listing from the matched store. The
	// browser can never hand us an arbitrary id or a session-db path.
	list, err := s.adapter.ListSessions(r.Context(), "")
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "session_list_failed",
			"the docker-agent session store could not be listed")
		return
	}
	valid := false
	for _, sum := range list {
		if sum.SessionID == req.SessionID {
			valid = true
			break
		}
	}
	if !valid {
		s.fail(w, http.StatusNotFound, "unknown_session", "unknown session")
		return
	}
	_ = ws
	s.openChat(w, r, req.WorkspaceID, req.AgentID, "", req.SessionID)
}

func (s *Server) openChat(w http.ResponseWriter, r *http.Request, workspaceID, agentID, agentName, resumeID string) {
	ws, ok := s.workspace(workspaceID)
	if !ok {
		s.fail(w, http.StatusNotFound, "unknown_workspace", "unknown workspace")
		return
	}
	ag, err := s.agentOrDefault(r.Context(), agentID)
	if err != nil {
		if errors.Is(err, adapter.ErrNotFound) {
			s.fail(w, http.StatusNotFound, "unknown_agent", "unknown agent source")
			return
		}
		s.log.Warn("resolving default agent", "error", err)
		s.fail(w, http.StatusFailedDependency, "default_agent_failed",
			"docker-agent's built-in agent could not be loaded; check `docker agent doctor`")
		return
	}

	// Single-writer rule: a session already driven by this process is never
	// opened a second time. The second browser attaches to the live chat.
	if resumeID != "" {
		s.mu.Lock()
		if chatID, live := s.bySession[resumeID]; live {
			existing := s.chats[chatID]
			s.mu.Unlock()
			if existing != nil {
				s.json(w, http.StatusOK, protocol.ChatRef{ChatID: existing.id, SessionID: resumeID})
				return
			}
		} else {
			s.mu.Unlock()
		}
	}

	chatID := newOpaqueID("chat")
	c, err := s.adapter.OpenChat(r.Context(), adapter.OpenRequest{
		ChatID: chatID, WorkingDir: ws.path, Source: ag.source, Kind: ag.kind,
		AgentName: agentName, ResumeSessionID: resumeID,
		// New chats start in the server's configured default safety mode.
		// Resumed sessions keep the mode stored with the session.
		Posture: postureForOpen(s.defaultPosture, resumeID),
	})
	if err != nil {
		switch {
		case errors.Is(err, adapter.ErrNotFound):
			s.fail(w, http.StatusNotFound, "unknown_session", "unknown session")
		case errors.Is(err, adapter.ErrNoModel):
			s.fail(w, http.StatusFailedDependency, "no_model",
				"no model could be resolved; run `docker agent setup` or `docker agent doctor` on this machine")
		default:
			s.log.Warn("open chat failed", "error", err)
			s.fail(w, http.StatusBadRequest, "open_failed", "this chat could not be started")
		}
		return
	}

	sessionID := c.SessionID()
	s.mu.Lock()
	if otherID, live := s.bySession[sessionID]; live {
		other := s.chats[otherID]
		s.mu.Unlock()
		_ = c.Close(r.Context())
		if other != nil {
			s.json(w, http.StatusOK, protocol.ChatRef{ChatID: other.id, SessionID: sessionID})
			return
		}
		s.fail(w, http.StatusConflict, "session_in_use", "that session is already open in this server")
		return
	}
	lc := newLiveChat(chatID, ws.id, ag.id, c)
	lc.generation = 1
	s.chats[chatID] = lc
	s.bySession[sessionID] = chatID
	s.mu.Unlock()

	if err := lc.hydrate(r.Context()); err != nil {
		s.disposeChat(r.Context(), chatID, "hydrate failed")
		s.fail(w, http.StatusInternalServerError, "snapshot_failed",
			"the session history could not be read")
		return
	}
	go lc.pump(1, c.Events())

	if resolved := ag.resolved; resolved != nil {
		for _, warn := range resolved.Warnings {
			lc.publish(protocol.Event{Type: protocol.EventNotice, Notice: &protocol.Notice{
				ID: "load:" + warn, Level: protocol.NoticeWarning, Message: warn, Code: "load_warning"}})
		}
	}

	s.json(w, http.StatusCreated, protocol.ChatRef{ChatID: chatID, SessionID: sessionID})
}

func (s *Server) chat(id string) (*liveChat, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[id]
	return c, ok
}

func (s *Server) mustChat(w http.ResponseWriter, r *http.Request) (*liveChat, bool) {
	c, ok := s.chat(r.PathValue("id"))
	if !ok {
		s.fail(w, http.StatusNotFound, "unknown_chat", "unknown chat")
		return nil, false
	}
	return c, true
}

func (s *Server) handleGetChat(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	s.json(w, http.StatusOK, c.snapshot())
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	req, ok := decode[protocol.SendMessageRequest](w, r, s)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		s.fail(w, http.StatusBadRequest, "empty_message", "the message is empty")
		return
	}
	if len(req.Text) > 100_000 {
		s.fail(w, http.StatusRequestEntityTooLarge, "message_too_large", "that message is too long")
		return
	}
	switch req.Mode {
	case protocol.DeliveryNormal, protocol.DeliverySteer, protocol.DeliveryFollowUp:
	case "":
		req.Mode = protocol.DeliveryNormal
	default:
		s.fail(w, http.StatusBadRequest, "invalid_mode", "unknown delivery mode")
		return
	}

	// Idempotency: a retried submit with the same key is accepted once,
	// mirroring the upstream API server's idempotency-key behaviour.
	if key := strings.TrimSpace(req.IdempotencyKey); key != "" {
		c.mu.Lock()
		prev, seen := c.idem[key]
		c.mu.Unlock()
		if seen {
			s.json(w, http.StatusAccepted, prev)
			return
		}
	}

	runID, queued, err := c.chat.Send(r.Context(), req.Text, req.Mode)
	if err != nil {
		switch {
		case errors.Is(err, adapter.ErrBusy):
			s.fail(w, http.StatusConflict, "busy",
				"the agent is busy; use steer or follow-up while a turn is running")
		case errors.Is(err, adapter.ErrClosed):
			s.fail(w, http.StatusGone, "chat_closed", "this chat has been closed")
		default:
			s.log.Warn("send failed", "error", err)
			s.fail(w, http.StatusBadRequest, "send_failed", "the message could not be delivered")
		}
		return
	}
	res := protocol.Accepted{Accepted: true, Mode: req.Mode, RunID: runID, Queued: queued}
	if key := strings.TrimSpace(req.IdempotencyKey); key != "" {
		c.mu.Lock()
		if len(c.idem) > 256 {
			c.idem = map[string]protocol.Accepted{}
		}
		c.idem[key] = res
		c.mu.Unlock()
	}
	s.json(w, http.StatusAccepted, res)
}

func (s *Server) handleAbort(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	c.chat.Abort()
	s.json(w, http.StatusAccepted, protocol.Accepted{Accepted: true})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	req, ok := decode[protocol.UpdateConfigRequest](w, r, s)
	if !ok {
		return
	}
	c.mu.Lock()
	busy := c.run.State != protocol.RunStateIdle
	c.mu.Unlock()
	if busy {
		s.fail(w, http.StatusConflict, "busy", "configuration can only change while the agent is idle")
		return
	}
	apply := func(err error) bool {
		if err == nil {
			return true
		}
		switch {
		case errors.Is(err, adapter.ErrUnsupported):
			s.fail(w, http.StatusNotImplemented, "unsupported",
				"this runtime does not support that change")
		case errors.Is(err, adapter.ErrBusy):
			s.fail(w, http.StatusConflict, "busy", "the agent is busy")
		default:
			s.fail(w, http.StatusBadRequest, "config_failed", "that setting could not be applied")
		}
		return false
	}
	if req.Model != nil {
		if !apply(c.chat.SetModel(r.Context(), *req.Model)) {
			return
		}
	}
	if req.ThinkingLevel != nil {
		if !apply(c.chat.SetThinking(r.Context(), *req.ThinkingLevel)) {
			return
		}
	}
	if req.Posture != nil {
		if *req.Posture == protocol.PostureAutonomous && !req.ConfirmAutoApprove {
			s.fail(w, http.StatusPreconditionRequired, "confirm_auto_approve",
				"switching to autonomous requires an explicit confirmAutoApprove: it approves EVERY tool call")
			return
		}
		if !apply(c.chat.SetPosture(r.Context(), *req.Posture)) {
			return
		}
	}
	meta := c.chat.Meta()
	meta.ChatID = c.id
	meta.WorkspaceID = c.workspaceID
	meta.AgentID = c.agentID
	c.publish(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})
	s.json(w, http.StatusOK, meta)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	models := c.chat.Models(r.Context())
	if models == nil {
		models = []protocol.ModelOption{}
	}
	s.json(w, http.StatusOK, models)
}

func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	cmds := c.chat.Commands(r.Context())
	if cmds == nil {
		cmds = []protocol.CommandInfo{}
	}
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	s.json(w, http.StatusOK, cmds)
}

func (s *Server) handleToolConfirmation(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	req, ok := decode[protocol.ToolConfirmationReply](w, r, s)
	if !ok {
		return
	}
	switch req.Decision {
	case protocol.DecisionApprove, protocol.DecisionApproveSession,
		protocol.DecisionApproveAlways, protocol.DecisionReject:
	default:
		s.fail(w, http.StatusBadRequest, "invalid_decision", "unknown decision")
		return
	}
	c.mu.Lock()
	_, pending := c.pendingC[req.ToolCallID]
	c.mu.Unlock()
	if !pending {
		s.fail(w, http.StatusNotFound, "unknown_confirmation", "no such pending confirmation")
		return
	}
	if err := c.chat.Confirm(r.Context(), req.ToolCallID, req.Decision, req.Reason); err != nil {
		s.fail(w, http.StatusConflict, "confirm_failed", "that confirmation is no longer pending")
		return
	}
	s.json(w, http.StatusAccepted, protocol.Accepted{Accepted: true})
}

func (s *Server) handleElicitation(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	req, ok := decode[protocol.ElicitationReply](w, r, s)
	if !ok {
		return
	}
	switch req.Action {
	case protocol.ElicitAccept, protocol.ElicitDecline, protocol.ElicitCancel:
	default:
		s.fail(w, http.StatusBadRequest, "invalid_action", "unknown elicitation action")
		return
	}
	c.mu.Lock()
	_, pending := c.pendingE[req.ElicitationID]
	c.mu.Unlock()
	if !pending {
		s.fail(w, http.StatusNotFound, "unknown_elicitation", "no such pending elicitation")
		return
	}
	if err := c.chat.Elicit(r.Context(), req.ElicitationID, req.Action, req.Content); err != nil {
		s.fail(w, http.StatusConflict, "elicit_failed", "that elicitation is no longer pending")
		return
	}
	s.json(w, http.StatusAccepted, protocol.Accepted{Accepted: true})
}

func (s *Server) handleRetitle(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	req, ok := decode[protocol.RetitleRequest](w, r, s)
	if !ok {
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" || len(title) > 200 {
		s.fail(w, http.StatusBadRequest, "invalid_title", "the title must be 1-200 characters")
		return
	}
	if err := c.chat.Retitle(r.Context(), title); err != nil {
		s.fail(w, http.StatusBadRequest, "retitle_failed", "the title could not be updated")
		return
	}
	s.json(w, http.StatusOK, protocol.Accepted{Accepted: true})
}

func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	if err := c.chat.Compact(r.Context()); err != nil {
		if errors.Is(err, adapter.ErrBusy) {
			s.fail(w, http.StatusConflict, "busy", "compaction requires an idle agent")
			return
		}
		s.fail(w, http.StatusBadRequest, "compact_failed", "compaction could not be started")
		return
	}
	s.json(w, http.StatusAccepted, protocol.Accepted{Accepted: true})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	s.json(w, http.StatusOK, c.chat.Stats(r.Context()))
}

func (s *Server) handleDispose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.chat(id); !ok {
		s.fail(w, http.StatusNotFound, "unknown_chat", "unknown chat")
		return
	}
	s.disposeChat(r.Context(), id, "disposed")
	s.json(w, http.StatusOK, protocol.Accepted{Accepted: true})
}

func (s *Server) disposeChat(ctx context.Context, chatID, reason string) {
	s.mu.Lock()
	c := s.chats[chatID]
	if c != nil {
		delete(s.chats, chatID)
		delete(s.bySession, c.chat.SessionID())
	}
	s.mu.Unlock()
	if c != nil {
		c.close(ctx, reason)
	}
}

// Shutdown disposes every live chat and closes the adapter (and with it the
// single shared session store).
func (s *Server) Shutdown(ctx context.Context) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.chats))
	for id := range s.chats {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.disposeChat(ctx, id, "server shutting down")
	}
	_ = s.adapter.Close()
}

// ---------------------------------------------------------------------------
// SSE
// ---------------------------------------------------------------------------

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.fail(w, http.StatusInternalServerError, "no_streaming", "streaming is unavailable")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Content-Encoding", "identity")
	w.WriteHeader(http.StatusOK)

	var lastID uint64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil {
			lastID = n
		}
	} else if v := r.URL.Query().Get("lastEventId"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			lastID = n
		}
	}

	sub, replay, resumed := c.subscribe(lastID)
	defer c.unsubscribe(sub)

	write := func(ev protocol.Event) bool {
		buf, err := json.Marshal(ev)
		if err != nil {
			return true
		}
		if ev.Seq > 0 {
			if _, err := fmt.Fprintf(w, "id: %d\n", ev.Seq); err != nil {
				return false
			}
		}
		// Deliberately unnamed events: the discriminator already lives in the
		// JSON payload, and unnamed events reach EventSource.onmessage without
		// the client having to register a listener per type.
		if _, err := fmt.Fprintf(w, "data: %s\n\n", buf); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if resumed {
		for _, ev := range replay {
			if !write(ev) {
				return
			}
		}
	} else {
		if lastID > 0 {
			// The resume point fell out of the ring buffer: tell the client
			// explicitly, then resnapshot (upstream's gapEvent semantics).
			if !write(protocol.Event{Type: protocol.EventGap}) {
				return
			}
		}
		snap := c.snapshot()
		if !write(protocol.Event{Type: protocol.EventSnapshot, Seq: snap.Seq, Snapshot: &snap}) {
			return
		}
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.ch:
			if !open {
				return
			}
			if !write(ev) {
				return
			}
		case <-ticker.C:
			// SSE comment: invisible to EventSource, carries no id.
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
