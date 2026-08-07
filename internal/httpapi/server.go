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
	"github.com/rumpl/daw/internal/plugins"
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
	// WorkspaceHistoryFile persists successfully opened project paths so the
	// list is shared by every browser connected to this server. Empty disables
	// persistence (primarily useful for tests).
	WorkspaceHistoryFile string
	// ChatPreferencesFile persists the last model and thinking-level choices,
	// both as defaults for new chats and as per-session overrides. Empty
	// disables persistence (primarily useful for tests).
	ChatPreferencesFile string
	// PluginDir contains trusted, global browser plugins. It is configured by
	// the operator and is never selected by a browser request.
	PluginDir string
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
	workspaceHistoryFile string
	chatPreferencesFile  string
	pluginDir            string
	preferencesMu        sync.Mutex
	preferences          chatPreferences
	events               *dashboardEvents
	pluginWatcher        *pluginWatcher

	mu         sync.Mutex
	workspaces map[string]*workspaceEntry
	chats      map[string]*liveChat
	bySession  map[string]string // sessionId -> chatId (canonical ownership)
	hintsWS    []protocol.WorkspaceHint
}

type workspaceEntry struct {
	id   string
	path string
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
	s := &Server{
		workspaceHistoryFile: strings.TrimSpace(opts.WorkspaceHistoryFile),
		chatPreferencesFile:  strings.TrimSpace(opts.ChatPreferencesFile),
		pluginDir:            strings.TrimSpace(opts.PluginDir),
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
		workspaces:           map[string]*workspaceEntry{},
		chats:                map[string]*liveChat{},
		bySession:            map[string]string{},
		events:               newDashboardEvents(),
	}
	s.loadWorkspaceHistory()
	s.loadChatPreferences()
	s.pluginWatcher = startPluginWatcher(s.pluginDir, s.events, func(err error) {
		s.log.Warn("plugin watcher", "error", err)
	})
	s.routes()
	return s
}

// CSRFToken exposes the per-process token (tests only need it via bootstrap).
func (s *Server) CSRFToken() string { return s.csrf }

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/health", s.handleHealth)
	m.HandleFunc("GET /api/bootstrap", s.handleBootstrap)
	m.HandleFunc("GET /api/events", s.handleDashboardEvents)
	m.HandleFunc("GET /api/plugins", s.handlePlugins)
	m.HandleFunc("GET /api/plugins/{pluginId}/assets/{fingerprint}/{path...}", s.handlePluginAsset)
	m.HandleFunc("POST /api/workspaces/open", s.handleOpenWorkspace)
	m.HandleFunc("GET /api/sessions/live", s.handleListLiveSessions)
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
	s.json(w, http.StatusOK, protocol.Health{
		Status: "ok",
		Uptime: int64(time.Since(s.started).Seconds()),
	})
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
	s.mu.Unlock()

	notices := append([]protocol.Notice(nil), info.Notices...)
	notices = append(notices, protocol.Notice{
		ID: "tools-auto-approved", Level: protocol.NoticeWarning, Code: "tools_auto_approved",
		Message: "Every tool call, including shell commands, is auto-approved and runs " +
			"on this host as your user.",
	})
	notices = append(notices, protocol.Notice{
		ID: "sandbox", Level: protocol.NoticeInfo, Code: "no_sandbox",
		Message: "This dashboard embeds docker-agent in-process: tools run directly on this host " +
			"with your user's permissions. There is no sandbox. Use `docker agent run --sandbox` " +
			"in a terminal if you need isolation.",
	})

	s.json(w, http.StatusOK, protocol.Bootstrap{
		AppVersion: s.appVersion, AgentVersion: info.AgentVersion, AgentCommit: info.AgentCommit,
		ConfigDir: info.ConfigDir, DataDir: info.DataDir, CacheDir: info.CacheDir,
		SessionDB: info.SessionDB, PluginDir: s.pluginDir,
		CSRFToken: s.csrf, Sandboxed: false,
		ModelsAvailable: info.ModelsAvailable, ModelsHint: info.ModelsHint,
		WorkspaceHints: wsHints, Notices: notices,
	})
}

