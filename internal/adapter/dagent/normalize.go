package dagent

import (
	"context"
	"fmt"
	"time"

	daruntime "github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tui/components/toolconfirm"

	"github.com/rumpl/daw/internal/protocol"
)

// normalize maps one matched-runtime event onto the browser protocol.
//
// Only event types the matched module actually exposes are handled; nothing is
// invented. Unhandled events are ignored rather than guessed at.
func (c *chat) normalize(ev daruntime.Event) {
	switch e := ev.(type) {

	case *daruntime.StreamStartedEvent:
		c.publishRun()

	case *daruntime.StreamStoppedEvent:
		// Advisory only: the authoritative end-of-turn is the channel close
		// handled in runLoop. Reasons other than "normal" become a notice.
		if e.Reason != "" && e.Reason != "normal" {
			c.notice(protocol.NoticeInfo, "run stopped: "+e.Reason, "stream_stopped")
		}

	case *daruntime.AgentChoiceEvent:
		id := c.ensureAssistant(e.AgentName)
		c.emit(protocol.Event{Type: protocol.EventAssistantDelta,
			Delta: &protocol.Delta{ItemID: id, Text: e.Content}})

	case *daruntime.AgentChoiceReasoningEvent:
		id := c.ensureAssistant(e.AgentName)
		c.emit(protocol.Event{Type: protocol.EventReasoningDelta,
			Delta: &protocol.Delta{ItemID: id, Text: e.Content}})

	case *daruntime.UserMessageEvent:
		// Implicit/system-injected user messages are not shown.
		if e.Message == "" {
			return
		}

	case *daruntime.ToolCallConfirmationEvent:
		// The pattern shown to the user is the pattern granted on approval.
		pattern := toolconfirm.BuildPermissionPattern(e.ToolCall)
		c.mu.Lock()
		c.pendingTools[e.ToolCall.ID] = pendingTool{call: e.ToolCall, pattern: pattern}
		c.mu.Unlock()
		c.emit(protocol.Event{Type: protocol.EventToolUpdate, Tool: &protocol.ToolActivity{
			ID: e.ToolCall.ID, Name: e.ToolCall.Function.Name,
			Category: e.ToolDefinition.Category, AgentName: e.AgentName,
			ArgsSummary: summarizeArgs(e.ToolCall), State: protocol.ToolStateAwaiting}})
		c.emit(protocol.Event{Type: protocol.EventToolConfirmation,
			Confirmation: &protocol.ToolConfirmationRequest{
				ToolCallID: e.ToolCall.ID, ToolName: e.ToolCall.Function.Name,
				AgentName: e.AgentName, ArgsSummary: summarizeArgs(e.ToolCall),
				Pattern: pattern, PatternLabel: toolconfirm.AlwaysAllowLabel(pattern),
				Metadata: e.Metadata, RejectionReasons: rejectionReasons(),
			}})

	case *daruntime.ToolCallEvent:
		c.closeAssistant()
		c.emit(protocol.Event{Type: protocol.EventToolStart, Tool: &protocol.ToolActivity{
			ID: e.ToolCall.ID, Name: e.ToolCall.Function.Name,
			Category: e.ToolDefinition.Category, AgentName: e.AgentName,
			ArgsSummary: summarizeArgs(e.ToolCall), State: protocol.ToolStateRunning}})

	case *daruntime.ToolCallOutputEvent:
		c.emit(protocol.Event{Type: protocol.EventToolUpdate, Tool: &protocol.ToolActivity{
			ID: e.ToolCallID, Name: e.ToolDefinition.Name, Category: e.ToolDefinition.Category,
			AgentName: e.AgentName, State: protocol.ToolStateRunning,
			Preview: e.Output, OutputBytes: len(e.Output)}})

	case *daruntime.ToolCallResponseEvent:
		state := protocol.ToolStateSuccess
		isErr := false
		if e.Result != nil && e.Result.IsError {
			state = protocol.ToolStateError
			isErr = true
		}
		c.emit(protocol.Event{Type: protocol.EventToolEnd, Tool: &protocol.ToolActivity{
			ID: e.ToolCallID, Name: e.ToolDefinition.Name, Category: e.ToolDefinition.Category,
			AgentName: e.AgentName, State: state, Preview: e.Response,
			OutputBytes: len(e.Response), IsError: isErr}})

	case *daruntime.HookBlockedEvent:
		c.emit(protocol.Event{Type: protocol.EventToolEnd, Tool: &protocol.ToolActivity{
			ID: e.ToolCall.ID, Name: e.ToolCall.Function.Name, AgentName: e.AgentName,
			State: protocol.ToolStateRejected, Preview: e.Message, IsError: true}})
		c.notice(protocol.NoticeWarning, "a hook blocked this tool call: "+e.Message, "hook_blocked")

	case *daruntime.ElicitationRequestEvent:
		c.mu.Lock()
		c.pendingElic[e.ElicitationID] = struct{}{}
		c.mu.Unlock()
		c.emit(protocol.Event{Type: protocol.EventElicitation,
			Elicitation: &protocol.ElicitationRequest{
				ElicitationID: e.ElicitationID, Message: e.Message, Mode: e.Mode,
				URL: e.URL, AgentName: e.AgentName, Schema: e.Schema}})

	case *daruntime.AgentSwitchingEvent:
		c.closeAssistant()
		c.emit(protocol.Event{Type: protocol.EventTransfer, Transfer: &protocol.Transfer{
			ID:        fmt.Sprintf("%s-x-%s-%s-%d", c.sess.ID, e.FromAgent, e.ToAgent, time.Now().UnixNano()),
			FromAgent: e.FromAgent, ToAgent: e.ToAgent, Switching: e.Switching}})

	case *daruntime.TokenUsageEvent:
		if e.Usage == nil {
			return
		}
		c.emit(protocol.Event{Type: protocol.EventUsage, Usage: &protocol.Usage{
			InputTokens: e.Usage.InputTokens, OutputTokens: e.Usage.OutputTokens,
			Cost: e.Usage.Cost, ContextLimit: e.Usage.ContextLimit}})

	case *daruntime.SessionCompactionEvent:
		msg := "Context compaction " + e.Status
		if e.Outcome != "" {
			msg += " (" + e.Outcome + ")"
		}
		c.notice(protocol.NoticeInfo, msg, "compaction")

	case *daruntime.SessionSummaryEvent:
		c.notice(protocol.NoticeInfo, "History compacted into a summary.", "compaction")

	case *daruntime.ModelFallbackEvent:
		c.notice(protocol.NoticeWarning, fmt.Sprintf(
			"Model %s failed (%s); retrying with %s (attempt %d/%d).",
			e.FailedModel, e.Reason, e.FallbackModel, e.Attempt, e.MaxAttempts), "retry")

	case *daruntime.MaxIterationsReachedEvent:
		// The runtime blocks here waiting for a Resume. There is no user
		// affordance for "keep looping" in this dashboard, so the documented
		// safe fallback is applied and shown as a notice instead of hanging.
		c.notice(protocol.NoticeWarning, fmt.Sprintf(
			"The agent reached its %d-iteration limit and was stopped.", e.MaxIterations),
			"max_iterations")
		c.rt.Resume(context.Background(), daruntime.ResumeReject("iteration limit reached"))

	case *daruntime.ErrorEvent:
		c.notice(protocol.NoticeError, e.Error, e.Code)

	case *daruntime.WarningEvent:
		c.notice(protocol.NoticeWarning, e.Message, "warning")

	case *daruntime.ToolsetInfoEvent:
		if !e.Loading {
			return
		}

	case *daruntime.MCPInitStartedEvent:
		c.notice(protocol.NoticeInfo, "Starting MCP toolsets…", "mcp_init")

	case *daruntime.MCPInitFinishedEvent:
		// no notice: success is the expected path

	case *daruntime.SessionTitleEvent:
		meta := c.Meta()
		meta.Title = e.Title
		c.emit(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})

	case *daruntime.AgentInfoEvent:
		c.mu.Lock()
		if e.Model != "" {
			c.model = e.Model
		}
		c.mu.Unlock()
		meta := c.Meta()
		c.emit(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})

	case *daruntime.TeamInfoEvent:
		for _, d := range e.AvailableAgents {
			if d.Name != c.agentName {
				continue
			}
			c.mu.Lock()
			if d.Model != "" {
				c.model = d.Provider + "/" + d.Model
			}
			if d.Thinking != "" {
				c.thinking = d.Thinking
			}
			c.mu.Unlock()
		}
		meta := c.Meta()
		c.emit(protocol.Event{Type: protocol.EventSessionMeta, Meta: &meta})

	case *daruntime.BudgetExceededEvent:
		c.notice(protocol.NoticeWarning, "A configured budget was exceeded; the run was stopped.", "budget")
	}
}

