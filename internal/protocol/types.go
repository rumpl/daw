// Package protocol defines the browser-safe wire types shared by the Go
// server and the TypeScript frontend.
//
// Everything here is a plain data type: no docker-agent types leak across
// this boundary, which is what lets the UI and API tests run entirely on the
// fake adapter. The TypeScript mirror in web/src/protocol.gen.ts is generated
// from these declarations (see tsgen.go) and a Go test fails if it drifts.
package protocol

// ---------------------------------------------------------------------------
// Enumerations (string constants; mirrored as TS string-literal unions)
// ---------------------------------------------------------------------------

// RunState is the lifecycle state of a chat's current turn.
type RunState string

const (
	RunStateIdle     RunState = "idle"
	RunStateRunning  RunState = "running"
	RunStateStopping RunState = "stopping"
)

// DeliveryMode selects how a submitted message reaches the runtime.
type DeliveryMode string

const (
	// DeliveryNormal starts a new run; only valid while idle.
	DeliveryNormal DeliveryMode = "normal"
	// DeliverySteer injects the message into the running turn at the next
	// safe point (runtime.Steer).
	DeliverySteer DeliveryMode = "steer"
	// DeliveryFollowUp queues the message for its own turn once the current
	// one finishes (runtime.FollowUp).
	DeliveryFollowUp DeliveryMode = "followUp"
)

// ToolState is the lifecycle of one tool call as shown in the UI.
type ToolState string

const (
	ToolStatePending  ToolState = "pending"
	ToolStateAwaiting ToolState = "awaiting_confirmation"
	ToolStateRunning  ToolState = "running"
	ToolStateSuccess  ToolState = "success"
	ToolStateError    ToolState = "error"
	ToolStateRejected ToolState = "rejected"
)

// NoticeLevel classifies a non-conversational message shown inline.
type NoticeLevel string

const (
	NoticeInfo    NoticeLevel = "info"
	NoticeWarning NoticeLevel = "warning"
	NoticeError   NoticeLevel = "error"
)

// ToolDecision mirrors pkg/tui/components/toolconfirm.Decision.
type ToolDecision string

const (
	// DecisionApprove approves this one call (runtime.ResumeApprove).
	DecisionApprove ToolDecision = "approve"
	// DecisionApproveAlways grants the exact pattern shown in the dialog,
	// built by toolconfirm.BuildPermissionPattern.
	DecisionApproveAlways ToolDecision = "approveAlways"
	// DecisionReject rejects with an optional reason (runtime.ResumeReject).
	DecisionReject ToolDecision = "reject"
)

// ElicitationAction mirrors tools.ElicitationAction.
type ElicitationAction string

const (
	ElicitAccept  ElicitationAction = "accept"
	ElicitDecline ElicitationAction = "decline"
	ElicitCancel  ElicitationAction = "cancel"
)

// ---------------------------------------------------------------------------
// Timeline items
// ---------------------------------------------------------------------------

// ItemKind discriminates Item.
type ItemKind string

const (
	ItemKindMessage  ItemKind = "message"
	ItemKindTool     ItemKind = "tool"
	ItemKindTransfer ItemKind = "transfer"
	ItemKindNotice   ItemKind = "notice"
	ItemKindSummary  ItemKind = "summary"
)

// MessageItem is a user, assistant or system message.
type MessageItem struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	AgentName string `json:"agentName"`
	Text      string `json:"text"`
	Reasoning string `json:"reasoning"`
	Streaming bool   `json:"streaming"`
	CreatedAt string `json:"createdAt"`
	Model     string `json:"model"`
}

