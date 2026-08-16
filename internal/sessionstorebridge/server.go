// Package sessionstorebridge exposes a docker-agent session.Store to sandbox
// runners. It is a host callback service and must not be mounted on DAW's
// browser-facing API.
package sessionstorebridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/docker/docker-agent/pkg/session"
)

const (
	Version      = "1"
	MaxBodyBytes = 64 << 20
)

type Config struct {
	Store  session.Store
	Token  string
	Target string
}

type Server struct {
	store  session.Store
	token  string
	target string
	mux    *http.ServeMux

	operationsMu sync.Mutex
	operations   map[string]*operationResult
	operationIDs []string
}

type operationResult struct {
	done   chan struct{}
	status int
	header http.Header
	body   []byte
}

type captureWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *captureWriter) Header() http.Header { return w.header }
func (w *captureWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *captureWriter) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(value)
}

type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type messageRequest struct {
	Message *session.Message `json:"message"`
}
type sessionRequest struct {
	Session  *session.Session `json:"session"`
	ParentID string           `json:"parentId,omitempty"`
}
type summaryRequest struct {
	Item session.Item `json:"item"`
}
type errorRequest struct {
	Error *session.Error `json:"error"`
}
type starredRequest struct {
	Starred bool `json:"starred"`
}
type usageRequest struct {
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	Cost         float64 `json:"cost"`
}
type titleRequest struct {
	Title string `json:"title"`
}
type messageIDResponse struct {
	MessageID int64 `json:"messageId"`
}
type healthResponse struct {
	Version string `json:"version"`
}

func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("session store bridge: store is required")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("session store bridge: token is required")
	}
	s := &Server{store: cfg.Store, token: cfg.Token, target: cfg.Target, mux: http.NewServeMux(), operations: make(map[string]*operationResult)}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+s.token {
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if r.Method == http.MethodGet {
		s.mux.ServeHTTP(w, r)
		return
	}
	s.serveMutation(w, r)
}

func (s *Server) serveMutation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.Header.Get("X-DAW-Operation-ID"))
	if id == "" || len(id) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_operation_id", "a valid operation ID is required")
		return
	}
	key := r.Method + " " + r.URL.Path + " " + id
	s.operationsMu.Lock()
	if existing := s.operations[key]; existing != nil {
		s.operationsMu.Unlock()
		select {
		case <-existing.done:
			replay(w, existing)
		case <-r.Context().Done():
			writeError(w, http.StatusRequestTimeout, "cancelled", "request cancelled")
		}
		return
	}
	result := &operationResult{done: make(chan struct{})}
	s.operations[key] = result
	s.operationIDs = append(s.operationIDs, key)
	s.operationsMu.Unlock()

	captured := &captureWriter{header: make(http.Header)}
	s.mux.ServeHTTP(captured, r)
	if captured.status == 0 {
		captured.status = http.StatusOK
	}
	s.operationsMu.Lock()
	result.status, result.header, result.body = captured.status, captured.header.Clone(), append([]byte(nil), captured.body.Bytes()...)
	close(result.done)
	s.pruneOperationsLocked()
	s.operationsMu.Unlock()
	replay(w, result)
}

func (s *Server) pruneOperationsLocked() {
	for len(s.operationIDs) > 4096 {
		key := s.operationIDs[0]
		result := s.operations[key]
		select {
		case <-result.done:
			delete(s.operations, key)
			s.operationIDs = s.operationIDs[1:]
		default:
			return
		}
	}
}

func replay(w http.ResponseWriter, result *operationResult) {
	for key, values := range result.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(result.status)
	_, _ = w.Write(result.body)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /v1/store/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{Version: Version})
	})
	s.mux.HandleFunc("POST /v1/store/sessions", s.addSession)
	s.mux.HandleFunc("GET /v1/store/sessions", s.getSessions)
	s.mux.HandleFunc("GET /v1/store/sessions/{id}", s.getSession)
	s.mux.HandleFunc("PUT /v1/store/sessions/{id}", s.updateSession)
	s.mux.HandleFunc("DELETE /v1/store/sessions/{id}", s.deleteSession)
	s.mux.HandleFunc("GET /v1/store/session-summaries", s.getSessionSummaries)
	s.mux.HandleFunc("PUT /v1/store/sessions/{id}/starred", s.setStarred)
	s.mux.HandleFunc("POST /v1/store/sessions/{id}/messages", s.addMessage)
	s.mux.HandleFunc("PUT /v1/store/messages/{messageID}", s.updateMessage)
	s.mux.HandleFunc("POST /v1/store/sessions/{id}/sub-sessions", s.addSubSession)
	s.mux.HandleFunc("POST /v1/store/sessions/{id}/summaries", s.addSummary)
	s.mux.HandleFunc("POST /v1/store/sessions/{id}/errors", s.addError)
	s.mux.HandleFunc("PUT /v1/store/sessions/{id}/usage", s.updateUsage)
	s.mux.HandleFunc("PUT /v1/store/sessions/{id}/title", s.updateTitle)
}

