package dagent

import (
	"context"
	"strings"
	"testing"
	"time"

	dachat "github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/permissions"
	daruntime "github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/runtime/toolexec"
	"github.com/docker/docker-agent/pkg/safety"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tui/components/toolconfirm"
	"github.com/rumpl/daw/internal/protocol"
)

func TestSessionSummaryEmitsCompactionResult(t *testing.T) {
	sess := session.New()
	sess.AddMessage(session.NewAgentMessage("root", &dachat.Message{
		Role: dachat.MessageRoleUser, Content: "original prompt",
	}))
	sess.ApplyCompaction(0, 0, session.Item{Summary: "Work completed before compaction.", Cost: 0.0042})
	c := &chat{sess: sess, events: make(chan protocol.Event, 1)}

	c.normalize(daruntime.SessionSummary(sess.ID, "Work completed before compaction.", "root", 0, 0.0042, "", nil))

	ev := <-c.events
	if ev.Type != protocol.EventSummary || ev.Summary == nil {
		t.Fatalf("summary event was not normalized: %+v", ev)
	}
	if ev.Summary.Text != "Work completed before compaction." || ev.Summary.Cost != 0.0042 {
		t.Fatalf("compaction result was not preserved: %+v", ev.Summary)
	}
	if want := sess.ID + "-sum-1"; ev.Summary.ID != want {
		t.Fatalf("summary id = %q, want snapshot-compatible %q", ev.Summary.ID, want)
	}
}

func TestSnapshotExposesExactStoredMessageCost(t *testing.T) {
	sess := session.New()
	sess.AddMessage(session.NewAgentMessage("root", &dachat.Message{
		Role: dachat.MessageRoleAssistant, Content: "answer", Model: "provider/model", Cost: 0.012345,
		Usage: &dachat.Usage{
			InputTokens: 1234, OutputTokens: 56, CachedInputTokens: 900,
			CacheWriteTokens: 78, ReasoningTokens: 12,
		},
	}))
	c := &chat{sess: sess}

	items, _, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Message == nil {
		t.Fatalf("unexpected snapshot items: %+v", items)
	}
	if got := items[0].Message.Cost; got != 0.012345 {
		t.Fatalf("message cost = %v, want 0.012345", got)
	}
	if got := items[0].Message.InputTokens; got != 1234 {
		t.Fatalf("message input tokens = %d, want 1234", got)
	}
	if got := items[0].Message.OutputTokens; got != 56 {
		t.Fatalf("message output tokens = %d, want 56", got)
	}
	if got := items[0].Message.CachedInputTokens; got != 900 {
		t.Fatalf("message cached input tokens = %d, want 900", got)
	}
	if got := items[0].Message.CacheWriteTokens; got != 78 {
		t.Fatalf("message cache write tokens = %d, want 78", got)
	}
	if got := items[0].Message.ReasoningTokens; got != 12 {
		t.Fatalf("message reasoning tokens = %d, want 12", got)
	}
}

func TestSnapshotKeepsToolCallOnlyAssistantMessages(t *testing.T) {
	sess := session.New()
	sess.AddMessage(session.NewAgentMessage("root", &dachat.Message{
		Role: dachat.MessageRoleAssistant, Cost: 0.0042,
		Usage:     &dachat.Usage{InputTokens: 500, OutputTokens: 20},
		ToolCalls: []tools.ToolCall{{ID: "call-1", Function: tools.FunctionCall{Name: "shell"}}},
	}))
	c := &chat{sess: sess}

	items, _, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Message == nil || items[1].Tool == nil {
		t.Fatalf("tool-call-only assistant message was filtered: %+v", items)
	}
	if items[0].Message.Cost != 0.0042 || items[0].Message.InputTokens != 500 {
		t.Fatalf("tool-call message lost billing data: %+v", items[0].Message)
	}
}

