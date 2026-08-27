// Package remote implements the dashboard adapter by forwarding operations to
// a daw-runner process inside a Docker Sandbox.
package remote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/protocol"
	"github.com/rumpl/daw/internal/runnerapi"
)

type Config struct {
	Endpoint       string
	Token          string
	CallbackOrigin string
	CallbackToken  string
	DialContext    func(context.Context, string, string) (net.Conn, error)
}

type Adapter struct {
	endpoint       string
	token          string
	callbackOrigin string
	callbackToken  string
	client         *http.Client
	transport      *http.Transport
}

func New(config Config) (*Adapter, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
		return nil, errors.New("remote adapter: runner endpoint must be an http URL")
	}
	if strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("remote adapter: runner token is required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if config.DialContext != nil {
		transport.DialContext = config.DialContext
	}
	return &Adapter{
		endpoint: endpoint, token: config.Token,
		callbackOrigin: strings.TrimRight(config.CallbackOrigin, "/"),
		callbackToken:  config.CallbackToken,
		client:         &http.Client{Transport: transport}, transport: transport,
	}, nil
}

func (a *Adapter) Info(ctx context.Context) (adapter.Info, error) {
	var value adapter.Info
	return value, a.do(ctx, http.MethodGet, "/v1/info", nil, &value)
}
func (a *Adapter) ListSessions(ctx context.Context, workingDir string) ([]protocol.SessionSummary, error) {
	var wire []runnerapi.SessionSummary
	path := "/v1/sessions?workingDir=" + url.QueryEscape(workingDir)
	if err := a.do(ctx, http.MethodGet, path, nil, &wire); err != nil {
		return nil, err
	}
	value := make([]protocol.SessionSummary, len(wire))
	for i := range wire {
		value[i] = wire[i].SessionSummary
		value[i].Attributes = wire[i].Attributes
	}
	return value, nil
}
func (a *Adapter) ReadSession(ctx context.Context, sessionID string) (adapter.StoredSession, error) {
	var value adapter.StoredSession
	return value, a.do(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(sessionID), nil, &value)
}
func (a *Adapter) ChatOptions(ctx context.Context, model string, servers []adapter.MCPServer) ([]protocol.ModelOption, []string, []protocol.ToolOption, error) {
	request := runnerapi.ChatOptionsRequest{Model: model, MCPServers: a.prepareServers(servers)}
	var value runnerapi.ChatOptionsResponse
	err := a.do(ctx, http.MethodPost, "/v1/options", request, &value)
	return value.Models, value.ThinkingLevels, value.Tools, err
}
func (a *Adapter) OpenChat(ctx context.Context, request adapter.OpenRequest) (adapter.Chat, error) {
	request.MCPServers = a.prepareServers(request.MCPServers)
	var response runnerapi.OpenResponse
	if err := a.do(ctx, http.MethodPost, "/v1/chats", request, &response); err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	response.Meta.Attributes = response.MetaAttributes
	chat := &chat{
		a: a, id: response.ChatID, sessionID: response.SessionID, meta: response.Meta,
		events: make(chan protocol.Event, 512), cancel: cancel,
	}
	go chat.stream(streamCtx)
	return chat, nil
}
func (a *Adapter) Check(ctx context.Context) error {
	return a.do(ctx, http.MethodGet, "/v1/health", nil, nil)
}

// ModelsGateway reads the gateway from the sandbox's own docker-agent, which
// keeps its user configuration separate from the host's.
func (a *Adapter) ModelsGateway(ctx context.Context) (string, error) {
	var value protocol.ModelsGatewayConfig
	if err := a.do(ctx, http.MethodGet, "/v1/settings/models-gateway", nil, &value); err != nil {
		return "", err
	}
	return value.URL, nil
}

// SetModelsGateway mirrors the host's gateway into the sandbox so the runner's
// docker-agent resolves models through the same gateway as the host.
func (a *Adapter) SetModelsGateway(ctx context.Context, gatewayURL string) error {
	return a.do(ctx, http.MethodPut, "/v1/settings/models-gateway", protocol.UpdateModelsGatewayRequest{URL: gatewayURL}, nil)
}

func (a *Adapter) Close() error { a.transport.CloseIdleConnections(); return nil }

func (a *Adapter) prepareServers(servers []adapter.MCPServer) []adapter.MCPServer {
	prepared := make([]adapter.MCPServer, len(servers))
	copy(prepared, servers)
	for i := range prepared {
		prepared[i].Env = append([]string(nil), prepared[i].Env...)
		if a.callbackOrigin == "" || prepared[i].Command == "" {
			continue
		}
		env := prepared[i].Env[:0]
		for _, entry := range prepared[i].Env {
			name, _, _ := strings.Cut(entry, "=")
			if name != "DAW_API_ORIGIN" && name != "DAW_API_SOCKET" && name != "DAW_API_TOKEN" && name != "DAW_PLUGIN_TOKEN" {
				env = append(env, entry)
			}
		}
		prepared[i].Env = append(env,
			"DAW_API_ORIGIN="+a.callbackOrigin,
			"DAW_API_TOKEN="+a.callbackToken,
			"DAW_PLUGIN_TOKEN="+a.callbackToken,
		)
	}
	return prepared
}

func (a *Adapter) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, a.endpoint+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+a.token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("runner request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return statusError(response.StatusCode, strings.TrimSpace(string(message)))
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return fmt.Errorf("decode runner response: %w", err)
	}
	return nil
}

