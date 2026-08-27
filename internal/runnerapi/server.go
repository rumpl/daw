// Package runnerapi exposes the docker-agent adapter over a small authenticated
// HTTP transport. It is used only between the host DAW control plane and the
// sandbox-local runner.
package runnerapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/protocol"
)

const MaxBodyBytes = 32 << 20

type Server struct {
	adapter adapter.Adapter
	token   string
	mux     *http.ServeMux
	mu      sync.Mutex
	chats   map[string]adapter.Chat
}

type OpenResponse struct {
	ChatID         string               `json:"chatId"`
	SessionID      string               `json:"sessionId"`
	Meta           protocol.SessionMeta `json:"meta"`
	MetaAttributes map[string]string    `json:"metaAttributes,omitempty"`
}

// SessionSummary carries adapter-only attributes across the runner boundary.
// protocol.SessionSummary intentionally suppresses them from the browser JSON,
// but the host control plane needs them to reconstruct gossip lineage.
type SessionSummary struct {
	protocol.SessionSummary
	Attributes map[string]string `json:"attributes,omitempty"`
}

type MetaResponse struct {
	Meta       protocol.SessionMeta `json:"meta"`
	Attributes map[string]string    `json:"attributes,omitempty"`
}

type StreamEvent struct {
	protocol.Event
	MetaAttributes map[string]string `json:"metaAttributes,omitempty"`
}

type SnapshotResponse struct {
	Items []protocol.Item `json:"items"`
	Usage protocol.Usage  `json:"usage"`
}

type ChatOptionsRequest struct {
	Model      string              `json:"model"`
	MCPServers []adapter.MCPServer `json:"mcpServers"`
}

type ChatOptionsResponse struct {
	Models         []protocol.ModelOption `json:"models"`
	ThinkingLevels []string               `json:"thinkingLevels"`
	Tools          []protocol.ToolOption  `json:"tools"`
}

type SendRequest struct {
	Text        string                `json:"text"`
	Attachments []adapter.Attachment  `json:"attachments,omitempty"`
	Mode        protocol.DeliveryMode `json:"mode"`
}

type SendResponse struct {
	Mode   protocol.DeliveryMode `json:"mode"`
	RunID  string                `json:"runId"`
	Queued bool                  `json:"queued"`
}

type ConfirmRequest struct {
	ToolCallID string                `json:"toolCallId"`
	Decision   protocol.ToolDecision `json:"decision"`
	Reason     string                `json:"reason"`
}

type ElicitRequest struct {
	ElicitationID string                     `json:"elicitationId"`
	Action        protocol.ElicitationAction `json:"action"`
	Content       map[string]any             `json:"content,omitempty"`
}

type ValueRequest struct {
	Value string `json:"value"`
}

type ToolsRequest struct {
	Names []string `json:"names"`
}

