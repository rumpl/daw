package dagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	daagent "github.com/docker/docker-agent/pkg/agent"
	dachat "github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/effort"
	"github.com/docker/docker-agent/pkg/permissions"
	daruntime "github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/team"
	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tui/components/toolconfirm"
	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/protocol"
	"github.com/rumpl/daw/internal/sessionlineage"
)

type pendingTool struct {
	call    tools.ToolCall
	pattern string
}

type partialTool struct {
	call       tools.ToolCall
	definition tools.Tool
}

// chat is one live runtime + session pair.
type chat struct {
	a          *Adapter
	rt         daruntime.Runtime
	team       *team.Team
	sess       *session.Session
	agentName  string
	workingDir string

	events chan protocol.Event

	mu           sync.Mutex
	dispatchMu   sync.Mutex
	closed       bool
	run          protocol.RunStatus
	cancel       context.CancelFunc
	generation   uint64
	pendingTools map[string]pendingTool
	partialTools map[string]partialTool
	pendingElic  map[string]struct{}
	grants       []string
	// unsaved marks a brand-new session whose row is created lazily, on the
	// first real prompt, exactly as the CLI does.
	unsaved      bool
	agentsIgnore bool
	model        string
	thinking     string
	thinkLevels  []string
	assistantSeq int
	userSeq      int
	curAssistant string
	noticeSeq    int
	steerQueue   *observableQueue
	followQueue  *observableQueue
}

func (c *chat) SessionID() string { return c.sess.ID }

func (c *chat) emit(ev protocol.Event) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	defer func() { _ = recover() }() // channel closed concurrently with dispose
	select {
	case c.events <- ev:
	default:
		// Drop rather than block the runtime; the HTTP layer resnapshots.
	}
}

func (c *chat) Events() <-chan protocol.Event { return c.events }

// collectWarnings surfaces load-time warnings instead of swallowing them.
func (c *chat) collectWarnings(ag *daagent.Agent) {
	for _, w := range ag.DrainWarnings() {
		c.notice(protocol.NoticeWarning, w, "load_warning")
	}
}

func (c *chat) notice(level protocol.NoticeLevel, msg, code string) {
	c.mu.Lock()
	c.noticeSeq++
	id := fmt.Sprintf("%s-notice-%d", c.sess.ID, c.noticeSeq)
	c.mu.Unlock()
	c.emit(protocol.Event{
		Type:   protocol.EventNotice,
		Notice: &protocol.Notice{ID: id, Level: level, Message: msg, Code: code},
	})
}

// startBackgroundBridges wires the runtime's out-of-stream handlers so
// background jobs and MCP notifications are not lost.
func (c *chat) startBackgroundBridges() {
	c.rt.OnToolsChanged(func(ev daruntime.Event) { c.normalize(ev) })
	c.rt.OnBackgroundEvent(func(ev daruntime.Event) { c.normalize(ev) })
	c.rt.OnElicitationRequest(func(ev daruntime.Event) { c.normalize(ev) })
}

// ---------------------------------------------------------------------------
// metadata
// ---------------------------------------------------------------------------

func (c *chat) Meta() protocol.SessionMeta {
	c.mu.Lock()
	grants := append([]string(nil), c.grants...)
	model, thinking, levels := c.model, c.thinking, append([]string(nil), c.thinkLevels...)
	ignore := c.agentsIgnore
	c.mu.Unlock()

	ctx := context.Background()
	if model == "" {
		if ag, err := c.team.Agent(c.agentName); err == nil && ag != nil {
			if m := ag.Model(ctx); m != nil {
				model = m.ID().String()
			}
		}
	}
	if len(levels) == 0 {
		levels = c.supportedThinkingLevels()
	}
	checker := c.team.Permissions()
	view := viewFromChecker(checker, grants)
	view.AgentsIgnore = ignore
	if sp := c.sess.ClonePermissions(); sp != nil {
		view.Allow = append(sp.Allow, view.Allow...)
		view.Ask = append(sp.Ask, view.Ask...)
		view.Deny = append(sp.Deny, view.Deny...)
	}

	origin := sessionlineage.FromAttributes(c.sess.AttributesSnapshot())
	return protocol.SessionMeta{
		SessionID: c.sess.ID, Title: c.sess.TitleSnapshot(),
		WorkingDir: c.workingDir, AgentName: c.agentName,
		Model: model, ThinkingLevel: thinking, ThinkingLevels: levels,
		Permissions: view, CreatedAt: c.sess.CreatedAt.UTC().Format(time.RFC3339),
		ParentSessionID: origin.ParentSessionID, RootSessionID: origin.RootSessionID,
		OriginKind: origin.Kind, OriginPluginID: origin.PluginID,
		Attributes: c.sess.AttributesSnapshot(),
	}
}

