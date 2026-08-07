// Package fake is a deterministic docker-agent adapter used by every default
// test (Go, Vitest fixtures and Playwright). It never contacts a model
// provider or starts a sandbox.
//
// It replays a scripted turn whose shape mirrors the real runtime's event
// order: stream start -> reasoning -> assistant deltas -> tool call ->
// (optional confirmation / elicitation) -> tool result -> usage -> settle.
package fake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/protocol"
)

// Adapter is the fake docker-agent facade.
type Adapter struct {
	mu       sync.Mutex
	sessions map[string]*storedSession
	seq      int
	// Sessions opened live, so the manager's ownership rules can be tested.
	Now func() time.Time
	// Delay, when set, slows the scripted turn (used by e2e to exercise the
	// Stop button). Default 0 keeps unit tests instant.
	Delay time.Duration
}

type storedSession struct {
	id         string
	title      string
	workingDir string
	createdAt  time.Time
	items      []protocol.Item
	usage      protocol.Usage
	agentName  string
	model      string
	thinking   string
	grants     []string
}

// New builds an empty fake adapter.
func New() *Adapter {
	return &Adapter{sessions: map[string]*storedSession{}, Now: time.Now}
}

func (a *Adapter) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Adapter) nextID(prefix string) string {
	a.seq++
	return fmt.Sprintf("%s-%04d", prefix, a.seq)
}

// Info reports fixed, non-secret status.
func (a *Adapter) Info(context.Context) (adapter.Info, error) {
	return adapter.Info{
		AgentVersion:    "fake",
		AgentCommit:     "0000000",
		ConfigDir:       "/fake/config/cagent",
		DataDir:         "/fake/data/cagent",
		CacheDir:        "/fake/cache/cagent",
		SessionDB:       "/fake/data/cagent/session.db",
		ModelsAvailable: true,
		ModelsHint:      "",
	}, nil
}

// ListSessions returns stored sessions, newest first.
func (a *Adapter) ListSessions(_ context.Context, workingDir string) ([]protocol.SessionSummary, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []protocol.SessionSummary
	for _, s := range a.sessions {
		if workingDir != "" && s.workingDir != workingDir {
			continue
		}
		out = append(out, protocol.SessionSummary{
			SessionID: s.id, Title: s.title, WorkingDir: s.workingDir,
			CreatedAt: s.createdAt.UTC().Format(time.RFC3339), Messages: len(s.items),
		})
	}
	sortSummaries(out)
	return out, nil
}

func sortSummaries(s []protocol.SessionSummary) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].CreatedAt > s[j-1].CreatedAt; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Seed inserts a pre-existing session so resume paths can be tested.
func (a *Adapter) Seed(id, title, workingDir string, items []protocol.Item) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[id] = &storedSession{
		id: id, title: title, workingDir: workingDir, createdAt: a.now(),
		items: items, agentName: "root", model: "fake/model-a", thinking: "medium",
	}
}

// OpenChat creates or resumes a fake chat.
func (a *Adapter) OpenChat(_ context.Context, req adapter.OpenRequest) (adapter.Chat, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	var st *storedSession
	if req.ResumeSessionID != "" {
		s, ok := a.sessions[req.ResumeSessionID]
		if !ok {
			return nil, adapter.ErrNotFound
		}
		st = s
	} else {
		id := a.nextID("sess")
		st = &storedSession{
			id: id, title: "New chat", workingDir: req.WorkingDir, createdAt: a.now(),
			agentName: "root", model: "fake/model-a", thinking: "medium",
		}
		a.sessions[id] = st
	}
	// Mirror the production adapter's best-effort startup restoration. Unknown
	// values are stale preferences and leave the fake session unchanged.
	switch req.Model {
	case "fake/model-a", "fake/model-b", "anthropic/claude-sonnet-4-5", "openai/gpt-5.6":
		st.model = req.Model
	}
	switch req.ThinkingLevel {
	case "none", "low", "medium", "high":
		st.thinking = req.ThinkingLevel
	}
	c := &chat{
		a: a, st: st,
		events:  make(chan protocol.Event, 256),
		pending: map[string]chan reply{},
	}
	c.run = protocol.RunStatus{
		State: protocol.RunStateIdle,
		Queue: protocol.QueueStatus{SteerCapacity: 8, FollowUpCapacity: 8},
	}
	return c, nil
}