func statusError(status int, message string) error {
	var sentinel error
	switch status {
	case http.StatusNotFound:
		sentinel = adapter.ErrNotFound
	case http.StatusConflict:
		sentinel = adapter.ErrBusy
	case http.StatusNotImplemented:
		sentinel = adapter.ErrUnsupported
	case http.StatusFailedDependency:
		sentinel = adapter.ErrNoModel
	case http.StatusGone:
		sentinel = adapter.ErrClosed
	default:
		sentinel = errors.New("runner request failed")
	}
	if message == "" {
		return sentinel
	}
	return fmt.Errorf("%w: %s", sentinel, message)
}

type chat struct {
	a         *Adapter
	id        string
	sessionID string
	events    chan protocol.Event
	cancel    context.CancelFunc
	closeOnce sync.Once
	mu        sync.Mutex
	meta      protocol.SessionMeta
}

func (c *chat) SessionID() string          { return c.sessionID }
func (c *chat) Meta() protocol.SessionMeta { c.mu.Lock(); defer c.mu.Unlock(); return c.meta }
func (c *chat) Snapshot(ctx context.Context) ([]protocol.Item, protocol.Usage, error) {
	var value runnerapi.SnapshotResponse
	err := c.a.do(ctx, http.MethodGet, c.path("snapshot"), nil, &value)
	return value.Items, value.Usage, err
}
func (c *chat) Events() <-chan protocol.Event { return c.events }
func (c *chat) Send(ctx context.Context, text string, attachments []adapter.Attachment, mode protocol.DeliveryMode) (protocol.DeliveryMode, string, bool, error) {
	var value runnerapi.SendResponse
	err := c.a.do(ctx, http.MethodPost, c.path("send"), runnerapi.SendRequest{Text: text, Attachments: attachments, Mode: mode}, &value)
	return value.Mode, value.RunID, value.Queued, err
}
func (c *chat) Abort() {
	_ = c.a.do(context.Background(), http.MethodPost, c.path("abort"), map[string]any{}, nil)
}
func (c *chat) Confirm(ctx context.Context, id string, decision protocol.ToolDecision, reason string) error {
	return c.a.do(ctx, http.MethodPost, c.path("confirm"), runnerapi.ConfirmRequest{ToolCallID: id, Decision: decision, Reason: reason}, nil)
}
func (c *chat) Elicit(ctx context.Context, id string, action protocol.ElicitationAction, content map[string]any) error {
	return c.a.do(ctx, http.MethodPost, c.path("elicit"), runnerapi.ElicitRequest{ElicitationID: id, Action: action, Content: content}, nil)
}
func (c *chat) Models(ctx context.Context) []protocol.ModelOption {
	var v []protocol.ModelOption
	_ = c.a.do(ctx, http.MethodGet, c.path("models"), nil, &v)
	return v
}
func (c *chat) Commands(ctx context.Context) []protocol.CommandInfo {
	var v []protocol.CommandInfo
	_ = c.a.do(ctx, http.MethodGet, c.path("commands"), nil, &v)
	return v
}
func (c *chat) SetModel(ctx context.Context, value string) error { return c.value(ctx, "model", value) }
func (c *chat) SetThinking(ctx context.Context, value string) error {
	return c.value(ctx, "thinking", value)
}
func (c *chat) Retitle(ctx context.Context, value string) error {
	return c.value(ctx, "retitle", value)
}
func (c *chat) value(ctx context.Context, action, value string) error {
	return c.a.do(ctx, http.MethodPost, c.path(action), runnerapi.ValueRequest{Value: value}, nil)
}
func (c *chat) SetDisabledTools(names []string) {
	_ = c.a.do(context.Background(), http.MethodPost, c.path("tools"), runnerapi.ToolsRequest{Names: names}, nil)
}
func (c *chat) Compact(ctx context.Context) error {
	return c.a.do(ctx, http.MethodPost, c.path("compact"), map[string]any{}, nil)
}
func (c *chat) Stats(ctx context.Context) protocol.Stats {
	var v protocol.Stats
	_ = c.a.do(ctx, http.MethodGet, c.path("stats"), nil, &v)
	return v
}
func (c *chat) Close(ctx context.Context) error {
	var err error
	c.closeOnce.Do(func() { err = c.a.do(ctx, http.MethodDelete, "/v1/chats/"+url.PathEscape(c.id), nil, nil); c.cancel() })
	return err
}
func (c *chat) path(action string) string { return "/v1/chats/" + url.PathEscape(c.id) + "/" + action }

func (c *chat) stream(ctx context.Context) {
	defer close(c.events)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.a.endpoint+c.path("events"), nil)
	if err != nil {
		return
	}
	request.Header.Set("Authorization", "Bearer "+c.a.token)
	response, err := c.a.client.Do(request)
	if err != nil {
		c.streamNotice(err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		c.streamNotice(fmt.Errorf("runner event stream returned %s", response.Status))
		return
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		var wire runnerapi.StreamEvent
		if json.Unmarshal(scanner.Bytes(), &wire) != nil {
			continue
		}
		event := wire.Event
		if event.Meta != nil {
			event.Meta.Attributes = wire.MetaAttributes
		}
		if event.Type == protocol.EventSessionMeta && event.Meta != nil {
			c.mu.Lock()
			c.meta = *event.Meta
			c.mu.Unlock()
		}
		select {
		case c.events <- event:
		case <-ctx.Done():
			return
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		c.streamNotice(err)
	}
}
func (c *chat) streamNotice(err error) {
	select {
	case c.events <- protocol.Event{Type: protocol.EventNotice, Notice: &protocol.Notice{ID: "runner-stream", Level: protocol.NoticeError, Code: "runner_stream_lost", Message: "The sandbox runner event stream was lost: " + err.Error()}}:
	default:
	}
}

var _ adapter.Adapter = (*Adapter)(nil)
var _ adapter.Chat = (*chat)(nil)