func (c *chat) supportedThinkingLevels() []string {
	return runtimeThinkingLevels(context.Background(), c.rt)
}

// ---------------------------------------------------------------------------
// snapshot from the store
// ---------------------------------------------------------------------------

// Snapshot rebuilds the timeline from the session's own messages, as the
// matched store returns them. It is never reconstructed from our bookkeeping.
func (c *chat) Snapshot(context.Context) ([]protocol.Item, protocol.Usage, error) {
	items := c.sess.MessagesSnapshot()
	out := make([]protocol.Item, 0, len(items))
	toolDefs := map[string]tools.Tool{}
	toolResults := map[string]dachat.Message{}

	for _, it := range items {
		if it.Message == nil {
			continue
		}
		if it.Message.Message.Role == dachat.MessageRoleTool {
			toolResults[it.Message.Message.ToolCallID] = it.Message.Message
		}
		for _, def := range it.Message.Message.ToolDefinitions {
			toolDefs[def.Name] = def
		}
	}

	for i, it := range items {
		switch {
		case it.Summary != "":
			out = append(out, protocol.Item{
				Kind: protocol.ItemKindSummary,
				Summary: &protocol.Summary{
					ID:   fmt.Sprintf("%s-sum-%d", c.sess.ID, i),
					Text: it.Summary, Cost: it.Cost,
				},
			})
		case it.Error != nil:
			out = append(out, protocol.Item{
				Kind: protocol.ItemKindNotice,
				Notice: &protocol.Notice{
					ID:    fmt.Sprintf("%s-err-%d", c.sess.ID, i),
					Level: protocol.NoticeError, Message: it.Error.Message, Code: it.Error.Code,
				},
			})
		case it.SubSession != nil:
			out = append(out, protocol.Item{
				Kind: protocol.ItemKindTransfer,
				Transfer: &protocol.Transfer{
					ID:        fmt.Sprintf("%s-sub-%d", c.sess.ID, i),
					FromAgent: c.agentName, ToAgent: it.SubSession.AgentName,
				},
			})
		case it.Message != nil:
			m := it.Message
			if m.Implicit || m.Message.Role == dachat.MessageRoleTool ||
				m.Message.Role == dachat.MessageRoleSystem {
				continue
			}
			id := fmt.Sprintf("%s-m-%d", c.sess.ID, i)
			mi := &protocol.MessageItem{
				ID: id, Role: string(m.Message.Role), AgentName: m.AgentName,
				Text: m.Message.Content, Reasoning: m.Message.ReasoningContent,
				Attachments: attachmentMetadata(m.Message.MultiContent),
				CreatedAt:   m.Message.CreatedAt, Model: m.Message.Model, Cost: m.Message.Cost,
			}
			if m.Message.Usage != nil {
				mi.InputTokens = m.Message.Usage.InputTokens
				mi.OutputTokens = m.Message.Usage.OutputTokens
				mi.CachedInputTokens = m.Message.Usage.CachedInputTokens
				mi.CacheWriteTokens = m.Message.Usage.CacheWriteTokens
				mi.ReasoningTokens = m.Message.Usage.ReasoningTokens
			}
			// Assistant messages that contain only tool calls have no display text,
			// but they still represent a billed model invocation with their own
			// usage and cost. Keep them in the API alongside the ToolActivity items.
			if strings.TrimSpace(mi.Text) != "" || strings.TrimSpace(mi.Reasoning) != "" ||
				len(m.Message.ToolCalls) > 0 || m.Message.Usage != nil || m.Message.Cost != 0 {
				out = append(out, protocol.Item{Kind: protocol.ItemKindMessage, Message: mi})
			}
			for _, tc := range m.Message.ToolCalls {
				act := &protocol.ToolActivity{
					ID: tc.ID, Name: tc.Function.Name, AgentName: m.AgentName,
					ArgsSummary: summarizeArgs(tc), Arguments: presentationArgs(tc), State: protocol.ToolStateSuccess,
				}
				if def, ok := toolDefs[tc.Function.Name]; ok {
					act.DisplayName = def.DisplayName()
					act.Category = def.Category
				}
				if res, ok := toolResults[tc.ID]; ok {
					act.Preview = res.Content
					act.Images = storedToolImages(res)
					act.OutputBytes = len(res.Content)
					act.IsError = res.IsError
					if res.IsError {
						act.State = protocol.ToolStateError
					}
				}
				out = append(out, protocol.Item{Kind: protocol.ItemKindTool, Tool: act})
			}
		}
	}

	in, outTok, cost := c.sess.TokensAndCost()
	usage := protocol.Usage{InputTokens: in, OutputTokens: outTok, Cost: cost}
	return out, usage, nil
}