// Close releases the fake store.
func (a *Adapter) Close() error { return nil }

type reply struct {
	decision protocol.ToolDecision
	action   protocol.ElicitationAction
	reason   string
	content  map[string]any
}

type chat struct {
	a  *Adapter
	st *storedSession

	mu       sync.Mutex
	events   chan protocol.Event
	run      protocol.RunStatus
	closed   bool
	cancel   context.CancelFunc
	pending  map[string]chan reply
	genID    int
	steer    []protocol.QueuedMessage
	followUp []protocol.QueuedMessage
	toolN    int
	msgN     int
}

func (c *chat) SessionID() string { return c.st.id }

func (c *chat) Meta() protocol.SessionMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.meta()
}

func (c *chat) meta() protocol.SessionMeta {
	return protocol.SessionMeta{
		SessionID:      c.st.id,
		Title:          c.st.title,
		WorkingDir:     c.st.workingDir,
		AgentName:      c.st.agentName,
		Model:          c.st.model,
		ThinkingLevel:  c.st.thinking,
		ThinkingLevels: []string{"none", "low", "medium", "high"},
		Permissions:    c.permissions(),
		CreatedAt:      c.st.createdAt.UTC().Format(time.RFC3339),
	}
}

// permissions mirrors the real adapter's configured pattern lists.
func (c *chat) permissions() protocol.PermissionsView {
	return protocol.PermissionsView{
		Allow:         []string{"read_file", "list_files"},
		Deny:          []string{"rm*"},
		SessionGrants: append([]string(nil), c.st.grants...),
	}
}

func (c *chat) Snapshot(context.Context) ([]protocol.Item, protocol.Usage, error) {
	c.a.mu.Lock()
	defer c.a.mu.Unlock()
	return append([]protocol.Item(nil), c.st.items...), c.st.usage, nil
}

func (c *chat) Events() <-chan protocol.Event { return c.events }

func (c *chat) emit(ev protocol.Event) {
	// Events are immutable once published. The scripted fake reuses its local
	// tool value while advancing states, so give the consumer its own copy.
	if ev.Tool != nil {
		tool := *ev.Tool
		ev.Tool = &tool
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	// Keep the closed check and send under the same lock as Close. Otherwise
	// Close can close the channel between the check and send.
	select {
	case c.events <- ev:
	default:
	}
}

func (c *chat) Send(ctx context.Context, text string, mode protocol.DeliveryMode) (string, bool, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", false, adapter.ErrClosed
	}
	switch mode {
	case protocol.DeliveryNormal:
		if c.run.State != protocol.RunStateIdle {
			c.mu.Unlock()
			return "", false, adapter.ErrBusy
		}
	case protocol.DeliverySteer:
		if c.run.State != protocol.RunStateRunning {
			c.mu.Unlock()
			return "", false, fmt.Errorf("%w: steer requires a running turn", adapter.ErrBusy)
		}
		queued := protocol.QueuedMessage{ID: fmt.Sprintf("steer-%d", c.msgN+len(c.steer)+1), Text: text}
		c.steer = append(c.steer, queued)
		c.run.Queue.Steer = append([]protocol.QueuedMessage(nil), c.steer...)
		c.run.Queue.SteerDepth = len(c.steer)
		run := c.run
		c.mu.Unlock()
		c.emit(protocol.Event{Type: protocol.EventRunStatus, Run: &run})
		return run.RunID, true, nil
	case protocol.DeliveryFollowUp:
		if c.run.State != protocol.RunStateRunning {
			c.mu.Unlock()
			return "", false, fmt.Errorf("%w: follow-up requires a running turn", adapter.ErrBusy)
		}
		queued := protocol.QueuedMessage{ID: fmt.Sprintf("followUp-%d", c.msgN+len(c.followUp)+1), Text: text}
		c.followUp = append(c.followUp, queued)
		c.run.Queue.FollowUps = append([]protocol.QueuedMessage(nil), c.followUp...)
		c.run.Queue.FollowUpDepth = len(c.followUp)
		run := c.run
		c.mu.Unlock()
		c.emit(protocol.Event{Type: protocol.EventRunStatus, Run: &run})
		return run.RunID, true, nil
	default:
		c.mu.Unlock()
		return "", false, errors.New("unknown delivery mode")
	}

	c.genID++
	gen := c.genID
	runID := fmt.Sprintf("run-%s-%d", c.st.id, gen)
	c.run = protocol.RunStatus{
		State: protocol.RunStateRunning, RunID: runID,
		Queue: protocol.QueueStatus{SteerCapacity: 8, FollowUpCapacity: 8},
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.cancel = cancel
	c.mu.Unlock()

	c.appendUserMessage(text)
	// Announce the running turn immediately, exactly as the real adapter does
	// on StreamStarted, so the UI can switch Send to Stop.
	c.mu.Lock()
	run := c.run
	c.mu.Unlock()
	c.emit(protocol.Event{Type: protocol.EventRunStatus, Run: &run})

	go c.script(runCtx, gen, runID, text)
	return runID, false, nil
}

func (c *chat) appendUserMessage(text string) {
	c.mu.Lock()
	c.msgN++
	id := fmt.Sprintf("%s-msg-%d", c.st.id, c.msgN)
	c.mu.Unlock()
	m := &protocol.MessageItem{
		ID: id, Role: "user", Text: text,
		CreatedAt: c.a.now().UTC().Format(time.RFC3339),
	}
	c.a.mu.Lock()
	c.st.items = append(c.st.items, protocol.Item{Kind: protocol.ItemKindMessage, Message: m})
	if c.st.title == "New chat" && text != "" {
		c.st.title = firstWords(text)
	}
	c.a.mu.Unlock()
	c.emit(protocol.Event{Type: protocol.EventMessageItem, Message: m})
	meta := c.Meta()
	c.emit(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})
}