// ToolImage is a base64-encoded image attachment returned by a tool.
type ToolImage struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// ToolActivity is one tool call with a bounded preview of its result.
type ToolActivity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Category    string `json:"category"`
	AgentName   string `json:"agentName"`
	ArgsSummary string `json:"argsSummary"`
	// Arguments contains a bounded, presentation-safe subset of the call's
	// structured arguments. Large file contents are represented by counts and
	// short previews rather than copied wholesale into the browser timeline.
	Arguments   map[string]any `json:"arguments,omitempty"`
	Images      []ToolImage    `json:"images,omitempty"`
	State       ToolState      `json:"state"`
	Preview     string         `json:"preview"`
	Truncated   bool           `json:"truncated"`
	OutputBytes int            `json:"outputBytes"`
	IsError     bool           `json:"isError"`
}

// Transfer records a sub-agent delegation.
type Transfer struct {
	ID        string `json:"id"`
	FromAgent string `json:"fromAgent"`
	ToAgent   string `json:"toAgent"`
	Switching bool   `json:"switching"`
}

// Notice is an inline, non-conversational message (warnings, retries,
// compaction, load failures, run errors).
type Notice struct {
	ID      string      `json:"id"`
	Level   NoticeLevel `json:"level"`
	Message string      `json:"message"`
	Code    string      `json:"code"`
}

// Summary is a compaction summary entry.
type Summary struct {
	ID   string  `json:"id"`
	Text string  `json:"text"`
	Cost float64 `json:"cost"`
}

// Item is one entry in the conversation timeline.
type Item struct {
	Kind     ItemKind      `json:"kind"`
	Message  *MessageItem  `json:"message,omitempty"`
	Tool     *ToolActivity `json:"tool,omitempty"`
	Transfer *Transfer     `json:"transfer,omitempty"`
	Notice   *Notice       `json:"notice,omitempty"`
	Summary  *Summary      `json:"summary,omitempty"`
}

// ---------------------------------------------------------------------------
// Interactive requests
// ---------------------------------------------------------------------------

// RejectionReason is one preset from toolconfirm.RejectionReasons().
type RejectionReason struct {
	Label  string `json:"label"`
	Reason string `json:"reason"`
}

