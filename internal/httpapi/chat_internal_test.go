package httpapi

import (
	"strings"
	"testing"

	"github.com/rumpl/daw/internal/protocol"
)

func testChat() *liveChat {
	c := newLiveChat("chat1", "ws1", nil)
	c.generation = 1
	return c
}

func TestReducerAppliesDeltasByStableID(t *testing.T) {
	c := testChat()
	c.publish(protocol.Event{Type: protocol.EventMessageItem,
		Message: &protocol.MessageItem{ID: "a1", Role: "assistant", Streaming: true}})
	c.publish(protocol.Event{Type: protocol.EventAssistantDelta,
		Delta: &protocol.Delta{ItemID: "a1", Text: "Hel"}})
	c.publish(protocol.Event{Type: protocol.EventAssistantDelta,
		Delta: &protocol.Delta{ItemID: "a1", Text: "lo"}})
	c.publish(protocol.Event{Type: protocol.EventReasoningDelta,
		Delta: &protocol.Delta{ItemID: "a1", Text: "think"}})
	c.publish(protocol.Event{Type: protocol.EventAssistantEnd,
		Ref: &protocol.ItemRef{ItemID: "a1"}})

	snap := c.snapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("expected one merged item, got %d", len(snap.Items))
	}
	m := snap.Items[0].Message
	if m.Text != "Hello" || m.Reasoning != "think" || m.Streaming {
		t.Fatalf("bad reduction: %+v", m)
	}
}

func TestReducerNeverDuplicatesByID(t *testing.T) {
	c := testChat()
	for range 3 {
		c.publish(protocol.Event{Type: protocol.EventToolStart,
			Tool: &protocol.ToolActivity{ID: "t1", Name: "shell", State: protocol.ToolStateRunning}})
	}
	c.publish(protocol.Event{Type: protocol.EventToolEnd,
		Tool: &protocol.ToolActivity{ID: "t1", Name: "shell", State: protocol.ToolStateSuccess}})
	snap := c.snapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("expected 1 tool item, got %d", len(snap.Items))
	}
	if snap.Items[0].Tool.State != protocol.ToolStateSuccess {
		t.Fatalf("last write must win: %+v", snap.Items[0].Tool)
	}
}

// TestToolUpdatesMergeInsteadOfReplacing: a streaming output update carries no
// arguments, so replacing the record would blank the command shown to the user.
func TestToolUpdatesMergeInsteadOfReplacing(t *testing.T) {
	c := testChat()
	c.publish(protocol.Event{Type: protocol.EventToolStart, Tool: &protocol.ToolActivity{
		ID: "t1", Name: "shell", Category: "shell", AgentName: "root",
		ArgsSummary: "ls -la /workspace", State: protocol.ToolStateRunning}})
	// An output chunk: only id, name, state and the chunk.
	c.publish(protocol.Event{Type: protocol.EventToolUpdate, Tool: &protocol.ToolActivity{
		ID: "t1", Name: "shell", State: protocol.ToolStateRunning, Preview: "partial"}})

	tool := c.snapshot().Items[0].Tool
	if tool.ArgsSummary != "ls -la /workspace" {
		t.Fatalf("argument summary lost on update: %q", tool.ArgsSummary)
	}
	if tool.Category != "shell" || tool.AgentName != "root" {
		t.Fatalf("metadata lost on update: %+v", tool)
	}

	c.publish(protocol.Event{Type: protocol.EventToolEnd, Tool: &protocol.ToolActivity{
		ID: "t1", Name: "shell", State: protocol.ToolStateSuccess, Preview: "final output",
		Images: []protocol.ToolImage{{Name: "result.png", MimeType: "image/png", Data: "aW1n"}}}})
	tool = c.snapshot().Items[0].Tool
	if tool.State != protocol.ToolStateSuccess || tool.Preview != "final output" {
		t.Fatalf("final state not applied: %+v", tool)
	}
	if len(tool.Images) != 1 || tool.Images[0].Name != "result.png" {
		t.Fatalf("tool images not merged: %+v", tool.Images)
	}
	if tool.ArgsSummary != "ls -la /workspace" {
		t.Fatalf("argument summary lost at tool end: %q", tool.ArgsSummary)
	}
}

func TestStaleEventsFromReplacedRuntimeAreDropped(t *testing.T) {
	c := testChat()
	src := make(chan protocol.Event, 4)
	done := make(chan struct{})
	go func() { c.pump(1, src); close(done) }()

	src <- protocol.Event{Type: protocol.EventMessageItem,
		Message: &protocol.MessageItem{ID: "m1", Role: "assistant", Text: "live"}}

	// Replace the runtime: bump the generation. Everything the old pump
	// delivers afterwards must be ignored.
	c.mu.Lock()
	c.generation = 2
	c.mu.Unlock()

	src <- protocol.Event{Type: protocol.EventMessageItem,
		Message: &protocol.MessageItem{ID: "m2", Role: "assistant", Text: "stale"}}
	close(src)
	<-done

	snap := c.snapshot()
	for _, it := range snap.Items {
		if it.Message != nil && it.Message.ID == "m2" {
			t.Fatal("a stale event from a replaced runtime reached the timeline")
		}
	}
}

func TestToolPreviewClamped(t *testing.T) {
	c := testChat()
	huge := strings.Repeat("x", previewLimit*4)
	c.publish(protocol.Event{Type: protocol.EventToolEnd,
		Tool: &protocol.ToolActivity{ID: "t1", Name: "shell", Preview: huge,
			State: protocol.ToolStateSuccess}})
	snap := c.snapshot()
	tool := snap.Items[0].Tool
	if len(tool.Preview) > previewLimit+128 {
		t.Fatalf("preview not clamped: %d bytes", len(tool.Preview))
	}
	if !tool.Truncated {
		t.Fatal("truncation must be reported to the user")
	}
}

func TestReplayWindow(t *testing.T) {
	c := testChat()
	for i := range 5 {
		c.publish(protocol.Event{Type: protocol.EventNotice,
			Notice: &protocol.Notice{ID: string(rune('a' + i)), Message: "n"}})
	}
	sub, replay, resumed := c.subscribe(2)
	defer c.unsubscribe(sub)
	if !resumed {
		t.Fatal("expected an in-buffer resume")
	}
	if len(replay) != 3 || replay[0].Seq != 3 {
		t.Fatalf("bad replay window: %d events starting at %d", len(replay), replay[0].Seq)
	}

	sub2, _, resumed2 := c.subscribe(999)
	defer c.unsubscribe(sub2)
	if resumed2 {
		t.Fatal("a resume point beyond the buffer must force a resnapshot")
	}
}

func TestItemsAreBounded(t *testing.T) {
	c := testChat()
	for i := range maxItems + 50 {
		c.publish(protocol.Event{Type: protocol.EventNotice,
			Notice: &protocol.Notice{ID: itoa(i), Message: "n"}})
	}
	if got := len(c.snapshot().Items); got > maxItems {
		t.Fatalf("timeline unbounded: %d items", got)
	}
}