func attachmentMetadata(parts []dachat.MessagePart) []protocol.Attachment {
	var out []protocol.Attachment
	for i, part := range parts {
		if part.Type != dachat.MessagePartTypeDocument || part.Document == nil {
			continue
		}
		doc := part.Document
		size := doc.Size
		if size == 0 {
			size = int64(len(doc.Source.InlineData) + len(doc.Source.InlineText))
		}
		attachment := protocol.Attachment{
			ID: fmt.Sprintf("stored-%d", i), Name: doc.Name, MimeType: doc.MimeType, Size: size,
		}
		if dachat.IsImageMimeType(doc.MimeType) && len(doc.Source.InlineData) > 0 {
			attachment.Data = base64.StdEncoding.EncodeToString(doc.Source.InlineData)
		}
		out = append(out, attachment)
	}
	return out
}

// summarizeArgs renders a short, safe one-line summary of a tool call's
// arguments. It never returns the whole payload.
func summarizeArgs(tc tools.ToolCall) string {
	args := strings.TrimSpace(tc.Function.Arguments)
	if args == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err == nil {
		if cmd, ok := m["cmd"].(string); ok {
			return truncate(cmd, 300)
		}
		if p, ok := m["path"].(string); ok {
			return truncate(p, 300)
		}
		parts := make([]string, 0, len(m))
		for k, v := range m {
			parts = append(parts, fmt.Sprintf("%s=%v", k, truncate(fmt.Sprint(v), 80)))
		}
		return truncate(strings.Join(parts, " "), 300)
	}
	return truncate(args, 300)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------------------------------------------------------------------------
// run lifecycle
// ---------------------------------------------------------------------------

func buildAttachments(text string, attachments []adapter.Attachment) (string, []dachat.MessagePart, error) {
	if len(attachments) == 0 {
		return text, nil, nil
	}
	var content strings.Builder
	content.WriteString(text)
	parts := make([]dachat.MessagePart, 0, len(attachments)+1)
	for _, attachment := range attachments {
		doc := dachat.Document{
			Name: attachment.Name, MimeType: attachment.MimeType, Size: int64(len(attachment.Data)),
		}
		if strings.HasPrefix(attachment.MimeType, "text/") {
			doc.Source.InlineText = string(attachment.Data)
			parts = append(parts, dachat.MessagePart{Type: dachat.MessagePartTypeDocument, Document: &doc})
			continue
		}
		doc.Source.InlineData = attachment.Data
		processed, _, err := dachat.ProcessAttachmentWithMetadata(dachat.MessagePart{Type: dachat.MessagePartTypeDocument, Document: &doc})
		if err != nil {
			return "", nil, fmt.Errorf("process attachment: %w", err)
		}
		parts = append(parts, dachat.MessagePart{Type: dachat.MessagePartTypeDocument, Document: &processed})
	}
	parts = append([]dachat.MessagePart{{Type: dachat.MessagePartTypeText, Text: content.String()}}, parts...)
	return text, parts, nil
}

func (c *chat) Send(ctx context.Context, text string, attachments []adapter.Attachment, preferred protocol.DeliveryMode) (protocol.DeliveryMode, string, bool, error) {
	text, parts, err := buildAttachments(text, attachments)
	if err != nil {
		return preferred, "", false, err
	}
	// Dispatch and the idle→running transition are one serialized operation.
	// The browser's SSE state can lag, so the runtime state—not the requested
	// hint—decides whether this starts a turn or joins the active one.
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return preferred, "", false, adapter.ErrClosed
	}
	state := c.run.State
	c.mu.Unlock()

	mode := preferred
	if state == protocol.RunStateIdle {
		mode = protocol.DeliveryNormal
	} else if mode == protocol.DeliveryNormal {
		mode = protocol.DeliverySteer
	}

	switch mode {
	case protocol.DeliverySteer:
		if err := c.rt.Steer(ctx, daruntime.QueuedMessage{Content: text, MultiContent: parts}); err != nil {
			return mode, "", false, err
		}
		return mode, c.runID(), true, nil
	case protocol.DeliveryFollowUp:
		if err := c.rt.FollowUp(ctx, daruntime.QueuedMessage{Content: text, MultiContent: parts}); err != nil {
			return mode, "", false, err
		}
		return mode, c.runID(), true, nil
	}

	// Resolve slash commands, skills and prompt files through docker-agent's
	// own command resolution before the message reaches the model.
	resolved := daruntime.ResolveCommand(ctx, c.rt, text)
	if strings.TrimSpace(resolved) == "" {
		resolved = text
	}

	c.mu.Lock()
	c.generation++
	gen := c.generation
	runID := fmt.Sprintf("run-%s-%d", c.sess.ID, gen)
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.cancel = cancel
	c.run = protocol.RunStatus{State: protocol.RunStateRunning, RunID: runID}
	c.curAssistant = ""
	c.mu.Unlock()
	c.refreshQueue()

	// Persist the session row on the first real prompt (lazy creation).
	c.mu.Lock()
	unsaved := c.unsaved
	c.unsaved = false
	c.mu.Unlock()
	if unsaved {
		if err := c.a.store.AddSession(ctx, c.sess); err != nil {
			c.a.log.Warn("persisting new session", "error", err)
		}
	}

	msg := session.UserMessage(resolved, parts...)
	c.sess.AddMessage(msg)
	c.publishRun()

	// Generate a session title from the first user message(s), exactly as the
	// CLI/TUI do: a one-shot LLM call over the recent user messages, run in
	// parallel with the turn so it never delays the response. This only fires
	// while the session still carries the placeholder title.
	c.maybeGenerateTitle(runCtx)

	go c.runLoop(runCtx, gen, runID)
	return mode, runID, false, nil
}

