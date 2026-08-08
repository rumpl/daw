package httpapi

import (
	"context"
	"slices"
	"sync"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/protocol"
)

// eventLogCapacity bounds the replay buffer, mirroring upstream
// pkg/server/eventlog.go's defaultEventLogCapacity.
const eventLogCapacity = 1024

// previewLimit bounds any single tool preview pushed to the browser. Shell and
// filesystem tools routinely return megabytes; the browser never sees them.
const previewLimit = 4096

// maxItems bounds the in-memory timeline of one chat so a very long session
// cannot grow the snapshot without limit.
const maxItems = 2000

type subscriber struct {
	ch chan protocol.Event
}

// liveChat owns one adapter.Chat, its normalized timeline, its sequenced
// event log and its SSE subscribers. Exactly one liveChat may exist per
// docker-agent session inside this process.
type liveChat struct {
	id            string
	workspaceID   string
	chat          adapter.Chat
	onIndexChange func(sessionID, workspaceID, reason string)

	mu          sync.Mutex
	seq         uint64
	buf         []protocol.Event
	subs        map[*subscriber]struct{}
	items       []protocol.Item
	index       map[string]int
	meta        protocol.SessionMeta
	run         protocol.RunStatus
	usage       protocol.Usage
	pendingC    map[string]protocol.ToolConfirmationRequest
	pendingE    map[string]protocol.ElicitationRequest
	attachments map[string]uploadedAttachment
	// generation invalidates events from a runtime we already replaced or
	// closed. The pump goroutine carries its generation and drops everything
	// once it no longer matches.
	generation uint64
	closed     bool
}

type uploadedAttachment struct {
	meta protocol.Attachment
	data []byte
}

func newLiveChat(id, workspaceID string, c adapter.Chat) *liveChat {
	return &liveChat{
		id: id, workspaceID: workspaceID, chat: c,
		subs:        map[*subscriber]struct{}{},
		index:       map[string]int{},
		pendingC:    map[string]protocol.ToolConfirmationRequest{},
		pendingE:    map[string]protocol.ElicitationRequest{},
		attachments: map[string]uploadedAttachment{},
		run:         protocol.RunStatus{State: protocol.RunStateIdle},
	}
}

// hydrate rebuilds the timeline from docker-agent's own session data. Called
// on open/resume and never in the middle of a live run.
func (l *liveChat) hydrate(ctx context.Context) error {
	items, usage, err := l.chat.Snapshot(ctx)
	if err != nil {
		return err
	}
	meta := l.chat.Meta()
	meta.ChatID = l.id
	meta.WorkspaceID = l.workspaceID

	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = nil
	l.index = map[string]int{}
	for _, it := range items {
		l.upsertLocked(it)
	}
	l.usage = usage
	l.meta = meta
	return nil
}

func itemID(it protocol.Item) string {
	switch it.Kind {
	case protocol.ItemKindMessage:
		if it.Message != nil {
			return "m:" + it.Message.ID
		}
	case protocol.ItemKindTool:
		if it.Tool != nil {
			return "t:" + it.Tool.ID
		}
	case protocol.ItemKindTransfer:
		if it.Transfer != nil {
			return "x:" + it.Transfer.ID
		}
	case protocol.ItemKindNotice:
		if it.Notice != nil {
			return "n:" + it.Notice.ID
		}
	case protocol.ItemKindSummary:
		if it.Summary != nil {
			return "s:" + it.Summary.ID
		}
	}
	return ""
}

// upsertLocked reconciles by stable docker-agent IDs; it never deduplicates by
// text or timestamp. This is what makes resnapshot-after-reconnect idempotent.
func (l *liveChat) upsertLocked(it protocol.Item) {
	id := itemID(it)
	if id == "" {
		return
	}
	if pos, ok := l.index[id]; ok {
		l.items[pos] = it
		return
	}
	if len(l.items) >= maxItems {
		// Drop the oldest and reindex.
		l.items = l.items[1:]
		l.index = map[string]int{}
		for i, existing := range l.items {
			l.index[itemID(existing)] = i
		}
	}
	l.index[id] = len(l.items)
	l.items = append(l.items, it)
}