func (s *Server) prepareSession(sess *session.Session, parentID string) {
	if sess == nil {
		return
	}
	if parentID != "" {
		sess.ParentID = parentID
	}
	if s.target != "" {
		sess.SetAttribute("daw.execution.target", s.target)
	}
	for i := range sess.Messages {
		if child := sess.Messages[i].SubSession; child != nil {
			s.prepareSession(child, sess.ID)
		}
	}
}

func sessionResponse(sess *session.Session) sessionRequest {
	if sess == nil {
		return sessionRequest{}
	}
	return sessionRequest{Session: sess, ParentID: sess.ParentID}
}

func (s *Server) addSession(w http.ResponseWriter, r *http.Request) {
	var req sessionRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Session == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "session is required")
		return
	}
	s.prepareSession(req.Session, req.ParentID)
	writeStoreResult(w, s.store.AddSession(r.Context(), req.Session))
}
func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse(value))
}
func (s *Server) getSessions(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetSessions(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	response := make([]sessionRequest, len(value))
	for i := range value {
		response[i] = sessionResponse(value[i])
	}
	writeJSON(w, http.StatusOK, response)
}
func (s *Server) getSessionSummaries(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetSessionSummaries(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (s *Server) updateSession(w http.ResponseWriter, r *http.Request) {
	var req sessionRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Session == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "session is required")
		return
	}
	if req.Session.ID != r.PathValue("id") {
		writeError(w, http.StatusBadRequest, "invalid_request", "session ID does not match path")
		return
	}
	s.prepareSession(req.Session, req.ParentID)
	writeStoreResult(w, s.store.UpdateSession(r.Context(), req.Session))
}
func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	writeStoreResult(w, s.store.DeleteSession(r.Context(), r.PathValue("id")))
}
func (s *Server) setStarred(w http.ResponseWriter, r *http.Request) {
	var req starredRequest
	if !decode(w, r, &req) {
		return
	}
	writeStoreResult(w, s.store.SetSessionStarred(r.Context(), r.PathValue("id"), req.Starred))
}
func (s *Server) addMessage(w http.ResponseWriter, r *http.Request) {
	var req messageRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Message == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "message is required")
		return
	}
	id, err := s.store.AddMessage(r.Context(), r.PathValue("id"), req.Message)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, messageIDResponse{MessageID: id})
}
func (s *Server) updateMessage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("messageID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid message ID")
		return
	}
	var req messageRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Message == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "message is required")
		return
	}
	writeStoreResult(w, s.store.UpdateMessage(r.Context(), id, req.Message))
}
func (s *Server) addSubSession(w http.ResponseWriter, r *http.Request) {
	var req sessionRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Session == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "session is required")
		return
	}
	s.prepareSession(req.Session, r.PathValue("id"))
	writeStoreResult(w, s.store.AddSubSession(r.Context(), r.PathValue("id"), req.Session))
}
func (s *Server) addSummary(w http.ResponseWriter, r *http.Request) {
	var req summaryRequest
	if !decode(w, r, &req) {
		return
	}
	writeStoreResult(w, s.store.AddSummary(r.Context(), r.PathValue("id"), req.Item))
}
func (s *Server) addError(w http.ResponseWriter, r *http.Request) {
	var req errorRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Error == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "error is required")
		return
	}
	writeStoreResult(w, s.store.AddError(r.Context(), r.PathValue("id"), req.Error))
}
func (s *Server) updateUsage(w http.ResponseWriter, r *http.Request) {
	var req usageRequest
	if !decode(w, r, &req) {
		return
	}
	writeStoreResult(w, s.store.UpdateSessionTokens(r.Context(), r.PathValue("id"), req.InputTokens, req.OutputTokens, req.Cost))
}
func (s *Server) updateTitle(w http.ResponseWriter, r *http.Request) {
	var req titleRequest
	if !decode(w, r, &req) {
		return
	}
	writeStoreResult(w, s.store.UpdateSessionTitle(r.Context(), r.PathValue("id"), req.Title))
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request must contain one JSON value")
		return false
	}
	return true
}
func writeStoreResult(w http.ResponseWriter, err error) {
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "session data was not found")
	case errors.Is(err, session.ErrEmptyID):
		writeError(w, http.StatusBadRequest, "empty_id", "ID cannot be empty")
	default:
		writeError(w, http.StatusInternalServerError, "store_error", "session store operation failed")
	}
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, wireError{Code: code, Message: message})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