func (c *chat) runID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.run.RunID
}

// runLoop consumes one RunStream. The turn is complete when the channel
// closes — the matched module documents StreamStoppedEvent as best-effort.
func (c *chat) runLoop(ctx context.Context, gen uint64, runID string) {
	defer func() {
		if r := recover(); r != nil {
			c.notice(protocol.NoticeError, "the agent run failed unexpectedly", "panic")
		}
		c.settle(gen)
	}()

	for ev := range c.rt.RunStream(ctx, c.sess) {
		c.mu.Lock()
		stale := c.generation != gen || c.closed
		c.mu.Unlock()
		if stale {
			continue
		}
		c.normalize(ev)
	}
	_ = runID
}

func (c *chat) settle(gen uint64) {
	c.mu.Lock()
	if c.generation != gen {
		c.mu.Unlock()
		return
	}
	if c.curAssistant != "" {
		id := c.curAssistant
		c.curAssistant = ""
		c.mu.Unlock()
		c.emit(protocol.Event{Type: protocol.EventAssistantEnd, Ref: &protocol.ItemRef{ItemID: id}})
		c.mu.Lock()
	}
	c.run = protocol.RunStatus{State: protocol.RunStateIdle}
	c.cancel = nil
	clear(c.partialTools)
	c.mu.Unlock()

	// Confirm the runtime really is idle and both queues are drained before
	// reporting a settled turn.
	c.refreshQueue()
	c.publishRun()

	meta := c.Meta()
	c.emit(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})
}

func (c *chat) refreshQueue() {
	steer, steerCapacity := c.steerQueue.snapshot()
	followUps, followUpCapacity := c.followQueue.snapshot()
	c.mu.Lock()
	c.run.Queue = protocol.QueueStatus{
		SteerDepth: len(steer), SteerCapacity: steerCapacity, Steer: steer,
		FollowUpDepth: len(followUps), FollowUpCapacity: followUpCapacity, FollowUps: followUps,
	}
	c.mu.Unlock()
}