func firstWords(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func (c *chat) pause(ctx context.Context) bool {
	if c.a.Delay <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-time.After(c.a.Delay):
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

// script replays one deterministic turn.
func (c *chat) script(ctx context.Context, gen int, runID, prompt string) {
	defer c.settle(gen, runID)

	c.mu.Lock()
	c.msgN++
	itemID := fmt.Sprintf("%s-msg-%d", c.st.id, c.msgN)
	c.mu.Unlock()

	msg := &protocol.MessageItem{
		ID: itemID, Role: "assistant", AgentName: c.st.agentName,
		Streaming: true, Model: c.st.model, CreatedAt: c.a.now().UTC().Format(time.RFC3339),
	}
	c.emit(protocol.Event{Type: protocol.EventMessageItem, Message: msg})

	if !c.pause(ctx) {
		return
	}
	c.emit(protocol.Event{
		Type:  protocol.EventReasoningDelta,
		Delta: &protocol.Delta{ItemID: itemID, Text: "Considering the request."},
	})
	c.emit(protocol.Event{Type: protocol.EventReasoningEnd, Ref: &protocol.ItemRef{ItemID: itemID}})

	var text strings.Builder
	for _, chunk := range []string{"Working on **", firstWords(prompt), "**.\n\n"} {
		if !c.pause(ctx) {
			return
		}
		text.WriteString(chunk)
		c.emit(protocol.Event{
			Type:  protocol.EventAssistantDelta,
			Delta: &protocol.Delta{ItemID: itemID, Text: chunk},
		})
	}

	switch {
	case strings.Contains(prompt, "/transfer"):
		t := &protocol.Transfer{ID: runID + "-t1", FromAgent: "root", ToAgent: "helper", Switching: true}
		c.emit(protocol.Event{Type: protocol.EventTransfer, Transfer: t})
		c.emit(protocol.Event{
			Type:     protocol.EventTransfer,
			Transfer: &protocol.Transfer{ID: runID + "-t2", FromAgent: "helper", ToAgent: "root"},
		})
	case strings.Contains(prompt, "/elicit"):
		if !c.elicit(ctx, runID) {
			return
		}
	case strings.Contains(prompt, "/error"):
		c.emit(protocol.Event{Type: protocol.EventNotice, Notice: &protocol.Notice{
			ID: runID + "-err", Level: protocol.NoticeError,
			Message: "the model returned an error: simulated failure", Code: "model_error",
		}})
	case strings.Contains(prompt, "/compact"):
		c.emit(protocol.Event{Type: protocol.EventNotice, Notice: &protocol.Notice{
			ID: runID + "-compact", Level: protocol.NoticeInfo,
			Message: "Context compaction applied.", Code: "compaction",
		}})
	case strings.Contains(prompt, "/retry"):
		c.emit(protocol.Event{Type: protocol.EventNotice, Notice: &protocol.Notice{
			ID: runID + "-retry", Level: protocol.NoticeWarning,
			Message: "Model fake/model-a failed; retrying with fake/model-b (attempt 1/3).", Code: "retry",
		}})
	case strings.Contains(prompt, "/notool"):
		// plain text turn
	default:
		if !c.toolTurn(ctx, strings.Contains(prompt, "/confirm")) {
			return
		}
	}

	if !c.pause(ctx) {
		return
	}
	tail := "Done."
	text.WriteString(tail)
	c.emit(protocol.Event{
		Type:  protocol.EventAssistantDelta,
		Delta: &protocol.Delta{ItemID: itemID, Text: tail},
	})
	c.emit(protocol.Event{Type: protocol.EventAssistantEnd, Ref: &protocol.ItemRef{ItemID: itemID}})

	final := *msg
	final.Streaming = false
	final.Text = text.String()
	final.Reasoning = "Considering the request."
	final.Cost = 0.0012
	final.InputTokens = 120
	final.OutputTokens = 45
	final.CachedInputTokens = 80
	final.CacheWriteTokens = 10
	final.ReasoningTokens = 8
	c.a.mu.Lock()
	c.st.items = append(c.st.items, protocol.Item{Kind: protocol.ItemKindMessage, Message: &final})
	c.st.usage.InputTokens += 120
	c.st.usage.OutputTokens += 45
	c.st.usage.Cost += 0.0012
	c.st.usage.ContextLimit = 200000
	usage := c.st.usage
	c.a.mu.Unlock()
	c.emit(protocol.Event{Type: protocol.EventUsage, Usage: &usage})
}

func (c *chat) toolTurn(ctx context.Context, requireConfirmation bool) bool {
	c.mu.Lock()
	c.toolN++
	id := fmt.Sprintf("%s-tool-%d", c.st.id, c.toolN)
	c.mu.Unlock()

	act := &protocol.ToolActivity{
		ID: id, Name: "shell", DisplayName: "Shell", Category: "shell",
		AgentName: c.st.agentName, ArgsSummary: `ls -la /workspace`,
		Arguments: map[string]any{"cmd": "ls -la /workspace", "cwd": "."}, State: protocol.ToolStatePending,
	}
	c.emit(protocol.Event{Type: protocol.EventToolStart, Tool: act})

	// /confirm simulates an explicit permission rule that still asks even though
	// the session itself always auto-approves tools.
	if requireConfirmation {
		act.State = protocol.ToolStateAwaiting
		c.emit(protocol.Event{Type: protocol.EventToolUpdate, Tool: act})
		pattern := "shell(ls*)"
		req := &protocol.ToolConfirmationRequest{
			ToolCallID: id, ToolName: "shell", DisplayName: "Shell", AgentName: c.st.agentName,
			ArgsSummary: `ls -la /workspace`, Pattern: pattern,
			PatternLabel:     "Always allow " + pattern,
			RejectionReasons: []protocol.RejectionReason{{Label: "Not now", Reason: "The user declined this action."}},
		}
		ch := make(chan reply, 1)
		c.mu.Lock()
		c.pending[id] = ch
		c.mu.Unlock()
		c.emit(protocol.Event{Type: protocol.EventToolConfirmation, Confirmation: req})

		var r reply
		select {
		case r = <-ch:
		case <-ctx.Done():
			return false
		}
		c.mu.Lock()
		delete(c.pending, id)
		if r.decision == protocol.DecisionApproveAlways {
			c.st.grants = append(c.st.grants, pattern)
		}
		c.mu.Unlock()
		c.emit(protocol.Event{
			Type:         protocol.EventToolResolved,
			ToolResolved: &protocol.ToolResolved{ToolCallID: id, Decision: r.decision, Pattern: pattern},
		})
		meta := c.Meta()
		c.emit(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})
		if r.decision == protocol.DecisionReject {
			act.State = protocol.ToolStateRejected
			act.Preview = "Rejected: " + r.reason
			c.emit(protocol.Event{Type: protocol.EventToolEnd, Tool: act})
			return true
		}
	}

	act.State = protocol.ToolStateRunning
	c.emit(protocol.Event{Type: protocol.EventToolUpdate, Tool: act})
	if !c.pause(ctx) {
		return false
	}
	act.State = protocol.ToolStateSuccess
	act.Preview = "total 8\ndrwxr-xr-x  4 user staff  128 Jan  1 00:00 .\n-rw-r--r--  1 user staff   42 Jan  1 00:00 README.md"
	act.OutputBytes = len(act.Preview)
	c.emit(protocol.Event{Type: protocol.EventToolEnd, Tool: act})

	c.a.mu.Lock()
	c.st.items = append(c.st.items, protocol.Item{Kind: protocol.ItemKindTool, Tool: act})
	c.a.mu.Unlock()
	return true
}

func (c *chat) elicit(ctx context.Context, runID string) bool {
	id := runID + "-elicit-1"
	ch := make(chan reply, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	c.emit(protocol.Event{Type: protocol.EventElicitation, Elicitation: &protocol.ElicitationRequest{
		ElicitationID: id, Message: "Which branch should I use?", Mode: "form",
		AgentName: c.st.agentName,
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"branch": map[string]any{"type": "string", "title": "Branch"},
		}},
	}})
	select {
	case r := <-ch:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		c.emit(protocol.Event{
			Type:           protocol.EventElicitResolved,
			ElicitResolved: &protocol.ElicitResolved{ElicitationID: id},
		})
		c.emit(protocol.Event{Type: protocol.EventNotice, Notice: &protocol.Notice{
			ID: id + "-n", Level: protocol.NoticeInfo,
			Message: fmt.Sprintf("Elicitation %s answered (%s).", id, r.action),
		}})
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *chat) settle(gen int, runID string) {
	c.mu.Lock()
	if c.genID != gen {
		c.mu.Unlock()
		return
	}
	// Drain queues exactly like a settled runtime: steer messages were
	// consumed by the turn, follow-ups become the next turn's prompt.
	next := ""
	if len(c.followUp) > 0 {
		next, c.followUp = c.followUp[0].Text, c.followUp[1:]
	}
	c.steer = nil
	c.run = protocol.RunStatus{
		State: protocol.RunStateIdle,
		Queue: protocol.QueueStatus{
			SteerCapacity: 8, FollowUpCapacity: 8,
			FollowUpDepth: len(c.followUp), FollowUps: append([]protocol.QueuedMessage(nil), c.followUp...),
		},
	}
	run := c.run
	c.cancel = nil
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	_ = runID
	c.emit(protocol.Event{Type: protocol.EventRunStatus, Run: &run})
	if next != "" {
		_, _, _ = c.Send(context.Background(), next, protocol.DeliveryNormal)
	}
}

func (c *chat) Abort() {
	c.mu.Lock()
	if c.run.State == protocol.RunStateRunning {
		c.run.State = protocol.RunStateStopping
		c.steer = nil
		c.followUp = nil
		c.run.Queue.SteerDepth = 0
		c.run.Queue.FollowUpDepth = 0
		c.run.Queue.Steer = nil
		c.run.Queue.FollowUps = nil
	}
	cancel := c.cancel
	run := c.run
	for id, ch := range c.pending {
		select {
		case ch <- reply{decision: protocol.DecisionReject, action: protocol.ElicitCancel, reason: "run stopped"}:
		default:
		}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.emit(protocol.Event{Type: protocol.EventRunStatus, Run: &run})
	if cancel != nil {
		cancel()
	}
}

func (c *chat) Confirm(_ context.Context, toolCallID string, d protocol.ToolDecision, reason string) error {
	c.mu.Lock()
	ch, ok := c.pending[toolCallID]
	c.mu.Unlock()
	if !ok {
		return adapter.ErrNotFound
	}
	ch <- reply{decision: d, reason: reason}
	return nil
}

func (c *chat) Elicit(_ context.Context, id string, action protocol.ElicitationAction, content map[string]any) error {
	c.mu.Lock()
	ch, ok := c.pending[id]
	c.mu.Unlock()
	if !ok {
		return adapter.ErrNotFound
	}
	ch <- reply{action: action, content: content}
	return nil
}

func (c *chat) Models(context.Context) []protocol.ModelOption {
	return []protocol.ModelOption{
		{
			Name: "default", Ref: "fake/model-a", Provider: "fake", Model: "model-a", Family: "fake",
			ContextLimit: 200000, InputCost: 3, OutputCost: 15,
			IsCurrent: c.st.model == "fake/model-a", IsDefault: true,
		},
		{
			Name: "fast", Ref: "fake/model-b", Provider: "fake", Model: "model-b", Family: "fake",
			ContextLimit: 128000, InputCost: 0.8, OutputCost: 4,
			IsCurrent: c.st.model == "fake/model-b",
		},
		{
			Name: "claude-sonnet-4-5", Ref: "anthropic/claude-sonnet-4-5", Provider: "anthropic",
			Model: "claude-sonnet-4-5", Family: "claude", ContextLimit: 200000,
			InputCost: 3, OutputCost: 15, IsCatalog: true,
		},
		{
			Name: "gpt-5.6", Ref: "openai/gpt-5.6", Provider: "openai", Model: "gpt-5.6",
			Family: "gpt", ContextLimit: 400000, InputCost: 1.25, OutputCost: 10, IsCatalog: true,
		},
	}
}

func (c *chat) Commands(context.Context) []protocol.CommandInfo {
	return []protocol.CommandInfo{
		{Name: "notool", Description: "Reply without calling a tool", Kind: "command"},
		{Name: "elicit", Description: "Trigger an elicitation", Kind: "command"},
		{Name: "transfer", Description: "Delegate to the sub-agent", Kind: "command"},
	}
}

func (c *chat) idle() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.run.State != protocol.RunStateIdle {
		return adapter.ErrBusy
	}
	return nil
}

func (c *chat) SetModel(_ context.Context, ref string) error {
	if err := c.idle(); err != nil {
		return err
	}
	known := false
	for _, m := range c.Models(context.Background()) {
		if m.Ref == ref {
			known = true
		}
	}
	if !known {
		return adapter.ErrNotFound
	}
	c.a.mu.Lock()
	c.st.model = ref
	c.a.mu.Unlock()
	meta := c.Meta()
	c.emit(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})
	return nil
}

