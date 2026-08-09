package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/chatprefs"
	"github.com/rumpl/daw/internal/executionlocations"
	"github.com/rumpl/daw/internal/plugins"
	"github.com/rumpl/daw/internal/protocol"
	"github.com/rumpl/daw/internal/sessioncontext"
	"github.com/rumpl/daw/internal/sessionlineage"
)

func (s *Server) handleCreateChat(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[protocol.CreateChatRequest](w, r, s)
	if !ok {
		return
	}
	s.openChat(w, r, req.WorkspaceID, "", req.ExecutionLocationID, nil, r.Header.Get("X-DAW-Session-Context"), r.Header.Get("X-DAW-Plugin-ID"))
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
	ws, ok := s.workspaces.Get(req.WorkspaceID)
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
	var stored *protocol.SessionSummary
	for i := range list {
		if list[i].SessionID == req.SessionID {
			stored = &list[i]
			break
		}
	}
	if stored == nil {
		s.fail(w, http.StatusNotFound, "unknown_session", "unknown session")
		return
	}
	logicalPath := stored.WorkingDir
	if stored.Attributes[executionlocations.AttributeLocationType] == executionlocations.LocationType {
		logicalPath = stored.Attributes[executionlocations.AttributeWorkspacePath]
	}
	if logicalPath != ws.Path {
		s.fail(w, http.StatusNotFound, "unknown_session", "unknown session")
		return
	}
	s.openChat(w, r, req.WorkspaceID, req.SessionID, "", stored, "", "")
}

func (s *Server) openChat(w http.ResponseWriter, r *http.Request, workspaceID, resumeID, executionLocationID string, stored *protocol.SessionSummary, contextToken, pluginID string) {
	ws, ok := s.workspaces.Get(workspaceID)
	if !ok {
		s.fail(w, http.StatusNotFound, "unknown_workspace", "unknown workspace")
		return
	}

	// Single-writer rule: a session already driven by this process is never
	// opened a second time. The second browser attaches to the live chat.
	if resumeID != "" {
		if existing := s.chats.session(resumeID); existing != nil {
			s.log.Info("attached to live chat", "chat", existing.id, "session", resumeID, "workspace", workspaceID)
			s.json(w, http.StatusOK, protocol.ChatRef{ChatID: existing.id, SessionID: resumeID})
			return
		}
	}

	workingDir := ws.Path
	var attributes map[string]string
	if contextToken != "" {
		if pluginID == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-DAW-Plugin-Token")), []byte(s.backends.internalToken)) != 1 || !s.pluginManagement.running(pluginID) {
			s.fail(w, http.StatusForbidden, "invalid_session_context", "the session creation context is unavailable")
			return
		}
		creation, found := s.sessionContexts.Resolve(contextToken)
		parent, live := s.chat(creation.ParentChatID)
		if !found || !live || parent.workspaceID != workspaceID {
			s.fail(w, http.StatusBadRequest, "invalid_session_context", "the session creation context is invalid or expired")
			return
		}
		parentOrigin := sessionlineage.FromAttributes(parent.chat.Meta().Attributes)
		rootSessionID := parentOrigin.RootSessionID
		if rootSessionID == "" {
			rootSessionID = parent.chat.SessionID()
		}
		attributes = sessionlineage.Origin{
			ParentSessionID: parent.chat.SessionID(), RootSessionID: rootSessionID,
			Kind: sessionlineage.KindAgent, PluginID: pluginID,
		}.Attributes()
	}
	persistImmediately := false
	if resumeID != "" && stored != nil && stored.Attributes[executionlocations.AttributeLocationType] == executionlocations.LocationType {
		if stored.Attributes[executionlocations.AttributeWorkspacePath] != ws.Path ||
			stored.Attributes[executionlocations.AttributeLocationID] == "" ||
			stored.Attributes[executionlocations.AttributeLocationOwner] == "" {
			s.fail(w, http.StatusConflict, "execution_location_unavailable", "this session's execution location is invalid")
			return
		}
		resolved, err := s.guard.ResolveDir(stored.WorkingDir)
		if err != nil {
			s.log.Warn("validate session execution location failed", "workspace", workspaceID, "session", resumeID, "error", err)
			s.fail(w, http.StatusConflict, "execution_location_unavailable", "this session's execution directory is missing or invalid")
			return
		}
		workingDir = resolved
		attributes = sessionlineage.Merge(attributes, stored.Attributes)
	} else if executionLocationID != "" {
		location, err := s.executionLocations.Consume(executionLocationID, ws.Path)
		if err != nil {
			s.fail(w, http.StatusConflict, "execution_location_unavailable", "the execution location is expired, unknown, or belongs to another workspace")
			return
		}
		workingDir = location.WorkingDir
		attributes = sessionlineage.Merge(attributes, location.Attributes())
		persistImmediately = true
	}

	chatID := newOpaqueID("chat")
	creationContext := s.sessionContexts.Issue(sessioncontext.Context{ParentChatID: chatID})
	preference := s.preferences.Get(resumeID)
	c, err := s.adapter.OpenChat(r.Context(), adapter.OpenRequest{
		ChatID: chatID, WorkingDir: workingDir, ResumeSessionID: resumeID,
		SessionAttributes:  attributes,
		PersistImmediately: persistImmediately,
		Model:              preference.Model,
		ThinkingLevel:      preference.ThinkingLevel,
		MCPServers:         plugins.MCPServers(s.pluginDir, workingDir, chatID, creationContext, s.pluginManagement.running),
	})
	if err != nil {
		s.sessionContexts.Revoke(creationContext)
		switch {
		case errors.Is(err, adapter.ErrNotFound):
			s.fail(w, http.StatusNotFound, "unknown_session", "unknown session")
		case errors.Is(err, adapter.ErrNoModel):
			s.fail(w, http.StatusFailedDependency, "no_model",
				"no model could be resolved; run `docker agent setup` or `docker agent doctor` on this machine")
		default:
			s.log.Warn("open chat failed", "workspace", workspaceID, "session", resumeID, "error", err)
			s.fail(w, http.StatusBadRequest, "open_failed", "this chat could not be started")
		}
		return
	}
	// Location-backed sessions are persisted during OpenChat so their execution
	// directory remains discoverable if the server restarts before a prompt.

	sessionID := c.SessionID()
	// A new chat may have inherited the persisted defaults without the user
	// touching either control in this session. Bind those values to its new
	// session ID now, so its thinking level (which docker-agent's session schema
	// does not store) is still restored after a later server restart.
	if resumeID == "" && (preference.Model != "" || preference.ThinkingLevel != "") {
		if err := s.preferences.Remember(sessionID, preference); err != nil {
			s.log.Error("persist new chat preferences", "error", err)
			s.sessionContexts.Revoke(creationContext)
			_ = c.Close(r.Context())
			s.fail(w, http.StatusInternalServerError, "preference_save_failed",
				"the chat opened but its settings could not be saved to disk")
			return
		}
	}
	lc := newLiveChat(chatID, ws.ID, c)
	lc.creationContext = creationContext
	lc.onIndexChange = func(sessionID, workspaceID, reason string) {
		s.publishSessionsChanged(workspaceID, sessionID, reason)
	}
	lc.generation = 1
	if other := s.chats.register(sessionID, lc); other != nil {
		s.sessionContexts.Revoke(creationContext)
		_ = c.Close(r.Context())
		s.log.Info("attached to concurrently opened chat", "chat", other.id, "session", sessionID, "workspace", workspaceID)
		s.json(w, http.StatusOK, protocol.ChatRef{ChatID: other.id, SessionID: sessionID})
		return
	}

	if err := lc.hydrate(r.Context()); err != nil {
		s.disposeChat(r.Context(), chatID, "hydrate failed")
		s.fail(w, http.StatusInternalServerError, "snapshot_failed",
			"the session history could not be read")
		return
	}
	go lc.pump(1, c.Events())
	s.publishSessionsChanged(ws.ID, sessionID, "opened")
	s.log.Info("chat opened", "chat", chatID, "session", sessionID, "workspace", ws.ID, "resumed", resumeID != "")

	s.json(w, http.StatusCreated, protocol.ChatRef{ChatID: chatID, SessionID: sessionID})
}

