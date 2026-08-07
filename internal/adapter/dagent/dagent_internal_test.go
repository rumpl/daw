package dagent

import (
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/permissions"
	daruntime "github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/runtime/toolexec"
	"github.com/docker/docker-agent/pkg/safety"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tui/components/toolconfirm"

	"github.com/rumpl/daw/internal/protocol"
)

// TestPatternFidelity locks the contract that the dashboard's confirmation
// dialog shows exactly the pattern the matched module would grant.
func TestPatternFidelity(t *testing.T) {
	call := tools.ToolCall{ID: "1", Function: tools.FunctionCall{
		Name: "shell", Arguments: `{"cmd":"ls -la /tmp"}`}}
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
		Name: "shell", Arguments: `{"cmd":"echo ` + long + `"}`}}
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

// TestPostureMapsOntoRealSafetyModes locks the 1:1 mapping between the
// dashboard's postures and docker-agent's own session safety modes.
func TestPostureMapsOntoRealSafetyModes(t *testing.T) {
	cases := map[protocol.Posture]session.SafetyPolicy{
		protocol.PostureStrict:     session.SafetyPolicyStrict,
		protocol.PostureBalanced:   session.SafetyPolicyBalanced,
		protocol.PostureAutonomous: session.SafetyPolicyAutonomous,
	}
	for posture, want := range cases {
		got, ok := safetyPolicyFor(posture)
		if !ok || got != want {
			t.Fatalf("posture %q mapped to %q (ok=%v), want %q", posture, got, ok, want)
		}
		if back := postureFor(got); back != posture {
			t.Fatalf("round trip %q -> %q -> %q", posture, got, back)
		}
	}
	if _, ok := safetyPolicyFor("nonsense"); ok {
		t.Fatal("an unknown posture must be rejected, never silently accepted")
	}
	// Legacy stored values must be reported honestly, not as strict-by-accident.
	if got := postureFor("unsafe"); got != protocol.PostureAutonomous {
		t.Fatalf("legacy \"unsafe\" must normalize to autonomous, got %q", got)
	}
	if got := postureFor("safer"); got != protocol.PostureBalanced {
		t.Fatalf("legacy \"safer\" must normalize to balanced, got %q", got)
	}
}

// TestAutonomousActuallyAutoApproves is the regression test for the bug where
// auto-approve appeared to do nothing.
//
// Two things went wrong before: the dashboard set the legacy ToolsApproved
// flag (a no-op once the session had any explicit policy), and it installed a
// session-tier ask:["*"] rule, which toolexec.Decide honours unconditionally
// and which therefore outranked autonomous mode AND every "always allow"
// grant. This test drives the matched module's real decision function.
func TestAutonomousActuallyAutoApproves(t *testing.T) {
	sess := session.New()

	// What applyPosture does for autonomous.
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

// TestStrictStillPrompts guards the other direction: downgrading really does
// revoke a blanket approval.
func TestStrictStillPrompts(t *testing.T) {
	sess := session.New()
	sess.SetSafetyPolicy(session.SafetyPolicyAutonomous)
	sess.SetSafetyPolicy(session.SafetyPolicyStrict)

	if sess.IsToolsApproved() {
		t.Fatal("downgrading to strict must revoke the blanket approval")
	}
	d := toolexec.Decide(sess.GetSafetyPolicy(), safety.Label{Class: safety.ClassSafe}, nil, "read_file", nil)
	if d.Outcome != toolexec.OutcomeAsk {
		t.Fatalf("strict must prompt even for a safe call, got %v", d.Outcome)
	}
}

// TestBalancedAllowsSafeAsksUnknown documents the middle mode.
func TestBalancedAllowsSafeAsksUnknown(t *testing.T) {
	if d := toolexec.Decide(session.SafetyPolicyBalanced, safety.Label{Class: safety.ClassSafe},
		nil, "read_file", nil); d.Outcome != toolexec.OutcomeAllow {
		t.Fatalf("balanced must auto-approve a safe call, got %v", d.Outcome)
	}
	if d := toolexec.Decide(session.SafetyPolicyBalanced, safety.Label{Class: safety.ClassUnknown},
		nil, "shell", nil); d.Outcome != toolexec.OutcomeAsk {
		t.Fatalf("balanced must ask about an unknown call, got %v", d.Outcome)
	}
}

// TestGrantedPatternSurvivesUnderStrict proves the granted "always allow"
// pattern is actually honoured now: a session-tier ALLOW wins outright, which
// the old synthetic ask rule silently voided.
func TestGrantedPatternSurvivesUnderStrict(t *testing.T) {
	call := tools.ToolCall{ID: "1", Function: tools.FunctionCall{
		Name: "shell", Arguments: `{"cmd":"ls -la"}`}}
	pattern := toolconfirm.BuildPermissionPattern(call)

	granted := []toolexec.NamedChecker{{
		Checker: permissions.NewCheckerFromRules([]string{pattern}, nil, nil),
		Source:  "session permissions",
		Tier:    toolexec.TierSession,
	}}
	d := toolexec.Decide(session.SafetyPolicyStrict, safety.Label{Class: safety.ClassUnknown},
		granted, "shell", map[string]any{"cmd": "ls -la"})
	if d.Outcome != toolexec.OutcomeAllow {
		t.Fatalf("a granted pattern must silence the prompt even under strict, got %v", d.Outcome)
	}
}
