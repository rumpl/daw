package fake

import (
	"sync"
	"testing"

	"github.com/rumpl/daw/internal/protocol"
)

func TestCloseWhileEmittingEvents(t *testing.T) {
	for range 100 {
		c := &chat{
			events:  make(chan protocol.Event, 1),
			pending: map[string]chan reply{},
		}
		var emitters sync.WaitGroup
		for range 8 {
			emitters.Go(func() {
				for range 100 {
					c.emit(protocol.Event{Type: protocol.EventRunStatus})
				}
			})
		}

		if err := c.Close(t.Context()); err != nil {
			t.Fatalf("close: %v", err)
		}
		emitters.Wait()
	}
}