// ToolConfirmationRequest is a blocking tool-approval prompt. Pattern is the
// exact string that will be granted if the user picks "always allow" — it is
// produced by toolconfirm.BuildPermissionPattern and never rebuilt client-side.
type ToolConfirmationRequest struct {
	ToolCallID       string            `json:"toolCallId"`
	ToolName         string            `json:"toolName"`
	DisplayName      string            `json:"displayName,omitempty"`
	AgentName        string            `json:"agentName"`
	ArgsSummary      string            `json:"argsSummary"`
	Pattern          string            `json:"pattern"`
	PatternLabel     string            `json:"patternLabel"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	RejectionReasons []RejectionReason `json:"rejectionReasons"`
}

// ElicitationRequest is an MCP elicitation. Replies are correlated by
// ElicitationID, never by position.
type ElicitationRequest struct {
	ElicitationID string `json:"elicitationId"`
	Message       string `json:"message"`
	Mode          string `json:"mode"`
	URL           string `json:"url"`
	AgentName     string `json:"agentName"`
	Schema        any    `json:"schema,omitempty"`
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// QueueStatus mirrors runtime.QueueStatus.
type QueueStatus struct {
	SteerDepth       int `json:"steerDepth"`
	SteerCapacity    int `json:"steerCapacity"`
	FollowUpDepth    int `json:"followUpDepth"`
	FollowUpCapacity int `json:"followUpCapacity"`
}

// RunStatus is the current turn state plus queue depths.
type RunStatus struct {
	State RunState    `json:"state"`
	RunID string      `json:"runId"`
	Queue QueueStatus `json:"queue"`
}

// Usage is cumulative token/cost accounting for a session.
type Usage struct {
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	Cost         float64 `json:"cost"`
	ContextLimit int64   `json:"contextLimit"`
}

// PermissionsView reports the pattern sets the autonomous runtime evaluates.
type PermissionsView struct {
	Allow         []string `json:"allow"`
	Ask           []string `json:"ask"`
	Deny          []string `json:"deny"`
	AgentsIgnore  bool     `json:"agentsIgnore"`
	SessionGrants []string `json:"sessionGrants"`
}

// SessionMeta is per-chat metadata shown in the header and sidebar.
type SessionMeta struct {
	ChatID         string          `json:"chatId"`
	SessionID      string          `json:"sessionId"`
	Title          string          `json:"title"`
	WorkspaceID    string          `json:"workspaceId"`
	WorkingDir     string          `json:"workingDir"`
	AgentName      string          `json:"agentName"`
	Model          string          `json:"model"`
	ThinkingLevel  string          `json:"thinkingLevel"`
	ThinkingLevels []string        `json:"thinkingLevels"`
	Permissions    PermissionsView `json:"permissions"`
	CreatedAt      string          `json:"createdAt"`
}

// Snapshot is the complete, authoritative state of a chat.
type Snapshot struct {
	Seq                  uint64                    `json:"seq"`
	Meta                 SessionMeta               `json:"meta"`
	Items                []Item                    `json:"items"`
	Run                  RunStatus                 `json:"run"`
	Usage                Usage                     `json:"usage"`
	PendingConfirmations []ToolConfirmationRequest `json:"pendingConfirmations"`
	PendingElicitations  []ElicitationRequest      `json:"pendingElicitations"`
}

// ---------------------------------------------------------------------------
// SSE event envelope (discriminated union on Type)
// ---------------------------------------------------------------------------

// EventType discriminates Event.
type EventType string

const (
	EventSnapshot         EventType = "snapshot"
	EventRunStatus        EventType = "run_status"
	EventMessageItem      EventType = "message_item"
	EventAssistantDelta   EventType = "assistant_delta"
	EventAssistantEnd     EventType = "assistant_end"
	EventReasoningDelta   EventType = "reasoning_delta"
	EventReasoningEnd     EventType = "reasoning_end"
	EventToolStart        EventType = "tool_start"
	EventToolUpdate       EventType = "tool_update"
	EventToolEnd          EventType = "tool_end"
	EventToolConfirmation EventType = "tool_confirmation"
	EventToolResolved     EventType = "tool_confirmation_resolved"
	EventElicitation      EventType = "elicitation"
	EventElicitResolved   EventType = "elicitation_resolved"
	EventTransfer         EventType = "transfer"
	EventUsage            EventType = "usage"
	EventNotice           EventType = "notice"
	EventSessionMeta      EventType = "session_meta"
	EventGap              EventType = "gap"
	EventChatClosed       EventType = "chat_closed"
)

// Delta carries streamed assistant or reasoning text for one message item.
type Delta struct {
	ItemID string `json:"itemId"`
	Text   string `json:"text"`
}

// ItemRef names a single item.
type ItemRef struct {
	ItemID string `json:"itemId"`
}

// ToolResolved reports the decision applied to a confirmation request.
type ToolResolved struct {
	ToolCallID string       `json:"toolCallId"`
	Decision   ToolDecision `json:"decision"`
	Pattern    string       `json:"pattern"`
}

// ElicitResolved reports that an elicitation was answered.
type ElicitResolved struct {
	ElicitationID string `json:"elicitationId"`
}

// ChatClosed is the terminal event for a disposed chat.
type ChatClosed struct {
	Reason string `json:"reason"`
}

// Event is one normalized SSE payload. Exactly one payload field is set,
// selected by Type.
type Event struct {
	Type EventType `json:"type"`
	Seq  uint64    `json:"seq"`

	Snapshot       *Snapshot                `json:"snapshot,omitempty"`
	Run            *RunStatus               `json:"run,omitempty"`
	Message        *MessageItem             `json:"message,omitempty"`
	Delta          *Delta                   `json:"delta,omitempty"`
	Ref            *ItemRef                 `json:"ref,omitempty"`
	Tool           *ToolActivity            `json:"tool,omitempty"`
	Confirmation   *ToolConfirmationRequest `json:"confirmation,omitempty"`
	ToolResolved   *ToolResolved            `json:"toolResolved,omitempty"`
	Elicitation    *ElicitationRequest      `json:"elicitation,omitempty"`
	ElicitResolved *ElicitResolved          `json:"elicitResolved,omitempty"`
	Transfer       *Transfer                `json:"transfer,omitempty"`
	Usage          *Usage                   `json:"usage,omitempty"`
	Notice         *Notice                  `json:"notice,omitempty"`
	Meta           *SessionMeta             `json:"meta,omitempty"`
	Closed         *ChatClosed              `json:"closed,omitempty"`
}

// ---------------------------------------------------------------------------
// Dashboard-wide SSE events
// ---------------------------------------------------------------------------

// DashboardEventType discriminates low-volume resource invalidations.
type DashboardEventType string

const (
	DashboardEventSnapshot        DashboardEventType = "snapshot"
	DashboardEventSessionsChanged DashboardEventType = "sessions_changed"
	DashboardEventPluginsChanged  DashboardEventType = "plugins_changed"
	DashboardEventGap             DashboardEventType = "gap"
)

// DashboardEvent notifies clients that an authoritative REST resource changed.
type DashboardEvent struct {
	Type         DashboardEventType `json:"type"`
	Seq          uint64             `json:"seq"`
	WorkspaceIDs []string           `json:"workspaceIds,omitempty"`
	SessionIDs   []string           `json:"sessionIds,omitempty"`
	Reason       string             `json:"reason,omitempty"`
	Revision     string             `json:"revision,omitempty"`
}

// ---------------------------------------------------------------------------
// REST payloads
// ---------------------------------------------------------------------------

// Health is GET /api/health.
type Health struct {
	Status string `json:"status"`
	Uptime int64  `json:"uptimeSeconds"`
}

// WorkspaceHint is a previously-validated workspace offered in bootstrap.
type WorkspaceHint struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

// Bootstrap is GET /api/bootstrap: non-secret app and docker-agent status.
type Bootstrap struct {
	AppVersion      string          `json:"appVersion"`
	AgentVersion    string          `json:"agentVersion"`
	AgentCommit     string          `json:"agentCommit"`
	ConfigDir       string          `json:"configDir"`
	DataDir         string          `json:"dataDir"`
	CacheDir        string          `json:"cacheDir"`
	SessionDB       string          `json:"sessionDb"`
	PluginDir       string          `json:"pluginDir"`
	CSRFToken       string          `json:"csrfToken"`
	Sandboxed       bool            `json:"sandboxed"`
	ModelsAvailable bool            `json:"modelsAvailable"`
	ModelsHint      string          `json:"modelsHint"`
	WorkspaceHints  []WorkspaceHint `json:"workspaceHints"`
	Notices         []Notice        `json:"notices"`
}

// PluginPage is one route contributed by a global dashboard plugin.
type PluginPage struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Label   string `json:"label"`
	Sidebar bool   `json:"sidebar"`
}

// Plugin describes one valid global plugin. EntryURL and StyleURL are
// fingerprinted, same-origin assets that can be loaded directly by the browser.
type Plugin struct {
	APIVersion  int          `json:"apiVersion"`
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Version     string       `json:"version"`
	Fingerprint string       `json:"fingerprint"`
	EntryURL    string       `json:"entryUrl"`
	StyleURL    string       `json:"styleUrl,omitempty"`
	Pages       []PluginPage `json:"pages"`
}

// PluginError is a bounded discovery diagnostic for one invalid plugin.
type PluginError struct {
	PluginID string `json:"pluginId,omitempty"`
	Message  string `json:"message"`
}

// PluginCatalog is GET /api/plugins.
type PluginCatalog struct {
	Plugins []Plugin      `json:"plugins"`
	Errors  []PluginError `json:"errors"`
}

// OpenWorkspaceRequest is POST /api/workspaces/open.
type OpenWorkspaceRequest struct {
	Path string `json:"path"`
}

// Workspace is an opaque, server-resolved working directory.
type Workspace struct {
	WorkspaceID  string   `json:"workspaceId"`
	Path         string   `json:"path"`
	Label        string   `json:"label"`
	Notices      []Notice `json:"notices"`
	AgentsMD     bool     `json:"agentsMd"`
	AgentsIgnore bool     `json:"agentsIgnore"`
}

// SessionSummary is one row of the session list.
type SessionSummary struct {
	SessionID  string    `json:"sessionId"`
	Title      string    `json:"title"`
	WorkingDir string    `json:"workingDir"`
	CreatedAt  string    `json:"createdAt"`
	Messages   int       `json:"messages"`
	Live       bool      `json:"live"`
	ChatID     string    `json:"chatId,omitempty"`
	RunState   *RunState `json:"runState,omitempty"`
}

// CreateChatRequest is POST /api/chats. Every chat uses the dashboard's
// single SDK-built coding agent.
type CreateChatRequest struct {
	WorkspaceID string `json:"workspaceId"`
}

// ResumeChatRequest is POST /api/chats/resume.
type ResumeChatRequest struct {
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
}

// ChatRef identifies a chat.
type ChatRef struct {
	ChatID    string `json:"chatId"`
	SessionID string `json:"sessionId"`
}

// SendMessageRequest is POST /api/chats/:id/messages.
type SendMessageRequest struct {
	Text           string       `json:"text"`
	Mode           DeliveryMode `json:"mode"`
	IdempotencyKey string       `json:"idempotencyKey"`
}

// Accepted is the 202 body for accepted prompts.
type Accepted struct {
	Accepted bool         `json:"accepted"`
	Mode     DeliveryMode `json:"mode"`
	RunID    string       `json:"runId"`
	Queued   bool         `json:"queued"`
}

// ModelOption is one selectable model, sourced from the runtime only. The
// fields mirror runtime.ModelChoice; costs are USD per 1M tokens.
type ModelOption struct {
	Name         string  `json:"name"`
	Ref          string  `json:"ref"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	Family       string  `json:"family"`
	ContextLimit int     `json:"contextLimit"`
	InputCost    float64 `json:"inputCost"`
	OutputCost   float64 `json:"outputCost"`
	IsCurrent    bool    `json:"isCurrent"`
	IsDefault    bool    `json:"isDefault"`
	// IsCustom marks a model used earlier in this session rather than one
	// declared in config. IsCatalog marks a models.dev catalog entry.
	IsCustom  bool `json:"isCustom"`
	IsCatalog bool `json:"isCatalog"`
}

