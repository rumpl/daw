package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rumpl/daw/internal/protocol"
	"github.com/rumpl/daw/internal/runnerapi"
)

func TestChatEventStreamReconnectsAfterUnexpectedEOF(t *testing.T) {
	var mu sync.Mutex
	connections := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chats/chat-1/snapshot" {
			_ = json.NewEncoder(w).Encode(runnerapi.SnapshotResponse{
				Run: protocol.RunStatus{State: protocol.RunStateIdle},
			})
			return
		}
		if r.URL.Path != "/v1/chats/chat-1/events" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		connections++
		connection := connections
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher := w.(http.Flusher)
		if connection == 1 {
			_ = json.NewEncoder(w).Encode(protocol.Event{
				Type: protocol.EventRunStatus,
				Run:  &protocol.RunStatus{State: protocol.RunStateRunning},
			})
			flusher.Flush()
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.Event{
			Type: protocol.EventRunStatus,
			Run:  &protocol.RunStatus{State: protocol.RunStateIdle},
		})
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	adapter, err := New(Config{Endpoint: server.URL, Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	chat := &chat{
		a: adapter, id: "chat-1", events: make(chan protocol.Event, 8), cancel: cancel,
		run: protocol.RunStatus{State: protocol.RunStateRunning},
	}
	go chat.stream(ctx)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case event, open := <-chat.events:
			if !open {
				t.Fatal("event stream closed instead of reconnecting")
			}
			if event.Type == protocol.EventRunStatus && event.Run != nil && event.Run.State == protocol.RunStateIdle {
				cancel()
				return
			}
		case <-deadline:
			t.Fatal("event stream did not reconnect and deliver the idle state")
		}
	}
}