func (s *Server) handlePlugins(w http.ResponseWriter, _ *http.Request) {
	s.json(w, http.StatusOK, plugins.Catalog(s.pluginDir))
}

func (s *Server) handlePluginAsset(w http.ResponseWriter, r *http.Request) {
	path, info, err := plugins.Asset(
		s.pluginDir,
		r.PathValue("pluginId"),
		r.PathValue("fingerprint"),
		r.PathValue("path"),
	)
	if err != nil {
		s.fail(w, http.StatusNotFound, "plugin_asset_not_found", "plugin asset not found")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		s.fail(w, http.StatusNotFound, "plugin_asset_not_found", "plugin asset not found")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", plugins.ContentType(path))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
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
		ws.Notices = append(ws.Notices, protocol.Notice{
			ID: "agentsignore", Level: protocol.NoticeInfo,
			Message: ".agentsignore is present and is honoured by docker-agent's permission checker.",
			Code:    "agentsignore",
		})
	}
	s.json(w, http.StatusOK, ws)
}

func (s *Server) failPath(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pathsec.ErrOutsideRoots):
		s.fail(w, http.StatusForbidden, "outside_roots",
			"that path is outside your home directory")
	case errors.Is(err, pathsec.ErrNotDirectory):
		s.fail(w, http.StatusBadRequest, "not_a_directory", "that path is not a directory")
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

func (s *Server) workspace(id string) (*workspaceEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.workspaces[id]
	return e, ok
}

// handleListLiveSessions returns every session currently owned by this server,
// regardless of project. WorkingDir is taken from the validated workspace used
// to open the chat rather than trusting stale session-store metadata; the
// browser can therefore safely use it to switch projects and attach.
func (s *Server) handleListLiveSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.adapter.ListSessions(r.Context(), "")
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "session_list_failed",
			"the docker-agent session store could not be listed")
		return
	}

	type liveInfo struct {
		path string
		chat *liveChat
	}
	s.mu.Lock()
	liveChats := make(map[string]liveInfo, len(s.bySession))
	for sessionID, chatID := range s.bySession {
		chat := s.chats[chatID]
		if chat == nil {
			continue
		}
		if ws := s.workspaces[chat.workspaceID]; ws != nil {
			liveChats[sessionID] = liveInfo{path: ws.path, chat: chat}
		}
	}
	s.mu.Unlock()

	live := make([]protocol.SessionSummary, 0, len(liveChats))
	for _, summary := range list {
		info, ok := liveChats[summary.SessionID]
		if !ok {
			continue
		}
		state := info.chat.runState()
		summary.Live = true
		summary.ChatID = info.chat.id
		summary.RunState = &state
		summary.WorkingDir = info.path
		live = append(live, summary)
	}
	s.json(w, http.StatusOK, live)
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
	liveChats := make(map[string]*liveChat, len(s.bySession))
	for sessionID, chatID := range s.bySession {
		if chat := s.chats[chatID]; chat != nil {
			liveChats[sessionID] = chat
		}
	}
	s.mu.Unlock()
	for i := range list {
		if chat := liveChats[list[i].SessionID]; chat != nil {
			state := chat.runState()
			list[i].Live = true
			list[i].ChatID = chat.id
			list[i].RunState = &state
		}
	}
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
	s.openChat(w, r, req.WorkspaceID, "")
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
	s.openChat(w, r, req.WorkspaceID, req.SessionID)
}

