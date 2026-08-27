package httpapi

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/executionlocations"
	"github.com/rumpl/daw/internal/pathsec"
	"github.com/rumpl/daw/internal/protocol"
	"github.com/rumpl/daw/internal/sessionlineage"
)

func (s *Server) handleOpenWorkspace(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[protocol.OpenWorkspaceRequest](w, r, s)
	if !ok {
		return
	}
	entry, err := s.workspaces.Open(req.Path)
	if err != nil {
		s.log.Warn("open workspace failed", "error", err)
		s.failPath(w, err)
		return
	}
	canon := entry.Path
	s.log.Info("workspace opened", "workspace", entry.ID, "path", canon)

	ws := protocol.Workspace{WorkspaceID: entry.ID, Path: canon, Label: filepath.Base(canon)}
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

// handleListLiveSessions returns every session currently owned by this server,
// regardless of project. WorkingDir is taken from the validated workspace used
// to open the chat rather than trusting stale session-store metadata; the
// browser can therefore safely use it to switch projects and attach.

func (s *Server) exposeSession(summary *protocol.SessionSummary) {
	origin := sessionlineage.FromAttributes(summary.Attributes)
	summary.ParentSessionID = origin.ParentSessionID
	summary.RootSessionID = origin.RootSessionID
	summary.OriginKind = origin.Kind
	summary.OriginPluginID = origin.PluginID
	if summary.ExecutionTarget == "" {
		summary.ExecutionTarget = s.defaultExecutionTarget
	}
}

func (s *Server) handleListLiveSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.adapter.ListSessions(r.Context(), "")
	if err != nil {
		s.log.Warn("list live sessions failed", "error", err)
		s.fail(w, http.StatusInternalServerError, "session_list_failed",
			"the docker-agent session store could not be listed")
		return
	}

	type liveInfo struct {
		path string
		chat *liveChat
	}
	indexed := s.chats.bySessionSnapshot()
	liveChats := make(map[string]liveInfo, len(indexed))
	for sessionID, chat := range indexed {
		if path, ok := s.workspaces.Path(chat.workspaceID); ok {
			liveChats[sessionID] = liveInfo{path: path, chat: chat}
		}
	}

	live := make([]protocol.SessionSummary, 0, len(liveChats))
	for _, summary := range list {
		s.exposeSession(&summary)
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

func (s *Server) storedSessionForWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	ws, ok := s.workspaces.Get(r.PathValue("workspaceId"))
	if !ok {
		s.fail(w, http.StatusNotFound, "unknown_workspace", "unknown workspace")
		return "", false
	}
	list, err := s.adapter.ListSessions(r.Context(), "")
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "session_list_failed",
			"the docker-agent session store could not be listed")
		return "", false
	}
	for i := range list {
		if list[i].SessionID != r.PathValue("sessionId") {
			continue
		}
		logicalPath := list[i].WorkingDir
		if list[i].Attributes[executionlocations.AttributeLocationType] == executionlocations.LocationType {
			logicalPath = list[i].Attributes[executionlocations.AttributeWorkspacePath]
		}
		if logicalPath == ws.Path {
			return ws.Path, true
		}
		break
	}
	s.fail(w, http.StatusNotFound, "unknown_session", "unknown session")
	return "", false
}

func (s *Server) readStoredSession(w http.ResponseWriter, r *http.Request) (adapter.StoredSession, string, bool) {
	workspacePath, ok := s.storedSessionForWorkspace(w, r)
	if !ok {
		return adapter.StoredSession{}, "", false
	}
	stored, err := s.adapter.ReadSession(r.Context(), r.PathValue("sessionId"))
	if err != nil {
		if errors.Is(err, adapter.ErrNotFound) {
			s.fail(w, http.StatusNotFound, "unknown_session", "unknown session")
		} else {
			s.log.Warn("read stored session failed", "session", r.PathValue("sessionId"), "error", err)
			s.fail(w, http.StatusInternalServerError, "session_read_failed", "the session history could not be read")
		}
		return adapter.StoredSession{}, "", false
	}
	return stored, workspacePath, true
}

func (s *Server) handleGetStoredSession(w http.ResponseWriter, r *http.Request) {
	stored, workspacePath, ok := s.readStoredSession(w, r)
	if !ok {
		return
	}
	stored.Meta.WorkspaceID = r.PathValue("workspaceId")
	stored.Meta.WorkingDir = workspacePath
	s.json(w, http.StatusOK, protocol.StoredSession{
		Meta: stored.Meta, Usage: stored.Usage, Stats: stored.Stats,
		Live: s.chats.session(stored.Meta.SessionID) != nil,
	})
}

const (
	defaultStoredItemLimit = 200
	maxStoredItemLimit     = 1000
)

func (s *Server) handleGetStoredSessionItems(w http.ResponseWriter, r *http.Request) {
	stored, _, ok := s.readStoredSession(w, r)
	if !ok {
		return
	}
	offset, err := nonNegativeQueryInt(r, "offset", 0)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_pagination", "offset must be a non-negative integer")
		return
	}
	limit, err := nonNegativeQueryInt(r, "limit", defaultStoredItemLimit)
	if err != nil || limit < 1 || limit > maxStoredItemLimit {
		s.fail(w, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 1000")
		return
	}
	total := len(stored.Items)
	start := min(offset, total)
	end := min(start+limit, total)
	items := append([]protocol.Item(nil), stored.Items[start:end]...)
	var next *int
	if end < total {
		n := end
		next = &n
	}
	s.json(w, http.StatusOK, protocol.StoredSessionItems{
		Items: items, Offset: offset, Limit: limit, Total: total, NextOffset: next,
	})
}

func nonNegativeQueryInt(r *http.Request, key string, fallback int) (int, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, errors.New("invalid non-negative integer")
	}
	return n, nil
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.workspaces.Get(r.PathValue("workspaceId"))
	if !ok {
		s.fail(w, http.StatusNotFound, "unknown_workspace", "unknown workspace")
		return
	}
	list, err := s.adapter.ListSessions(r.Context(), "")
	if err != nil {
		s.log.Warn("list workspace sessions failed", "workspace", ws.ID, "error", err)
		s.fail(w, http.StatusInternalServerError, "session_list_failed",
			"the docker-agent session store could not be listed")
		return
	}
	liveChats := s.chats.bySessionSnapshot()
	filtered := make([]protocol.SessionSummary, 0, len(list))
	for i := range list {
		s.exposeSession(&list[i])
		logicalPath := list[i].WorkingDir
		if list[i].Attributes[executionlocations.AttributeLocationType] == executionlocations.LocationType {
			logicalPath = list[i].Attributes[executionlocations.AttributeWorkspacePath]
		}
		if logicalPath != ws.Path {
			continue
		}
		// Session navigation is by logical workspace; the actual execution path
		// remains available in chat metadata.
		list[i].WorkingDir = ws.Path
		if chat := liveChats[list[i].SessionID]; chat != nil {
			state := chat.runState()
			list[i].Live = true
			list[i].ChatID = chat.id
			list[i].RunState = &state
		}
		filtered = append(filtered, list[i])
	}
	list = filtered
	if list == nil {
		list = []protocol.SessionSummary{}
	}
	s.json(w, http.StatusOK, list)
}
