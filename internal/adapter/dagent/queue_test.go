package dagent

import (
	"testing"

	daruntime "github.com/docker/docker-agent/pkg/runtime"
	"github.com/rumpl/daw/internal/protocol"
)

func TestObservableQueuePublishesContentsAsRuntimeConsumesMessages(t *testing.T) {
	q := newObservableQueue(protocol.DeliveryFollowUp, 2)
	changes := 0
	q.setOnChange(func() { changes++ })

	if !q.Enqueue(t.Context(), daruntime.QueuedMessage{Content: "first"}) ||
		!q.Enqueue(t.Context(), daruntime.QueuedMessage{Content: "second"}) {
		t.Fatal("expected messages to be accepted")
	}
	if q.Enqueue(t.Context(), daruntime.QueuedMessage{Content: "full"}) {
		t.Fatal("expected a full queue to reject the message")
	}

	pending, capacity := q.snapshot()
	if capacity != 2 || len(pending) != 2 || pending[0].Text != "first" || pending[1].Text != "second" {
		t.Fatalf("unexpected snapshot: capacity=%d pending=%+v", capacity, pending)
	}
	if pending[0].ID == pending[1].ID {
		t.Fatal("queued message IDs must be unique")
	}

	message, ok := q.Dequeue(t.Context())
	if !ok || message.Content != "first" {
		t.Fatalf("unexpected dequeue: ok=%v message=%+v", ok, message)
	}
	pending, _ = q.snapshot()
	if len(pending) != 1 || pending[0].Text != "second" {
		t.Fatalf("unexpected snapshot after dequeue: %+v", pending)
	}

	drained := q.Drain(t.Context())
	if len(drained) != 1 || drained[0].Content != "second" {
		t.Fatalf("unexpected drain: %+v", drained)
	}
	if pending, _ := q.snapshot(); len(pending) != 0 {
		t.Fatalf("queue was not emptied: %+v", pending)
	}
	if changes != 4 {
		t.Fatalf("expected one notification per mutation, got %d", changes)
	}
}