func (c *chat) SetThinking(_ context.Context, level string) error {
	if err := c.idle(); err != nil {
		return err
	}
	switch level {
	case "none", "low", "medium", "high":
	default:
		return adapter.ErrNotFound
	}
	c.a.mu.Lock()
	c.st.thinking = level
	c.a.mu.Unlock()
	meta := c.Meta()
	c.emit(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})
	return nil
}

func (c *chat) Retitle(_ context.Context, title string) error {
	c.a.mu.Lock()
	c.st.title = title
	c.a.mu.Unlock()
	meta := c.Meta()
	c.emit(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})
	return nil
}

func (c *chat) Compact(context.Context) error {
	if err := c.idle(); err != nil {
		return err
	}
	c.a.mu.Lock()
	c.st.items = append(c.st.items, protocol.Item{
		Kind:    protocol.ItemKindSummary,
		Summary: &protocol.Summary{ID: c.st.id + "-summary", Text: "Earlier turns were summarized."},
	})
	c.a.mu.Unlock()
	c.emit(protocol.Event{Type: protocol.EventNotice, Notice: &protocol.Notice{
		ID: c.st.id + "-compact-notice", Level: protocol.NoticeInfo,
		Message: "Context compaction applied.", Code: "compaction",
	}})
	return nil
}

func (c *chat) Stats(context.Context) protocol.Stats {
	c.a.mu.Lock()
	defer c.a.mu.Unlock()
	tools := 0
	msgs := 0
	for _, it := range c.st.items {
		switch it.Kind {
		case protocol.ItemKindTool:
			tools++
		case protocol.ItemKindMessage:
			msgs++
		}
	}
	return protocol.Stats{
		Usage: c.st.usage, Messages: msgs, ToolCalls: tools,
		Model: c.st.model, AgentName: c.st.agentName,
		DurationSec: int64(c.a.now().Sub(c.st.createdAt).Seconds()),
	}
}

func (c *chat) Close(context.Context) error {
	c.Abort()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.events)
	c.mu.Unlock()
	return nil
}