func New(ad adapter.Adapter, token string) *Server {
	s := &Server{adapter: ad, token: token, mux: http.NewServeMux(), chats: map[string]adapter.Chat{}}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.token == "" || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), []byte(s.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		s.json(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	m.HandleFunc("GET /v1/info", s.info)
	m.HandleFunc("GET /v1/settings/models-gateway", s.modelsGateway)
	m.HandleFunc("PUT /v1/settings/models-gateway", s.setModelsGateway)
	m.HandleFunc("GET /v1/sessions", s.sessions)
	m.HandleFunc("GET /v1/sessions/{id}", s.readSession)
	m.HandleFunc("POST /v1/options", s.options)
	m.HandleFunc("POST /v1/chats", s.open)
	m.HandleFunc("GET /v1/chats/{id}/meta", s.meta)
	m.HandleFunc("GET /v1/chats/{id}/snapshot", s.snapshot)
	m.HandleFunc("GET /v1/chats/{id}/events", s.events)
	m.HandleFunc("POST /v1/chats/{id}/send", s.send)
	m.HandleFunc("POST /v1/chats/{id}/abort", s.abort)
	m.HandleFunc("POST /v1/chats/{id}/confirm", s.confirm)
	m.HandleFunc("POST /v1/chats/{id}/elicit", s.elicit)
	m.HandleFunc("GET /v1/chats/{id}/models", s.models)
	m.HandleFunc("GET /v1/chats/{id}/commands", s.commands)
	m.HandleFunc("POST /v1/chats/{id}/model", s.setModel)
	m.HandleFunc("POST /v1/chats/{id}/thinking", s.setThinking)
	m.HandleFunc("POST /v1/chats/{id}/tools", s.setTools)
	m.HandleFunc("POST /v1/chats/{id}/retitle", s.retitle)
	m.HandleFunc("POST /v1/chats/{id}/compact", s.compact)
	m.HandleFunc("GET /v1/chats/{id}/stats", s.stats)
	m.HandleFunc("DELETE /v1/chats/{id}", s.closeChat)
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	value, err := s.adapter.Info(r.Context())
	s.respond(w, value, err)
}

// The sandbox runs its own docker-agent with its own user configuration, so
// the models gateway must be mirrored into it: the host is the authority and
// pushes the current value on provisioning and on every change.
func (s *Server) modelsGateway(w http.ResponseWriter, r *http.Request) {
	value, err := s.adapter.ModelsGateway(r.Context())
	s.respond(w, protocol.ModelsGatewayConfig{URL: value}, err)
}

func (s *Server) setModelsGateway(w http.ResponseWriter, r *http.Request) {
	request, ok := decode[protocol.UpdateModelsGatewayRequest](w, r)
	if !ok {
		return
	}
	err := s.adapter.SetModelsGateway(r.Context(), request.URL)
	s.respond(w, protocol.ModelsGatewayConfig(request), err)
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	value, err := s.adapter.ListSessions(r.Context(), r.URL.Query().Get("workingDir"))
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	wire := make([]SessionSummary, len(value))
	for i := range value {
		wire[i] = SessionSummary{SessionSummary: value[i], Attributes: value[i].Attributes}
	}
	s.respond(w, wire, nil)
}

func (s *Server) readSession(w http.ResponseWriter, r *http.Request) {
	value, err := s.adapter.ReadSession(r.Context(), r.PathValue("id"))
	s.respond(w, value, err)
}

func (s *Server) options(w http.ResponseWriter, r *http.Request) {
	request, ok := decode[ChatOptionsRequest](w, r)
	if !ok {
		return
	}
	models, levels, tools, err := s.adapter.ChatOptions(r.Context(), request.Model, request.MCPServers)
	s.respond(w, ChatOptionsResponse{Models: models, ThinkingLevels: levels, Tools: tools}, err)
}

func (s *Server) open(w http.ResponseWriter, r *http.Request) {
	request, ok := decode[adapter.OpenRequest](w, r)
	if !ok {
		return
	}
	chat, err := s.adapter.OpenChat(r.Context(), request)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	s.mu.Lock()
	if old := s.chats[request.ChatID]; old != nil {
		_ = old.Close(context.Background())
	}
	s.chats[request.ChatID] = chat
	s.mu.Unlock()
	meta := chat.Meta()
	s.json(w, http.StatusCreated, OpenResponse{
		ChatID: request.ChatID, SessionID: chat.SessionID(), Meta: meta, MetaAttributes: meta.Attributes,
	})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) (adapter.Chat, bool) {
	s.mu.Lock()
	chat := s.chats[r.PathValue("id")]
	s.mu.Unlock()
	if chat == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return nil, false
	}
	return chat, true
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	if c, ok := s.chat(w, r); ok {
		meta := c.Meta()
		s.json(w, http.StatusOK, MetaResponse{Meta: meta, Attributes: meta.Attributes})
	}
}
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chat(w, r)
	if !ok {
		return
	}
	items, usage, err := c.Snapshot(r.Context())
	s.respond(w, SnapshotResponse{Items: items, Usage: usage}, err)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chat(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	encoder := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-c.Events():
			if !open {
				return
			}
			wire := StreamEvent{Event: event}
			if event.Meta != nil {
				wire.MetaAttributes = event.Meta.Attributes
			}
			if encoder.Encode(wire) != nil {
				return
			}
			flusher.Flush()
		}
	}
}
func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chat(w, r)
	if !ok {
		return
	}
	request, valid := decode[SendRequest](w, r)
	if !valid {
		return
	}
	mode, runID, queued, err := c.Send(r.Context(), request.Text, request.Attachments, request.Mode)
	s.respond(w, SendResponse{Mode: mode, RunID: runID, Queued: queued}, err)
}
func (s *Server) abort(w http.ResponseWriter, r *http.Request) {
	if c, ok := s.chat(w, r); ok {
		c.Abort()
		s.json(w, http.StatusOK, map[string]bool{"ok": true})
	}
}
func (s *Server) confirm(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chat(w, r)
	if !ok {
		return
	}
	q, valid := decode[ConfirmRequest](w, r)
	if !valid {
		return
	}
	s.respond(w, map[string]bool{"ok": true}, c.Confirm(r.Context(), q.ToolCallID, q.Decision, q.Reason))
}
func (s *Server) elicit(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chat(w, r)
	if !ok {
		return
	}
	q, valid := decode[ElicitRequest](w, r)
	if !valid {
		return
	}
	s.respond(w, map[string]bool{"ok": true}, c.Elicit(r.Context(), q.ElicitationID, q.Action, q.Content))
}
func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	if c, ok := s.chat(w, r); ok {
		s.json(w, http.StatusOK, c.Models(r.Context()))
	}
}
func (s *Server) commands(w http.ResponseWriter, r *http.Request) {
	if c, ok := s.chat(w, r); ok {
		s.json(w, http.StatusOK, c.Commands(r.Context()))
	}
}
func (s *Server) setModel(w http.ResponseWriter, r *http.Request) {
	s.valueCall(w, r, func(c adapter.Chat, v string) error { return c.SetModel(r.Context(), v) })
}
func (s *Server) setThinking(w http.ResponseWriter, r *http.Request) {
	s.valueCall(w, r, func(c adapter.Chat, v string) error { return c.SetThinking(r.Context(), v) })
}
func (s *Server) retitle(w http.ResponseWriter, r *http.Request) {
	s.valueCall(w, r, func(c adapter.Chat, v string) error { return c.Retitle(r.Context(), v) })
}
func (s *Server) valueCall(w http.ResponseWriter, r *http.Request, call func(adapter.Chat, string) error) {
	c, ok := s.chat(w, r)
	if !ok {
		return
	}
	q, valid := decode[ValueRequest](w, r)
	if !valid {
		return
	}
	s.respond(w, map[string]bool{"ok": true}, call(c, q.Value))
}
func (s *Server) setTools(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chat(w, r)
	if !ok {
		return
	}
	q, valid := decode[ToolsRequest](w, r)
	if !valid {
		return
	}
	c.SetDisabledTools(q.Names)
	s.json(w, http.StatusOK, map[string]bool{"ok": true})
}
func (s *Server) compact(w http.ResponseWriter, r *http.Request) {
	if c, ok := s.chat(w, r); ok {
		s.respond(w, map[string]bool{"ok": true}, c.Compact(r.Context()))
	}
}
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	if c, ok := s.chat(w, r); ok {
		s.json(w, http.StatusOK, c.Stats(r.Context()))
	}
}
func (s *Server) closeChat(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	c := s.chats[r.PathValue("id")]
	delete(s.chats, r.PathValue("id"))
	s.mu.Unlock()
	if c == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.respond(w, map[string]bool{"ok": true}, c.Close(r.Context()))
}

func (s *Server) Shutdown(ctx context.Context) {
	s.mu.Lock()
	chats := s.chats
	s.chats = map[string]adapter.Chat{}
	s.mu.Unlock()
	for _, chat := range chats {
		_ = chat.Close(ctx)
	}
	_ = s.adapter.Close()
}

func (s *Server) respond(w http.ResponseWriter, value any, err error) {
	if err == nil {
		s.json(w, http.StatusOK, value)
		return
	}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, adapter.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, adapter.ErrBusy):
		status = http.StatusConflict
	case errors.Is(err, adapter.ErrUnsupported):
		status = http.StatusNotImplemented
	case errors.Is(err, adapter.ErrNoModel):
		status = http.StatusFailedDependency
	case errors.Is(err, adapter.ErrClosed):
		status = http.StatusGone
	}
	http.Error(w, err.Error(), status)
}
func (s *Server) json(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var value T
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&value); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return value, false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return value, false
	}
	return value, true
}
