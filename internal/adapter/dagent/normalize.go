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
		c.emit(protocol.Event{
			Type:  protocol.EventAssistantDelta,
			Delta: &protocol.Delta{ItemID: id, Text: e.Content},
		})

	case *daruntime.AgentChoiceReasoningEvent:
		id := c.ensureAssistant(e.AgentName)
		c.emit(protocol.Event{
			Type:  protocol.EventReasoningDelta,
			Delta: &protocol.Delta{ItemID: id, Text: e.Content},
		})

	case *daruntime.UserMessageEvent:
		// Implicit/system-injected user messages are not shown.
		if e.Message == "" {
			return
		}

	case *daruntime.PartialToolCallEvent:
		// docker-agent emits this as soon as the model starts a tool call. Its
		// arguments are deltas (and the definition is normally present only on
		// the first event), so merge them before deriving browser presentation.
		c.closeAssistant()
		call, def := c.mergePartialToolCall(e)
		displayName, category := "", ""
		if def.Name != "" {
			displayName = def.DisplayName()
			category = def.Category
		}
		c.emit(protocol.Event{Type: protocol.EventToolUpdate, Tool: &protocol.ToolActivity{
			ID: call.ID, Name: call.Function.Name, DisplayName: displayName,
			Category: category, AgentName: e.AgentName, ArgsSummary: summarizeArgs(call),
			Arguments: presentationArgs(call), State: protocol.ToolStatePending,
		}})

	case *daruntime.ToolCallConfirmationEvent:
		c.forgetPartialToolCall(e.ToolCall.ID)
		// The pattern shown to the user is the pattern granted on approval.
		pattern := toolconfirm.BuildPermissionPattern(e.ToolCall)
		c.mu.Lock()
		c.pendingTools[e.ToolCall.ID] = pendingTool{call: e.ToolCall, pattern: pattern}
		c.mu.Unlock()
		c.emit(protocol.Event{Type: protocol.EventToolUpdate, Tool: &protocol.ToolActivity{
			ID: e.ToolCall.ID, Name: e.ToolCall.Function.Name,
			DisplayName: e.ToolDefinition.DisplayName(), Category: e.ToolDefinition.Category, AgentName: e.AgentName,
			ArgsSummary: summarizeArgs(e.ToolCall), Arguments: presentationArgs(e.ToolCall), State: protocol.ToolStateAwaiting,
		}})
		c.emit(protocol.Event{
			Type: protocol.EventToolConfirmation,
			Confirmation: &protocol.ToolConfirmationRequest{
				ToolCallID: e.ToolCall.ID, ToolName: e.ToolCall.Function.Name,
				DisplayName: e.ToolDefinition.DisplayName(), AgentName: e.AgentName, ArgsSummary: summarizeArgs(e.ToolCall),
				Pattern: pattern, PatternLabel: toolconfirm.AlwaysAllowLabel(pattern),
				Metadata: e.Metadata, RejectionReasons: rejectionReasons(),
			},
		})

	case *daruntime.ToolCallEvent:
		c.forgetPartialToolCall(e.ToolCall.ID)
		c.closeAssistant()
		c.emit(protocol.Event{Type: protocol.EventToolStart, Tool: &protocol.ToolActivity{
			ID: e.ToolCall.ID, Name: e.ToolCall.Function.Name,
			DisplayName: e.ToolDefinition.DisplayName(), Category: e.ToolDefinition.Category, AgentName: e.AgentName,
			ArgsSummary: summarizeArgs(e.ToolCall), Arguments: presentationArgs(e.ToolCall), State: protocol.ToolStateRunning,
		}})

	case *daruntime.ToolCallOutputEvent:
		c.emit(protocol.Event{Type: protocol.EventToolUpdate, Tool: &protocol.ToolActivity{
			ID: e.ToolCallID, Name: e.ToolDefinition.Name, DisplayName: e.ToolDefinition.DisplayName(),
			Category: e.ToolDefinition.Category, AgentName: e.AgentName, State: protocol.ToolStateRunning,
			Preview: e.Output, OutputBytes: len(e.Output),
		}})

	case *daruntime.ToolCallResponseEvent:
		c.forgetPartialToolCall(e.ToolCallID)
		state := protocol.ToolStateSuccess
		isErr := false
		if e.Result != nil && e.Result.IsError {
			state = protocol.ToolStateError
			isErr = true
		}
		c.emit(protocol.Event{Type: protocol.EventToolEnd, Tool: &protocol.ToolActivity{
			ID: e.ToolCallID, Name: e.ToolDefinition.Name, DisplayName: e.ToolDefinition.DisplayName(),
			Category: e.ToolDefinition.Category, AgentName: e.AgentName, State: state, Preview: e.Response,
			Images: toolResultImages(e.Result), OutputBytes: len(e.Response), IsError: isErr,
		}})

	case *daruntime.HookBlockedEvent:
		c.forgetPartialToolCall(e.ToolCall.ID)
		c.emit(protocol.Event{Type: protocol.EventToolEnd, Tool: &protocol.ToolActivity{
			ID: e.ToolCall.ID, Name: e.ToolCall.Function.Name, AgentName: e.AgentName,
			State: protocol.ToolStateRejected, Preview: e.Message, IsError: true,
		}})
		c.notice(protocol.NoticeWarning, "a hook blocked this tool call: "+e.Message, "hook_blocked")

	case *daruntime.ElicitationRequestEvent:
		c.mu.Lock()
		c.pendingElic[e.ElicitationID] = struct{}{}
		c.mu.Unlock()
		c.emit(protocol.Event{
			Type: protocol.EventElicitation,
			Elicitation: &protocol.ElicitationRequest{
				ElicitationID: e.ElicitationID, Message: e.Message, Mode: e.Mode,
				URL: e.URL, AgentName: e.AgentName, Schema: e.Schema,
			},
		})

	case *daruntime.AgentSwitchingEvent:
		c.closeAssistant()
		c.emit(protocol.Event{Type: protocol.EventTransfer, Transfer: &protocol.Transfer{
			ID:        fmt.Sprintf("%s-x-%s-%s-%d", c.sess.ID, e.FromAgent, e.ToAgent, time.Now().UnixNano()),
			FromAgent: e.FromAgent, ToAgent: e.ToAgent, Switching: e.Switching,
		}})

	case *daruntime.TokenUsageEvent:
		if e.Usage == nil {
			return
		}
		c.emit(protocol.Event{Type: protocol.EventUsage, Usage: &protocol.Usage{
			InputTokens: e.Usage.InputTokens, OutputTokens: e.Usage.OutputTokens,
			Cost: e.Usage.Cost, ContextLimit: e.Usage.ContextLimit,
		}})

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

	case *daruntime.MCPInitStartedEvent, *daruntime.MCPInitFinishedEvent:
		// MCP initialization is expected runtime housekeeping and occurs on
		// every turn. Keep it out of the persistent conversation timeline;
		// warnings and errors from initialization still arrive as their own
		// WarningEvent or ErrorEvent and remain visible.

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
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}})
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

// mergePartialToolCall accumulates docker-agent's argument deltas and retains
// the tool definition, which is normally sent only with the first delta.
func (c *chat) mergePartialToolCall(e *daruntime.PartialToolCallEvent) (tools.ToolCall, tools.Tool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.partialTools == nil {
		c.partialTools = map[string]partialTool{}
	}
	partial := c.partialTools[e.ToolCall.ID]
	if partial.call.ID == "" {
		partial.call.ID = e.ToolCall.ID
	}
	if e.ToolCall.Type != "" {
		partial.call.Type = e.ToolCall.Type
	}
	if e.ToolCall.Function.Name != "" {
		partial.call.Function.Name = e.ToolCall.Function.Name
	}
	partial.call.Function.Arguments += e.ToolCall.Function.Arguments
	if e.ToolDefinition != nil {
		partial.definition = *e.ToolDefinition
	}
	c.partialTools[e.ToolCall.ID] = partial
	return partial.call, partial.definition
}

func (c *chat) forgetPartialToolCall(id string) {
	c.mu.Lock()
	delete(c.partialTools, id)
	c.mu.Unlock()
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
