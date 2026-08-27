// Package sessionstoreremote implements docker-agent's session.Store over the
// host-only session store bridge.
package sessionstoreremote

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/docker-agent/pkg/session"
)

const (
	Version          = "1"
	maxResponseBytes = 64 << 20
)

var ErrClosed = errors.New("remote session store is closed")

type Config struct {
	URL         string
	Token       string
	Timeout     time.Duration
	DialContext func(context.Context, string, string) (net.Conn, error)
}

type RemoteStore struct {
	base      *url.URL
	token     string
	client    *http.Client
	transport *http.Transport
	mutations sync.Mutex
	closed    atomic.Bool
}

type sessionRequest struct {
	Session  *session.Session `json:"session"`
	ParentID string           `json:"parentId,omitempty"`
}
type messageRequest struct {
	Message *session.Message `json:"message"`
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
type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func New(cfg Config) (*RemoteStore, error) {
	base, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("remote session store: invalid URL %q", cfg.URL)
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("remote session store: token is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if cfg.DialContext != nil {
		transport.DialContext = cfg.DialContext
	}
	return &RemoteStore{base: base, token: cfg.Token, transport: transport, client: &http.Client{Transport: transport, Timeout: cfg.Timeout}}, nil
}

// Check verifies authentication and protocol compatibility before the runner
// begins accepting chat requests.
func (s *RemoteStore) Check(ctx context.Context) error {
	var health healthResponse
	if err := s.query(ctx, http.MethodGet, "/v1/store/health", nil, &health); err != nil {
		return fmt.Errorf("session store handshake: %w", err)
	}
	if health.Version != Version {
		return fmt.Errorf("session store protocol mismatch: host=%q runner=%q", health.Version, Version)
	}
	return nil
}

func (s *RemoteStore) AddSession(ctx context.Context, value *session.Session) error {
	return s.mutate(ctx, http.MethodPost, "/v1/store/sessions", sessionRequest{Session: value, ParentID: value.ParentID}, nil)
}
func (s *RemoteStore) GetSession(ctx context.Context, id string) (*session.Session, error) {
	var value sessionRequest
	if err := s.query(ctx, http.MethodGet, "/v1/store/sessions/"+url.PathEscape(id), nil, &value); err != nil {
		return nil, err
	}
	if value.Session == nil {
		return nil, errors.New("session store returned no session")
	}
	restoreParentLinks(value.Session, value.ParentID)
	return value.Session, nil
}
func (s *RemoteStore) GetSessionByOrigin(ctx context.Context, id, origin string) (*session.Session, error) {
	value, err := s.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if value.Origin != origin {
		return nil, session.ErrNotFound
	}
	return value, nil
}
func (s *RemoteStore) GetSessions(ctx context.Context) ([]*session.Session, error) {
	var wire []sessionRequest
	if err := s.query(ctx, http.MethodGet, "/v1/store/sessions", nil, &wire); err != nil {
		return nil, err
	}
	value := make([]*session.Session, len(wire))
	for i := range wire {
		value[i] = wire[i].Session
		restoreParentLinks(value[i], wire[i].ParentID)
	}
	return value, nil
}
func (s *RemoteStore) GetSessionSummaries(ctx context.Context) ([]session.Summary, error) {
	var value []session.Summary
	if err := s.query(ctx, http.MethodGet, "/v1/store/session-summaries", nil, &value); err != nil {
		return nil, err
	}
	return value, nil
}
func (s *RemoteStore) DeleteSession(ctx context.Context, id string) error {
	return s.mutate(ctx, http.MethodDelete, "/v1/store/sessions/"+url.PathEscape(id), nil, nil)
}
func (s *RemoteStore) UpdateSession(ctx context.Context, value *session.Session) error {
	return s.mutate(ctx, http.MethodPut, "/v1/store/sessions/"+url.PathEscape(value.ID), sessionRequest{Session: value, ParentID: value.ParentID}, nil)
}
func (s *RemoteStore) SetSessionStarred(ctx context.Context, id string, starred bool) error {
	return s.mutate(ctx, http.MethodPut, "/v1/store/sessions/"+url.PathEscape(id)+"/starred", starredRequest{starred}, nil)
}
func (s *RemoteStore) AddMessage(ctx context.Context, id string, value *session.Message) (int64, error) {
	var result messageIDResponse
	err := s.mutate(ctx, http.MethodPost, "/v1/store/sessions/"+url.PathEscape(id)+"/messages", messageRequest{value}, &result)
	return result.MessageID, err
}
func (s *RemoteStore) UpdateMessage(ctx context.Context, id int64, value *session.Message) error {
	return s.mutate(ctx, http.MethodPut, "/v1/store/messages/"+strconv.FormatInt(id, 10), messageRequest{value}, nil)
}
func (s *RemoteStore) AddSubSession(ctx context.Context, id string, value *session.Session) error {
	return s.mutate(ctx, http.MethodPost, "/v1/store/sessions/"+url.PathEscape(id)+"/sub-sessions", sessionRequest{Session: value, ParentID: id}, nil)
}
func (s *RemoteStore) AddSummary(ctx context.Context, id string, value session.Item) error {
	return s.mutate(ctx, http.MethodPost, "/v1/store/sessions/"+url.PathEscape(id)+"/summaries", summaryRequest{value}, nil)
}
func (s *RemoteStore) AddError(ctx context.Context, id string, value *session.Error) error {
	return s.mutate(ctx, http.MethodPost, "/v1/store/sessions/"+url.PathEscape(id)+"/errors", errorRequest{value}, nil)
}
func (s *RemoteStore) UpdateSessionTokens(ctx context.Context, id string, input, output int64, cost float64) error {
	return s.mutate(ctx, http.MethodPut, "/v1/store/sessions/"+url.PathEscape(id)+"/usage", usageRequest{input, output, cost}, nil)
}
func (s *RemoteStore) UpdateSessionTitle(ctx context.Context, id, title string) error {
	return s.mutate(ctx, http.MethodPut, "/v1/store/sessions/"+url.PathEscape(id)+"/title", titleRequest{title}, nil)
}

func restoreParentLinks(value *session.Session, parentID string) {
	if value == nil {
		return
	}
	if parentID != "" {
		value.ParentID = parentID
	}
	for i := range value.Messages {
		if child := value.Messages[i].SubSession; child != nil {
			restoreParentLinks(child, value.ID)
		}
	}
}

func (s *RemoteStore) query(ctx context.Context, method, path string, request, response any) error {
	s.mutations.Lock()
	defer s.mutations.Unlock()
	return s.do(ctx, method, path, request, response, "")
}

func (s *RemoteStore) mutate(ctx context.Context, method, path string, request, response any) error {
	s.mutations.Lock()
	defer s.mutations.Unlock()
	operationID, err := newOperationID()
	if err != nil {
		return fmt.Errorf("create store operation ID: %w", err)
	}
	return s.do(ctx, method, path, request, response, operationID)
}

func newOperationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (s *RemoteStore) do(ctx context.Context, method, path string, input, output any, operationID string) error {
	if s.closed.Load() {
		return ErrClosed
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode store request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	endpoint := *s.base
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	if operationID != "" {
		req.Header.Set("X-DAW-Operation-ID", operationID)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("session store request: %w", err)
	}
	defer res.Body.Close()
	limited := io.LimitReader(res.Body, maxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read store response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return errors.New("session store response exceeds size limit")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var remote wireError
		_ = json.Unmarshal(data, &remote)
		switch remote.Code {
		case "not_found":
			return session.ErrNotFound
		case "empty_id":
			return session.ErrEmptyID
		}
		if remote.Message == "" {
			remote.Message = http.StatusText(res.StatusCode)
		}
		return fmt.Errorf("session store %s: %s", remote.Code, remote.Message)
	}
	if output != nil {
		if len(data) == 0 {
			return errors.New("session store returned an empty response")
		}
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("decode store response: %w", err)
		}
	}
	return nil
}

func (s *RemoteStore) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		s.transport.CloseIdleConnections()
	}
	return nil
}

var _ session.Store = (*RemoteStore)(nil)