func clampPreview(s string) (string, bool) {
	if len(s) <= previewLimit {
		return s, false
	}
	return s[:previewLimit] + "\n… output truncated (" + itoa(len(s)-previewLimit) + " more bytes)", true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// pump consumes the adapter's normalized stream until it closes.
func (l *liveChat) pump(gen uint64, src <-chan protocol.Event) {
	for ev := range src {
		l.mu.Lock()
		stale := l.generation != gen || l.closed
		l.mu.Unlock()
		if stale {
			continue // stale event from a replaced or closed runtime
		}
		l.publish(ev)
	}
}

// publish applies one adapter event to the timeline and fans it out with a
// fresh sequence number.
func (l *liveChat) publish(ev protocol.Event) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.applyLocked(&ev)
	l.seq++
	ev.Seq = l.seq
	l.buf = append(l.buf, ev)
	if len(l.buf) > eventLogCapacity {
		copy(l.buf, l.buf[len(l.buf)-eventLogCapacity:])
		l.buf = l.buf[:eventLogCapacity]
	}
	for s := range l.subs {
		select {
		case s.ch <- ev:
		default:
			// Slow client: drop it. It reconnects with Last-Event-ID and
			// replays, or resnapshots if the buffer moved past it.
			delete(l.subs, s)
			close(s.ch)
		}
	}
	l.mu.Unlock()
	if l.onIndexChange != nil {
		switch ev.Type {
		case protocol.EventRunStatus:
			l.onIndexChange(l.chat.SessionID(), l.workspaceID, "run_state")
		case protocol.EventSessionMeta:
			l.onIndexChange(l.chat.SessionID(), l.workspaceID, "metadata")
		case protocol.EventMessageItem:
			l.onIndexChange(l.chat.SessionID(), l.workspaceID, "messages")
		}
	}
}

// applyLocked is the server-side reducer. The browser runs the same reduction
// for live deltas, but the server keeps the authoritative copy so any
// reconnect can be answered with a complete snapshot.
func (l *liveChat) applyLocked(ev *protocol.Event) {
	switch ev.Type {
	case protocol.EventMessageItem:
		if ev.Message != nil {
			m := *ev.Message
			l.upsertLocked(protocol.Item{Kind: protocol.ItemKindMessage, Message: &m})
		}
	case protocol.EventAssistantDelta, protocol.EventReasoningDelta:
		if ev.Delta == nil {
			return
		}
		if pos, ok := l.index["m:"+ev.Delta.ItemID]; ok && l.items[pos].Message != nil {
			m := *l.items[pos].Message
			if ev.Type == protocol.EventAssistantDelta {
				m.Text += ev.Delta.Text
			} else {
				m.Reasoning += ev.Delta.Text
			}
			m.Streaming = true
			l.items[pos].Message = &m
		}
	case protocol.EventAssistantEnd:
		if ev.Ref == nil {
			return
		}
		if pos, ok := l.index["m:"+ev.Ref.ItemID]; ok && l.items[pos].Message != nil {
			m := *l.items[pos].Message
			m.Streaming = false
			l.items[pos].Message = &m
		}
	case protocol.EventToolStart, protocol.EventToolUpdate, protocol.EventToolEnd:
		if ev.Tool == nil {
			return
		}
		// Merge rather than replace: a streaming output update carries only
		// the ID, state and the latest chunk, so replacing would drop the
		// argument summary (and category) captured at tool_start.
		t := l.mergeToolLocked(*ev.Tool)
		preview, truncated := clampPreview(t.Preview)
		t.Preview = preview
		t.Truncated = t.Truncated || truncated
		ev.Tool = &t
		tc := t
		l.upsertLocked(protocol.Item{Kind: protocol.ItemKindTool, Tool: &tc})
	case protocol.EventTransfer:
		if ev.Transfer != nil {
			t := *ev.Transfer
			l.upsertLocked(protocol.Item{Kind: protocol.ItemKindTransfer, Transfer: &t})
		}
	case protocol.EventNotice:
		if ev.Notice != nil {
			n := *ev.Notice
			l.upsertLocked(protocol.Item{Kind: protocol.ItemKindNotice, Notice: &n})
		}
	case protocol.EventRunStatus:
		if ev.Run != nil {
			l.run = *ev.Run
		}
	case protocol.EventUsage:
		if ev.Usage != nil {
			if ev.Usage.LastMessage != nil {
				l.applyLastMessageUsageLocked(*ev.Usage.LastMessage)
			}
			l.usage = *ev.Usage
			l.usage.LastMessage = nil
		}
	case protocol.EventSessionMeta:
		if ev.Meta != nil {
			m := *ev.Meta
			m.ChatID = l.id
			m.WorkspaceID = l.workspaceID
			l.meta = m
			ev.Meta = &m
		}
	case protocol.EventToolConfirmation:
		if ev.Confirmation != nil {
			l.pendingC[ev.Confirmation.ToolCallID] = *ev.Confirmation
		}
	case protocol.EventToolResolved:
		if ev.ToolResolved != nil {
			delete(l.pendingC, ev.ToolResolved.ToolCallID)
		}
	case protocol.EventElicitation:
		if ev.Elicitation != nil {
			l.pendingE[ev.Elicitation.ElicitationID] = *ev.Elicitation
		}
	case protocol.EventElicitResolved:
		if ev.ElicitResolved != nil {
			delete(l.pendingE, ev.ElicitResolved.ElicitationID)
		}
	}
}