func (c *chat) queueChanged() {
	c.refreshQueue()
	c.publishRun()
}

func (c *chat) publishRun() {
	c.mu.Lock()
	run := c.run
	c.mu.Unlock()
	c.emit(protocol.Event{Type: protocol.EventRunStatus, Run: &run})
}

// Abort cancels the run context and clears queued messages. The chat stays in
// Stopping until the stream actually closes.
func (c *chat) Abort() {
	c.mu.Lock()
	cancel := c.cancel
	if c.run.State == protocol.RunStateRunning {
		c.run.State = protocol.RunStateStopping
	}
	// Reject anything blocking so the runtime can unwind.
	pendingTools := make([]string, 0, len(c.pendingTools))
	for id := range c.pendingTools {
		pendingTools = append(pendingTools, id)
	}
	pendingElic := make([]string, 0, len(c.pendingElic))
	for id := range c.pendingElic {
		pendingElic = append(pendingElic, id)
	}
	c.mu.Unlock()

	ctx := context.Background()
	for range pendingTools {
		c.rt.Resume(ctx, daruntime.ResumeReject("the user stopped the run"))
	}
	for _, id := range pendingElic {
		_ = c.rt.ResumeElicitation(ctx, tools.ElicitationActionCancel, nil, id)
	}
	c.publishRun()
	if cancel != nil {
		cancel()
	}
}

// ---------------------------------------------------------------------------
// interactive surfaces
// ---------------------------------------------------------------------------

// Confirm applies the user's decision. The permission pattern granted is the
// one built by toolconfirm.BuildPermissionPattern when the dialog was raised —
// the same string the user was shown.
func (c *chat) Confirm(ctx context.Context, toolCallID string, decision protocol.ToolDecision, reason string) error {
	c.mu.Lock()
	pt, ok := c.pendingTools[toolCallID]
	if !ok {
		c.mu.Unlock()
		return adapter.ErrNotFound
	}
	delete(c.pendingTools, toolCallID)
	if decision == protocol.DecisionApproveAlways {
		c.grants = append(c.grants, pt.pattern)
	}
	c.mu.Unlock()

	var d toolconfirm.Decision
	switch decision {
	case protocol.DecisionApprove:
		d = toolconfirm.Approve
	case protocol.DecisionApproveAlways:
		d = toolconfirm.ApproveTool
	case protocol.DecisionReject:
		d = toolconfirm.Reject
	default:
		return adapter.ErrNotFound
	}
	c.rt.Resume(ctx, d.Resume(pt.pattern, reason))
	c.emit(protocol.Event{Type: protocol.EventToolResolved, ToolResolved: &protocol.ToolResolved{
		ToolCallID: toolCallID, Decision: decision, Pattern: pt.pattern,
	}})
	return nil
}

// Elicit answers one elicitation, correlated by its ID.
func (c *chat) Elicit(ctx context.Context, id string, action protocol.ElicitationAction, content map[string]any) error {
	c.mu.Lock()
	_, ok := c.pendingElic[id]
	if !ok {
		c.mu.Unlock()
		return adapter.ErrNotFound
	}
	delete(c.pendingElic, id)
	c.mu.Unlock()

	var a tools.ElicitationAction
	switch action {
	case protocol.ElicitAccept:
		a = tools.ElicitationActionAccept
	case protocol.ElicitDecline:
		a = tools.ElicitationActionDecline
	default:
		a = tools.ElicitationActionCancel
	}
	if err := c.rt.ResumeElicitation(ctx, a, content, id); err != nil {
		return err
	}
	c.emit(protocol.Event{
		Type:           protocol.EventElicitResolved,
		ElicitResolved: &protocol.ElicitResolved{ElicitationID: id},
	})
	return nil
}

// ---------------------------------------------------------------------------
// configuration
// ---------------------------------------------------------------------------

func (c *chat) Models(ctx context.Context) []protocol.ModelOption {
	if !c.rt.SupportsModelSwitching() {
		return nil
	}
	return modelOptions(c.rt.AvailableModels(ctx))
}