// UpdateConfigRequest is PATCH /api/chats/:id/config. Nil fields are unchanged.
type UpdateConfigRequest struct {
	Model         *string `json:"model,omitempty"`
	ThinkingLevel *string `json:"thinkingLevel,omitempty"`
}

// ToolConfirmationReply is POST /api/chats/:id/tool-confirmation.
type ToolConfirmationReply struct {
	ToolCallID string       `json:"toolCallId"`
	Decision   ToolDecision `json:"decision"`
	Reason     string       `json:"reason"`
}

// ElicitationReply is POST /api/chats/:id/elicitation.
type ElicitationReply struct {
	ElicitationID string            `json:"elicitationId"`
	Action        ElicitationAction `json:"action"`
	Content       map[string]any    `json:"content,omitempty"`
}

// RetitleRequest is POST /api/chats/:id/retitle.
type RetitleRequest struct {
	Title string `json:"title"`
}

// Stats is GET /api/chats/:id/stats.
type Stats struct {
	Usage       Usage  `json:"usage"`
	Messages    int    `json:"messages"`
	ToolCalls   int    `json:"toolCalls"`
	Model       string `json:"model"`
	AgentName   string `json:"agentName"`
	DurationSec int64  `json:"durationSeconds"`
}

// CommandInfo is one discovered slash command / skill / prompt file.
type CommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
}

// APIError is the single JSON error shape for every failure.
type APIError struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Details string `json:"details,omitempty"`
}