// applyLastMessageUsageLocked attaches docker-agent's live per-invocation
// accounting to the most recent assistant item. Stored history already carries
// these fields directly; this bridges the live event stream until rehydration.
func (l *liveChat) applyLastMessageUsageLocked(usage protocol.MessageUsage) {
	for i, item := range slices.Backward(l.items) {
		if item.Kind != protocol.ItemKindMessage || item.Message == nil || item.Message.Role != "assistant" {
			continue
		}
		m := *item.Message
		m.Cost = usage.Cost
		m.InputTokens = usage.InputTokens
		m.OutputTokens = usage.OutputTokens
		m.CachedInputTokens = usage.CachedInputTokens
		m.CacheWriteTokens = usage.CacheWriteTokens
		m.ReasoningTokens = usage.ReasoningTokens
		if usage.Model != "" {
			m.Model = usage.Model
		}
		l.items[i].Message = &m
		return
	}
}

// mergeToolLocked folds an incoming tool event onto what is already known
// about that tool call. Empty fields in the incoming event mean "unchanged",
// never "cleared".
func (l *liveChat) mergeToolLocked(in protocol.ToolActivity) protocol.ToolActivity {
	pos, ok := l.index["t:"+in.ID]
	if !ok || l.items[pos].Tool == nil {
		return in
	}
	out := *l.items[pos].Tool
	out.State = in.State
	if in.Name != "" {
		out.Name = in.Name
	}
	if in.DisplayName != "" {
		out.DisplayName = in.DisplayName
	}
	if in.Category != "" {
		out.Category = in.Category
	}
	if in.AgentName != "" {
		out.AgentName = in.AgentName
	}
	if in.ArgsSummary != "" {
		out.ArgsSummary = in.ArgsSummary
	}
	if in.Arguments != nil {
		out.Arguments = in.Arguments
	}
	if in.Images != nil {
		out.Images = in.Images
	}
	if in.Preview != "" {
		out.Preview = in.Preview
		out.OutputBytes = in.OutputBytes
	}
	out.IsError = in.IsError
	out.Truncated = out.Truncated || in.Truncated
	return out
}