func (c *chat) Commands(ctx context.Context) []protocol.CommandInfo {
	info := c.rt.CurrentAgentInfo(ctx)
	out := make([]protocol.CommandInfo, 0, len(info.Commands))
	for name, cmd := range info.Commands {
		desc := cmd.Description
		if desc == "" {
			desc = truncate(cmd.DisplayText(), 120)
		}
		out = append(out, protocol.CommandInfo{Name: name, Description: desc, Kind: "command"})
	}
	if ts := c.rt.CurrentAgentSkillsToolset(); ts != nil {
		for _, s := range ts.Skills() {
			out = append(out, protocol.CommandInfo{
				Name: s.Name, Description: s.Description, Kind: "skill",
			})
		}
	}
	return out
}

func (c *chat) Tools(ctx context.Context) ([]protocol.ToolOption, error) {
	definitions, err := c.rt.CurrentAgentTools(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	disabled := make(map[string]bool, len(c.sess.ExcludedTools))
	for _, name := range c.sess.ExcludedTools {
		disabled[name] = true
	}
	c.mu.Unlock()
	out := make([]protocol.ToolOption, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, protocol.ToolOption{
			Name: definition.Name, Category: definition.Category,
			Description: definition.Description, Enabled: !disabled[definition.Name],
		})
	}
	return out, nil
}

func (c *chat) SetToolEnabled(_ context.Context, name string, enabled bool) error {
	if err := c.idle(); err != nil {
		return err
	}
	definitions, err := c.rt.CurrentAgentTools(context.Background())
	if err != nil {
		return err
	}
	known := false
	for _, definition := range definitions {
		if definition.Name == name {
			known = true
			break
		}
	}
	if !known {
		return adapter.ErrNotFound
	}
	c.mu.Lock()
	disabled := make([]string, 0, len(c.sess.ExcludedTools)+1)
	seen := false
	for _, current := range c.sess.ExcludedTools {
		if current == name {
			seen = true
			if enabled {
				continue
			}
		}
		disabled = append(disabled, current)
	}
	if !enabled && !seen {
		disabled = append(disabled, name)
	}
	c.sess.ExcludedTools = disabled
	c.mu.Unlock()
	return nil
}

func (c *chat) idle() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.run.State != protocol.RunStateIdle {
		return adapter.ErrBusy
	}
	return nil
}

func (c *chat) SetModel(ctx context.Context, ref string) error {
	if err := c.idle(); err != nil {
		return err
	}
	if err := c.rt.SetAgentModel(ctx, c.agentName, ref); err != nil {
		if errors.Is(err, daruntime.ErrUnsupported) {
			return adapter.ErrUnsupported
		}
		return err
	}
	if c.sess.AgentModelOverrides == nil {
		c.sess.AgentModelOverrides = map[string]string{}
	}
	c.sess.AgentModelOverrides[c.agentName] = ref
	c.mu.Lock()
	unsaved := c.unsaved
	c.model = ref
	c.mu.Unlock()
	// Keep lazy session creation intact when a preference is selected before
	// the first prompt. AddSession will persist the in-memory override with the
	// first message; an existing session is updated immediately.
	if !unsaved {
		if err := c.a.store.UpdateSession(ctx, c.sess); err != nil {
			c.a.log.Warn("persisting model override", "error", err)
		}
	}
	return nil
}

func (c *chat) SetThinking(ctx context.Context, level string) error {
	if err := c.idle(); err != nil {
		return err
	}
	l, ok := effort.Parse(level)
	if !ok {
		return adapter.ErrNotFound
	}
	applied, err := c.rt.SetAgentThinkingLevel(ctx, c.agentName, l)
	if err != nil {
		if errors.Is(err, daruntime.ErrUnsupported) {
			return adapter.ErrUnsupported
		}
		return err
	}
	c.mu.Lock()
	c.thinking = applied.String()
	c.mu.Unlock()
	return nil
}

func (c *chat) Retitle(ctx context.Context, title string) error {
	if err := c.rt.UpdateSessionTitle(ctx, c.sess, title); err != nil {
		return err
	}
	meta := c.Meta()
	c.emit(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})
	return nil
}

// placeholderTitle is the title a brand-new session carries until either the
// user renames it or one is generated. It matches OpenChat's WithTitle and is
// treated as "no title yet", exactly as the CLI treats an empty title.
const placeholderTitle = "New chat"

