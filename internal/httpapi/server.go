package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/chatprefs"
	"github.com/rumpl/daw/internal/executionlocations"
	"github.com/rumpl/daw/internal/pathsec"
	"github.com/rumpl/daw/internal/protocol"
	"github.com/rumpl/daw/internal/workspaces"
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
	// PluginDir contains trusted, global browser and Node backend plugins. It is
	// configured by the operator and is never selected by a browser request.
	PluginDir string
	// PluginAPIOrigin is the loopback origin plugin backends use to call the
	// dashboard API. It must not be a public or forwarded origin.
	PluginAPIOrigin string
	// PluginDataDir stores host-managed plugin configuration and backend data.
	PluginDataDir string
}

// Server is the HTTP transport and application composition root. Stateful
// domain behavior lives in the focused services below.
type Server struct {
	mux            *http.ServeMux
	adapter        adapter.Adapter
	hosts          *hostPolicy
	csrf           string
	appVersion     string
	allowedTSUsers map[string]bool
	static         http.Handler
	log            *slog.Logger
	started        time.Time
	pluginDir      string

	guard              *pathsec.Guard
	workspaces         *workspaces.Service
	chats              *chatRegistry
	preferences        *chatprefs.Service
	executionLocations *executionlocations.Service
	events             *dashboardEvents
	plugins            *pluginWatcher
	backends           *pluginBackendManager
	pluginEvents       *pluginEventHub
	pluginConfig       *pluginConfigStore
	pluginManagement   *pluginManagement
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
		pluginDir:      strings.TrimSpace(opts.PluginDir),
		guard:          opts.Guard,
		mux:            http.NewServeMux(),
		adapter:        opts.Adapter,
		hosts:          newHostPolicy(opts.TailscaleHosts),
		csrf:           newToken(),
		appVersion:     opts.AppVersion,
		allowedTSUsers: users,
		static:         opts.Static,
		log:            log,
		started:        time.Now(),
		events:         newDashboardEvents(),
		pluginEvents:   newPluginEventHub(),
	}
	s.workspaces = workspaces.New(opts.Guard, strings.TrimSpace(opts.WorkspaceHistoryFile), log)
	s.preferences = chatprefs.New(strings.TrimSpace(opts.ChatPreferencesFile), log)
	s.executionLocations = executionlocations.New()
	s.chats = newChatRegistry()
	s.pluginConfig = newPluginConfigStore(filepath.Join(strings.TrimSpace(opts.PluginDataDir), "config"))
	s.pluginManagement = newPluginManagement(strings.TrimSpace(opts.PluginDataDir))
	s.plugins = startPluginWatcher(s.pluginDir, s.events, log)
	s.backends = newPluginBackendManager(s.pluginDir, strings.TrimSpace(opts.PluginDataDir), strings.TrimRight(opts.PluginAPIOrigin, "/"), s.csrf, s.pluginManagement.running, log)
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
	m.HandleFunc("GET /api/plugin-management", s.handlePluginManagement)
	m.HandleFunc("POST /api/plugins/{pluginId}/start", s.handlePluginLifecycle)
	m.HandleFunc("POST /api/plugins/{pluginId}/stop", s.handlePluginLifecycle)
	m.HandleFunc("POST /api/plugins/{pluginId}/enable", s.handlePluginLifecycle)
	m.HandleFunc("POST /api/plugins/{pluginId}/disable", s.handlePluginLifecycle)
	m.HandleFunc("DELETE /api/plugins/{pluginId}", s.handleDeletePlugin)
	m.HandleFunc("GET /api/plugins/{pluginId}/assets/{fingerprint}/{path...}", s.handlePluginAsset)
	m.HandleFunc("POST /api/plugins/{pluginId}/execution-locations", s.handleRegisterExecutionLocation)
	m.HandleFunc("GET /api/plugins/{pluginId}/events", s.handlePluginEvents)
	m.HandleFunc("GET /api/plugins/{pluginId}/config", s.handleGetPluginConfig)
	m.HandleFunc("PUT /api/plugins/{pluginId}/config", s.handlePutPluginConfig)
	m.HandleFunc("POST /api/plugins/{pluginId}/publish", s.handlePluginPublish)
	m.HandleFunc("/api/plugins/{pluginId}/webhooks/{webhookId}", s.handlePluginWebhook)
	m.HandleFunc("GET /api/plugins/{pluginId}/webhooks/{webhookId}/token", s.handlePluginWebhookToken)
	m.HandleFunc("/api/plugins/{pluginId}/backend", s.handlePluginBackend)
	m.HandleFunc("/api/plugins/{pluginId}/backend/{path...}", s.handlePluginBackend)
	m.HandleFunc("POST /api/workspaces/open", s.handleOpenWorkspace)
	m.HandleFunc("GET /api/sessions/live", s.handleListLiveSessions)
	m.HandleFunc("GET /api/workspaces/{workspaceId}/sessions", s.handleListSessions)
	m.HandleFunc("POST /api/chats", s.handleCreateChat)
	m.HandleFunc("POST /api/chats/resume", s.handleResumeChat)
	m.HandleFunc("GET /api/chats/{id}", s.handleGetChat)
	m.HandleFunc("GET /api/chats/{id}/events", s.handleEvents)
	m.HandleFunc("POST /api/chats/{id}/attachments", s.handleAttachment)
	m.HandleFunc("DELETE /api/chats/{id}/attachments/{attachmentId}", s.handleDeleteAttachment)
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
	if isMutation(r.Method) && !s.isAuthenticatedWebhook(r) {
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
// lifecycle
// ---------------------------------------------------------------------------

func (s *Server) disposeChat(ctx context.Context, chatID, reason string) {
	c := s.chats.remove(chatID)
	if c != nil {
		s.log.Info("closing chat", "chat", chatID, "session", c.chat.SessionID(), "workspace", c.workspaceID, "reason", reason)
		c.close(ctx, reason)
		s.publishSessionsChanged(c.workspaceID, c.chat.SessionID(), reason)
	}
}

// Shutdown disposes every live chat and closes the adapter (and with it the
// single shared session store).
func (s *Server) Shutdown(ctx context.Context) {
	s.log.Info("server shutdown started", "live_chats", len(s.chats.all()))
	s.plugins.close()
	s.backends.close(ctx)
	s.pluginEvents.close()
	s.events.close()
	for _, chat := range s.chats.all() {
		s.disposeChat(ctx, chat.id, "server shutting down")
	}
	if err := s.adapter.Close(); err != nil {
		s.log.Warn("closing adapter", "error", err)
	}
	s.log.Info("server shutdown complete")
}

func (s *Server) publishSessionsChanged(workspaceID, sessionID, reason string) {
	s.events.publish(protocol.DashboardEvent{
		Type:         protocol.DashboardEventSessionsChanged,
		WorkspaceIDs: []string{workspaceID}, SessionIDs: []string{sessionID}, Reason: reason,
	})
}
