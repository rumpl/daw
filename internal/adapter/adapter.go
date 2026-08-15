// Package adapter defines the narrow, typed seam between the HTTP server and
// docker-agent. The production implementation (internal/adapter/dagent) embeds
// the matched docker-agent Go SDK; internal/adapter/fake is a deterministic
// stand-in so every API, UI and e2e test runs without a model token or a
// Docker sandbox.
package adapter

import (
	"context"
	"errors"

	"github.com/rumpl/daw/internal/protocol"
)

// Errors returned across the seam. The HTTP layer maps these onto status codes
// and never leaks raw Go error strings to the browser.
var (
	ErrNotFound     = errors.New("not found")
	ErrBusy         = errors.New("chat is busy")
	ErrUnsupported  = errors.New("operation not supported by this runtime")
	ErrNoModel      = errors.New("no model could be resolved")
	ErrSessionInUse = errors.New("session is already open in this server")
	ErrInvalidAgent = errors.New("agent could not be loaded")
	ErrClosed       = errors.New("chat is closed")
)

// Info is the non-secret status shown in the bootstrap payload.
type Info struct {
	AgentVersion    string
	AgentCommit     string
	ConfigDir       string
	DataDir         string
	CacheDir        string
	SessionDB       string
	ModelsAvailable bool
	ModelsHint      string
	Notices         []protocol.Notice
}

type MCPServer struct {
	Name       string
	Command    string
	Args       []string
	Env        []string
	WorkingDir string
	URL        string
	Transport  string
	Headers    map[string]string
}

// OpenRequest opens (or resumes) exactly one live chat.
type OpenRequest struct {
	ChatID          string
	WorkingDir      string
	ResumeSessionID string
	// SessionAttributes are persisted by docker-agent on newly-created sessions.
	SessionAttributes map[string]string
	// PersistImmediately creates the session row during open instead of waiting
	// for the first prompt. Alternate execution locations require this so their
	// working directory remains discoverable after a restart.
	PersistImmediately bool
	// Model and ThinkingLevel are dashboard preferences restored from disk.
	// Adapters apply them, in that order, before publishing startup metadata.
	// Empty values leave the agent or resumed session's own configuration in
	// place. A stale or unsupported preference is ignored by the adapter.
	Model         string
	ThinkingLevel string
	// DisabledTools are restored into docker-agent's per-session exclusion
	// filter before the chat is exposed to the browser.
	DisabledTools []string
	MCPServers    []MCPServer
}

// Adapter is the process-wide docker-agent facade. It owns the single shared
// session store.
type Adapter interface {
	Info(ctx context.Context) (Info, error)
	// ListSessions returns sessions from docker-agent's own store. When
	// workingDir is non-empty the list is filtered to that directory.
	ListSessions(ctx context.Context, workingDir string) ([]protocol.SessionSummary, error)
	// ReadSession reads persisted session history without constructing a live
	// runtime, loading toolsets, or claiming the session in the chat registry.
	ReadSession(ctx context.Context, sessionID string) (StoredSession, error)
	// ChatOptions resolves the process-wide model catalog and the thinking
	// levels supported by model without creating a workspace chat or session.
	ChatOptions(ctx context.Context, model string) (models []protocol.ModelOption, thinkingLevels []string, err error)
	OpenChat(ctx context.Context, req OpenRequest) (Chat, error)
	Close() error
}

type StoredSession struct {
	Meta  protocol.StoredSessionMeta
	Items []protocol.Item
	Usage protocol.Usage
	Stats protocol.Stats
}

type Attachment struct {
	Name     string
	MimeType string
	Data     []byte
}

// Chat is one live runtime + session pair.
//
// Events returns the adapter's normalized stream. The channel is closed when
// the chat is disposed. Sequence numbers, buffering and fan-out belong to the
// HTTP layer, not here.
type Chat interface {
	SessionID() string
	Meta() protocol.SessionMeta
	// Snapshot rebuilds the timeline from docker-agent's own session data.
	// Used on open/resume and after a server restart.
	Snapshot(ctx context.Context) ([]protocol.Item, protocol.Usage, error)
	Events() <-chan protocol.Event

	Send(ctx context.Context, text string, attachments []Attachment, preferred protocol.DeliveryMode) (mode protocol.DeliveryMode, runID string, queued bool, err error)
	Abort()

	Confirm(ctx context.Context, toolCallID string, decision protocol.ToolDecision, reason string) error
	Elicit(ctx context.Context, elicitationID string, action protocol.ElicitationAction, content map[string]any) error

	Models(ctx context.Context) []protocol.ModelOption
	Commands(ctx context.Context) []protocol.CommandInfo
	Tools(ctx context.Context) ([]protocol.ToolOption, error)

	SetModel(ctx context.Context, ref string) error
	SetThinking(ctx context.Context, level string) error
	SetToolEnabled(ctx context.Context, name string, enabled bool) error

	Retitle(ctx context.Context, title string) error
	Compact(ctx context.Context) error
	Stats(ctx context.Context) protocol.Stats

	Close(ctx context.Context) error
}