// titleMessageWindow bounds how many recent user messages feed the generator,
// matching docker-agent's own session-title prompt (up to 2 messages).
const titleMessageWindow = 2

// maybeGenerateTitle mirrors the CLI/TUI behaviour: after the first prompt on a
// session that still carries the placeholder title, generate a concise title
// from the recent user messages with a one-shot LLM call. It runs in the
// background so it never blocks the turn, and on success persists the title and
// emits a session-meta event so the browser updates live.
func (c *chat) maybeGenerateTitle(ctx context.Context) {
	if strings.TrimSpace(c.sess.TitleSnapshot()) != placeholderTitle {
		return
	}
	gen := c.rt.TitleGenerator(ctx)
	if gen == nil {
		return
	}
	userMessages := c.recentUserMessages(titleMessageWindow)
	if len(userMessages) == 0 {
		return
	}

	// Detach from the run context: title generation must survive the turn
	// completing (or being aborted) and carries its own timeout inside the SDK.
	genCtx := context.WithoutCancel(ctx)
	go func() {
		defer func() { _ = recover() }()
		title, err := gen.Generate(genCtx, c.sess.ID, userMessages)
		if err != nil {
			c.a.log.Debug("generating session title", "session", c.sess.ID, "error", err)
			return
		}
		if strings.TrimSpace(title) == "" {
			return
		}
		// Don't clobber a title the user set (or one another run generated)
		// while this call was in flight.
		if strings.TrimSpace(c.sess.TitleSnapshot()) != placeholderTitle {
			return
		}
		if err := c.rt.UpdateSessionTitle(genCtx, c.sess, title); err != nil {
			c.a.log.Debug("persisting generated title", "session", c.sess.ID, "error", err)
			return
		}
		meta := c.Meta()
		c.emit(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})
	}()
}

// recentUserMessages returns up to n most-recent explicit user message texts,
// oldest-first, skipping implicit/system/tool messages. This is the same
// signal docker-agent feeds its title generator.
func (c *chat) recentUserMessages(n int) []string {
	items := c.sess.MessagesSnapshot()
	var msgs []string
	for _, it := range items {
		m := it.Message
		if m == nil || m.Implicit || m.Message.Role != dachat.MessageRoleUser {
			continue
		}
		if text := strings.TrimSpace(m.Message.Content); text != "" {
			msgs = append(msgs, text)
		}
	}
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	return msgs
}

func (c *chat) Compact(ctx context.Context) error {
	if err := c.idle(); err != nil {
		return err
	}
	go func() {
		sink := daruntime.EventSinkFunc(func(ev daruntime.Event) { c.normalize(ev) })
		c.rt.Summarize(context.WithoutCancel(ctx), c.sess, "", sink)
	}()
	return nil
}

func (c *chat) Stats(context.Context) protocol.Stats {
	in, out, cost := c.sess.TokensAndCost()
	msgs := 0
	toolCalls := 0
	for _, it := range c.sess.MessagesSnapshot() {
		if it.Message == nil {
			continue
		}
		msgs++
		toolCalls += len(it.Message.Message.ToolCalls)
	}
	c.mu.Lock()
	model := c.model
	c.mu.Unlock()
	return protocol.Stats{
		Usage:    protocol.Usage{InputTokens: in, OutputTokens: out, Cost: cost},
		Messages: msgs, ToolCalls: toolCalls, Model: model, AgentName: c.agentName,
		DurationSec: int64(c.sess.Duration().Seconds()),
	}
}

// Close disposes the chat: cancel pending dialogs, stop the run, close the
// runtime and stop the toolsets. The shared session store is NOT closed here.
func (c *chat) Close(ctx context.Context) error {
	c.Abort()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.generation++
	c.mu.Unlock()

	// Persist final metadata before tearing down — but never create a row for
	// a session that was opened and closed without a single prompt.
	c.mu.Lock()
	unsaved := c.unsaved
	c.mu.Unlock()
	if !unsaved {
		_ = c.a.store.UpdateSession(ctx, c.sess)
	}

	if err := c.rt.Close(); err != nil {
		c.a.log.Warn("closing runtime", "error", err)
	}
	if err := c.team.StopToolSets(context.WithoutCancel(ctx)); err != nil {
		c.a.log.Warn("stopping toolsets", "error", err)
	}
	close(c.events)
	return nil
}

var _ = permissions.Merge