func (s *Server) chat(id string) (*liveChat, bool) {
	return s.chats.get(id)
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
	if strings.TrimSpace(req.Text) == "" && len(req.Attachments) == 0 {
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

	attachments, ok := c.takeAttachments(req.Attachments)
	if !ok {
		s.fail(w, http.StatusBadRequest, "unknown_attachment", "an attachment is missing or has already been sent")
		return
	}
	mode, runID, queued, err := c.chat.Send(r.Context(), req.Text, attachments, req.Mode)
	if err != nil {
		switch {
		case errors.Is(err, adapter.ErrBusy):
			s.fail(w, http.StatusConflict, "busy",
				"the agent is busy; use steer or follow-up while a turn is running")
		case errors.Is(err, adapter.ErrClosed):
			s.fail(w, http.StatusGone, "chat_closed", "this chat has been closed")
		default:
			s.log.Warn("send message failed", "chat", c.id, "session", c.chat.SessionID(), "mode", req.Mode, "error", err)
			s.fail(w, http.StatusBadRequest, "send_failed", "the message could not be delivered")
		}
		return
	}
	res := protocol.Accepted{Accepted: true, Mode: mode, RunID: runID, Queued: queued}
	s.log.Info("message accepted", "chat", c.id, "session", c.chat.SessionID(), "mode", mode, "run", runID, "queued", queued, "attachments", len(attachments))
	s.json(w, http.StatusAccepted, res)
}

func (s *Server) handleAbort(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	c.chat.Abort()
	s.log.Info("chat abort requested", "chat", c.id, "session", c.chat.SessionID())
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
	preferencePatch := chatprefs.Preference{}
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
		if err := s.preferences.Remember(c.chat.SessionID(), preferencePatch); err != nil {
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
	s.log.Info("chat configuration updated", "chat", c.id, "session", c.chat.SessionID(), "model_changed", req.Model != nil, "thinking_changed", req.ThinkingLevel != nil)
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
		s.log.Warn("retitle chat failed", "chat", c.id, "session", c.chat.SessionID(), "error", err)
		s.fail(w, http.StatusBadRequest, "retitle_failed", "the title could not be updated")
		return
	}
	s.log.Info("chat retitled", "chat", c.id, "session", c.chat.SessionID())
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
	s.log.Info("chat compaction started", "chat", c.id, "session", c.chat.SessionID())
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
