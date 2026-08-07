package dagent

import (
	"context"
	"fmt"
	"sync"

	daruntime "github.com/docker/docker-agent/pkg/runtime"
	"github.com/rumpl/daw/internal/protocol"
)

// observableQueue implements docker-agent's MessageQueue while retaining a
// browser-safe view of pending messages. Docker-agent owns when entries are
// consumed; the callback lets the chat publish that change immediately.
type observableQueue struct {
	mu       sync.Mutex
	name     protocol.DeliveryMode
	capacity int
	nextID   uint64
	entries  []observableQueueEntry
	onChange func()
}

type observableQueueEntry struct {
	id      string
	message daruntime.QueuedMessage
}

func newObservableQueue(name protocol.DeliveryMode, capacity int) *observableQueue {
	return &observableQueue{name: name, capacity: capacity}
}

func (q *observableQueue) setOnChange(fn func()) {
	q.mu.Lock()
	q.onChange = fn
	q.mu.Unlock()
}

func (q *observableQueue) Enqueue(ctx context.Context, message daruntime.QueuedMessage) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	q.mu.Lock()
	if len(q.entries) >= q.capacity {
		q.mu.Unlock()
		return false
	}
	q.nextID++
	q.entries = append(q.entries, observableQueueEntry{
		id:      fmt.Sprintf("%s-%d", q.name, q.nextID),
		message: message,
	})
	fn := q.onChange
	q.mu.Unlock()
	if fn != nil {
		fn()
	}
	return true
}

func (q *observableQueue) Dequeue(_ context.Context) (daruntime.QueuedMessage, bool) {
	q.mu.Lock()
	if len(q.entries) == 0 {
		q.mu.Unlock()
		return daruntime.QueuedMessage{}, false
	}
	entry := q.entries[0]
	q.entries[0] = observableQueueEntry{}
	q.entries = q.entries[1:]
	fn := q.onChange
	q.mu.Unlock()
	if fn != nil {
		fn()
	}
	return entry.message, true
}

func (q *observableQueue) Drain(_ context.Context) []daruntime.QueuedMessage {
	q.mu.Lock()
	if len(q.entries) == 0 {
		q.mu.Unlock()
		return nil
	}
	messages := make([]daruntime.QueuedMessage, len(q.entries))
	for i, entry := range q.entries {
		messages[i] = entry.message
	}
	clear(q.entries)
	q.entries = nil
	fn := q.onChange
	q.mu.Unlock()
	if fn != nil {
		fn()
	}
	return messages
}

func (q *observableQueue) clear() {
	q.mu.Lock()
	if len(q.entries) == 0 {
		q.mu.Unlock()
		return
	}
	clear(q.entries)
	q.entries = nil
	fn := q.onChange
	q.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (q *observableQueue) snapshot() ([]protocol.QueuedMessage, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	messages := make([]protocol.QueuedMessage, len(q.entries))
	for i, entry := range q.entries {
		messages[i] = protocol.QueuedMessage{ID: entry.id, Text: entry.message.Content}
	}
	return messages, q.capacity
}

var _ daruntime.MessageQueue = (*observableQueue)(nil)