// TestPatternFidelity locks the contract that the dashboard's confirmation
// dialog shows exactly the pattern the matched module would grant.
func TestPatternFidelity(t *testing.T) {
	call := tools.ToolCall{ID: "1", Function: tools.FunctionCall{
		Name: "shell", Arguments: `{"cmd":"ls -la /tmp"}`,
	}}
	pattern := toolconfirm.BuildPermissionPattern(call)
	if pattern == "" {
		t.Fatal("empty pattern")
	}
	label := toolconfirm.AlwaysAllowLabel(pattern)
	if !strings.Contains(label, "ls") {
		t.Fatalf("label %q must describe the pattern %q", label, pattern)
	}
	// Reconstructing the pattern anywhere else is forbidden; verify the
	// same call always yields the same string.
	if toolconfirm.BuildPermissionPattern(call) != pattern {
		t.Fatal("pattern construction is not deterministic")
	}
}

func TestRejectionReasonsComeFromTheMatchedModule(t *testing.T) {
	got := rejectionReasons()
	want := toolconfirm.RejectionReasons()
	if len(got) != len(want) {
		t.Fatalf("expected %d presets, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i].Label != want[i].Label || got[i].Reason != want[i].Value {
			t.Fatalf("preset %d diverged: %+v vs %+v", i, got[i], want[i])
		}
	}
}

func TestSummarizeArgsIsShortAndSingleLine(t *testing.T) {
	long := strings.Repeat("a", 5000)
	call := tools.ToolCall{Function: tools.FunctionCall{
		Name: "shell", Arguments: `{"cmd":"echo ` + long + `"}`,
	}}
	got := summarizeArgs(call)
	if len(got) > 320 {
		t.Fatalf("argument summary not bounded: %d", len(got))
	}
	if strings.Contains(got, "\n") {
		t.Fatal("argument summary must be single-line")
	}
}

func TestSummarizeArgsHandlesNonJSON(t *testing.T) {
	call := tools.ToolCall{Function: tools.FunctionCall{Name: "x", Arguments: "not json"}}
	if got := summarizeArgs(call); got != "not json" {
		t.Fatalf("got %q", got)
	}
}

func TestPartialToolCallIsEmittedAndArgumentDeltasAreMerged(t *testing.T) {
	c := &chat{
		sess:         session.New(),
		events:       make(chan protocol.Event, 4),
		partialTools: map[string]partialTool{},
	}
	def := tools.Tool{Name: "shell", Category: "shell"}
	c.normalize(&daruntime.PartialToolCallEvent{
		AgentContext: daruntime.AgentContext{AgentName: "root"},
		ToolCall: tools.ToolCall{ID: "t1", Function: tools.FunctionCall{
			Name: "shell", Arguments: `{"cmd":"echo `,
		}},
		ToolDefinition: &def,
	})

	first := <-c.events
	if first.Type != protocol.EventToolUpdate || first.Tool == nil {
		t.Fatalf("partial call did not immediately emit a tool update: %+v", first)
	}
	if first.Tool.ID != "t1" || first.Tool.Name != "shell" || first.Tool.State != protocol.ToolStatePending {
		t.Fatalf("bad first partial tool activity: %+v", first.Tool)
	}
	if first.Tool.Category != "shell" || first.Tool.DisplayName != "shell" {
		t.Fatalf("first-event tool definition was not retained: %+v", first.Tool)
	}

	// Current docker-agent versions send only the new argument bytes and omit
	// ToolDefinition after the first partial event.
	c.normalize(&daruntime.PartialToolCallEvent{
		AgentContext: daruntime.AgentContext{AgentName: "root"},
		ToolCall: tools.ToolCall{ID: "t1", Function: tools.FunctionCall{
			Name: "shell", Arguments: `hello"}`,
		}},
	})
	second := <-c.events
	if second.Tool == nil || second.Tool.ArgsSummary != "echo hello" {
		t.Fatalf("argument deltas were not merged: %+v", second.Tool)
	}
	if got := second.Tool.Arguments["cmd"]; got != "echo hello" {
		t.Fatalf("merged presentation arguments = %v, want echo hello", got)
	}
	if second.Tool.Category != "shell" || second.Tool.DisplayName != "shell" {
		t.Fatalf("tool definition was lost on a later delta: %+v", second.Tool)
	}

	complete := tools.ToolCall{ID: "t1", Function: tools.FunctionCall{
		Name: "shell", Arguments: `{"cmd":"echo hello"}`,
	}}
	c.normalize(daruntime.ToolCall(complete, def, "root"))
	started := <-c.events
	if started.Type != protocol.EventToolStart || started.Tool == nil || started.Tool.State != protocol.ToolStateRunning {
		t.Fatalf("complete call did not advance the pending item: %+v", started)
	}
	if len(c.partialTools) != 0 {
		t.Fatalf("completed partial call was not discarded: %+v", c.partialTools)
	}
}