// ensureAssistant returns the id of the assistant item currently streaming,
// opening a new one when a turn or tool boundary closed the previous one.
func (c *chat) ensureAssistant(agentName string) string {
	c.mu.Lock()
	if c.curAssistant != "" {
		id := c.curAssistant
		c.mu.Unlock()
		return id
	}
	c.assistantSeq++
	id := fmt.Sprintf("%s-a-%d", c.sess.ID, c.assistantSeq)
	c.curAssistant = id
	model := c.model
	c.mu.Unlock()

	c.emit(protocol.Event{Type: protocol.EventMessageItem, Message: &protocol.MessageItem{
		ID: id, Role: "assistant", AgentName: agentName, Streaming: true, Model: model,
		CreatedAt: time.Now().UTC().Format(time.RFC3339)}})
	return id
}

func (c *chat) closeAssistant() {
	c.mu.Lock()
	id := c.curAssistant
	c.curAssistant = ""
	c.mu.Unlock()
	if id != "" {
		c.emit(protocol.Event{Type: protocol.EventAssistantEnd, Ref: &protocol.ItemRef{ItemID: id}})
	}
}

// rejectionReasons exposes the matched module's own presets rather than
// inventing dashboard-specific wording.
func rejectionReasons() []protocol.RejectionReason {
	presets := toolconfirm.RejectionReasons()
	out := make([]protocol.RejectionReason, 0, len(presets))
	for _, r := range presets {
		out = append(out, protocol.RejectionReason{Label: r.Label, Reason: r.Value})
	}
	return out
}

var _ = tools.ElicitationActionAccept