// runState returns the lightweight status used by the cross-project session
// index without copying the chat's potentially large timeline.
func (l *liveChat) runState() protocol.RunState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.run.State
}

// snapshot returns the authoritative in-memory state.
func (l *liveChat) snapshot() protocol.Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	items := make([]protocol.Item, len(l.items))
	copy(items, l.items)
	confirmations := make([]protocol.ToolConfirmationRequest, 0, len(l.pendingC))
	for _, c := range l.pendingC {
		confirmations = append(confirmations, c)
	}
	elicitations := make([]protocol.ElicitationRequest, 0, len(l.pendingE))
	for _, e := range l.pendingE {
		elicitations = append(elicitations, e)
	}
	return protocol.Snapshot{
		Seq: l.seq, Meta: l.meta, Items: items, Run: l.run, Usage: l.usage,
		PendingConfirmations: confirmations, PendingElicitations: elicitations,
	}
}

// subscribe registers an SSE listener. When lastEventID is non-zero and still
// inside the ring buffer, buffered events are replayed; otherwise the caller
// is told to resnapshot.
func (l *liveChat) subscribe(lastEventID uint64) (*subscriber, []protocol.Event, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := &subscriber{ch: make(chan protocol.Event, 256)}
	if l.closed {
		close(s.ch)
		return s, nil, false
	}
	l.subs[s] = struct{}{}
	if lastEventID == 0 {
		return s, nil, false
	}
	if len(l.buf) == 0 || l.buf[0].Seq > lastEventID+1 || lastEventID > l.seq {
		// Either the resume point was evicted from the ring buffer, or the
		// client claims to have seen more than we ever sent (a different or
		// restarted chat). Both cases require a resnapshot.
		return s, nil, false
	}
	var replay []protocol.Event
	for _, e := range l.buf {
		if e.Seq > lastEventID {
			replay = append(replay, e)
		}
	}
	return s, replay, true
}

func (l *liveChat) unsubscribe(s *subscriber) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.subs[s]; ok {
		delete(l.subs, s)
		close(s.ch)
	}
}

func (l *liveChat) takeAttachments(ids []string) ([]adapter.Attachment, bool) {
	if len(ids) == 0 {
		return nil, true
	}
	if len(ids) > maxAttachmentCount {
		return nil, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := map[string]bool{}
	out := make([]adapter.Attachment, 0, len(ids))
	for _, id := range ids {
		upload, ok := l.attachments[id]
		if !ok || seen[id] {
			return nil, false
		}
		seen[id] = true
		out = append(out, adapter.Attachment{Name: upload.meta.Name, MimeType: upload.meta.MimeType, Data: append([]byte(nil), upload.data...)})
	}
	for _, id := range ids {
		delete(l.attachments, id)
	}
	return out, true
}

// close disposes the chat: cancels pending dialogs via the adapter, marks the
// generation stale, emits a terminal event and disconnects subscribers.
func (l *liveChat) close(ctx context.Context, reason string) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.generation++
	l.mu.Unlock()

	_ = l.chat.Close(ctx)

	l.mu.Lock()
	l.seq++
	ev := protocol.Event{
		Type: protocol.EventChatClosed, Seq: l.seq,
		Closed: &protocol.ChatClosed{Reason: reason},
	}
	l.buf = append(l.buf, ev)
	l.closed = true
	subs := make([]*subscriber, 0, len(l.subs))
	for s := range l.subs {
		subs = append(subs, s)
	}
	l.subs = map[*subscriber]struct{}{}
	l.pendingC = map[string]protocol.ToolConfirmationRequest{}
	l.pendingE = map[string]protocol.ElicitationRequest{}
	l.attachments = map[string]uploadedAttachment{}
	l.mu.Unlock()

	for _, s := range subs {
		select {
		case s.ch <- ev:
		default:
		}
		close(s.ch)
	}
}