func TestUserMessageIsEmittedOnlyFromRuntimeEvent(t *testing.T) {
	c := &chat{sess: session.New(), events: make(chan protocol.Event, 2)}
	at := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	c.normalize(&daruntime.UserMessageEvent{
		AgentContext:    daruntime.AgentContext{Timestamp: at},
		Message:         "change direction",
		SessionID:       c.sess.ID,
		SessionPosition: 3,
	})

	ev := <-c.events
	if ev.Type != protocol.EventMessageItem || ev.Message == nil {
		t.Fatalf("user event did not emit a message item: %+v", ev)
	}
	if ev.Message.ID != c.sess.ID+"-m-3" || ev.Message.Role != "user" || ev.Message.Text != "change direction" {
		t.Fatalf("bad user message item: %+v", ev.Message)
	}
	if ev.Message.CreatedAt != at.Format(time.RFC3339) {
		t.Fatalf("createdAt = %q, want %q", ev.Message.CreatedAt, at.Format(time.RFC3339))
	}
}

func TestUserMessageIgnoresEventsForOtherSessions(t *testing.T) {
	c := &chat{sess: session.New(), events: make(chan protocol.Event, 1)}
	c.normalize(&daruntime.UserMessageEvent{Message: "hidden", SessionID: "another-session"})

	select {
	case ev := <-c.events:
		t.Fatalf("foreign user event emitted a timeline item: %+v", ev)
	default:
	}
}

func TestMCPInitializationDoesNotClutterTimeline(t *testing.T) {
	c := &chat{sess: session.New(), events: make(chan protocol.Event, 2)}

	// The runtime initializes MCP toolsets on every turn. These lifecycle
	// events are expected housekeeping, not persistent conversation items.
	c.normalize(daruntime.MCPInitStarted("coder"))
	c.normalize(daruntime.MCPInitFinished("coder"))

	select {
	case ev := <-c.events:
		t.Fatalf("MCP initialization emitted a timeline event: %+v", ev)
	default:
	}
}

// TestToolsAreActuallyAutoApproved drives the matched module's real decision
// function and locks in the dashboard's only safety policy.
func TestToolsAreActuallyAutoApproved(t *testing.T) {
	sess := session.New()

	sess.SetSafetyPolicy(session.SafetyPolicyAutonomous)

	if !sess.IsToolsApproved() {
		t.Fatal("autonomous must report tools approved")
	}
	if perms := sess.ClonePermissions(); perms != nil && len(perms.Ask) > 0 {
		t.Fatalf("autonomous must not leave an ask rule behind: %+v", perms)
	}

	// The real decision function, with no custom rules, must allow.
	d := toolexec.Decide(sess.GetSafetyPolicy(), safety.Label{Class: safety.ClassUnknown},
		nil, "shell", map[string]any{"cmd": "rm -rf /tmp/x"})
	if d.Outcome != toolexec.OutcomeAllow {
		t.Fatalf("autonomous must auto-approve even an unknown-safety call, got outcome %v", d.Outcome)
	}

	// The old implementation's synthetic session-tier ask rule would have
	// defeated it; prove that is what happened so the fix stays in place.
	poisoned := []toolexec.NamedChecker{{
		Checker: permissions.NewCheckerFromRules(nil, []string{"*"}, nil),
		Source:  "session permissions",
		Tier:    toolexec.TierSession,
	}}
	if got := toolexec.Decide(session.SafetyPolicyAutonomous, safety.Label{Class: safety.ClassSafe},
		poisoned, "shell", nil); got.Outcome != toolexec.OutcomeAsk {
		t.Fatalf("expected a session-tier ask:[*] rule to force a prompt (the old bug), got %v", got.Outcome)
	}
}