func (s *Server) openChat(w http.ResponseWriter, r *http.Request, workspaceID, resumeID string) {
	ws, ok := s.workspace(workspaceID)
	if !ok {
		s.fail(w, http.StatusNotFound, "unknown_workspace", "unknown workspace")
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
	preference := s.chatPreference(resumeID)
	c, err := s.adapter.OpenChat(r.Context(), adapter.OpenRequest{
		ChatID: chatID, WorkingDir: ws.path, ResumeSessionID: resumeID,
		Model:         preference.Model,
		ThinkingLevel: preference.ThinkingLevel,
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
	// A new chat may have inherited the persisted defaults without the user
	// touching either control in this session. Bind those values to its new
	// session ID now, so its thinking level (which docker-agent's session schema
	// does not store) is still restored after a later server restart.
	if resumeID == "" && (preference.Model != "" || preference.ThinkingLevel != "") {
		if err := s.rememberChatPreference(sessionID, preference); err != nil {
			s.log.Error("persist new chat preferences", "error", err)
			_ = c.Close(r.Context())
			s.fail(w, http.StatusInternalServerError, "preference_save_failed",
				"the chat opened but its settings could not be saved to disk")
			return
		}
	}
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
	lc := newLiveChat(chatID, ws.id, c)
	lc.onIndexChange = func(sessionID, workspaceID, reason string) {
		s.publishSessionsChanged(workspaceID, sessionID, reason)
	}
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
	s.publishSessionsChanged(ws.id, sessionID, "opened")

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
	preferencePatch := chatPreference{}
	if req.Model != nil {
		if !apply(c.chat.SetModel(r.Context(), *req.Model)) {
			return
		}
		preferencePatch.Model = *req.Model
	}
	if req.ThinkingLevel != nil {
		if !apply(c.chat.SetThinking(r.Context(), *req.ThinkingLevel)) {
			return
		}
		preferencePatch.ThinkingLevel = *req.ThinkingLevel
	}
	if preferencePatch.Model != "" || preferencePatch.ThinkingLevel != "" {
		if err := s.rememberChatPreference(c.chat.SessionID(), preferencePatch); err != nil {
			s.log.Error("persist chat preferences", "error", err)
			s.fail(w, http.StatusInternalServerError, "preference_save_failed",
				"the setting was applied but could not be saved to disk")
			return
		}
	}
	meta := c.chat.Meta()
	meta.ChatID = c.id
	meta.WorkspaceID = c.workspaceID
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
	case protocol.DecisionApprove, protocol.DecisionApproveAlways, protocol.DecisionReject:
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
		s.publishSessionsChanged(c.workspaceID, c.chat.SessionID(), reason)
	}
}

// Shutdown disposes every live chat and closes the adapter (and with it the
// single shared session store).
func (s *Server) Shutdown(ctx context.Context) {
	s.pluginWatcher.close()
	s.events.close()
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

func (s *Server) publishSessionsChanged(workspaceID, sessionID, reason string) {
	s.events.publish(protocol.DashboardEvent{
		Type:         protocol.DashboardEventSessionsChanged,
		WorkspaceIDs: []string{workspaceID}, SessionIDs: []string{sessionID}, Reason: reason,
	})
}

func (s *Server) handleDashboardEvents(w http.ResponseWriter, r *http.Request) {
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
	if value := r.Header.Get("Last-Event-ID"); value != "" {
		lastID, _ = strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	} else if value := r.URL.Query().Get("lastEventId"); value != "" {
		lastID, _ = strconv.ParseUint(value, 10, 64)
	}
	sub, replay, resumed := s.events.subscribe(lastID)
	defer s.events.unsubscribe(sub)
	write := func(ev protocol.DashboardEvent) bool {
		data, err := json.Marshal(ev)
		if err != nil {
			return true
		}
		if ev.Seq > 0 {
			if _, err := fmt.Fprintf(w, "id: %d\n", ev.Seq); err != nil {
				return false
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
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
		if lastID > 0 && !write(protocol.DashboardEvent{Type: protocol.DashboardEventGap}) {
			return
		}
		if !write(s.events.snapshot()) {
			return
		}
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-sub.ch:
			if !open || !write(ev) {
				return
			}
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
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
