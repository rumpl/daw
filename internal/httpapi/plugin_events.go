package httpapi

import (
	"sync"

	"github.com/rumpl/daw/internal/protocol"
)

const pluginEventLogCapacity = 256

type pluginEventSubscriber struct{ ch chan protocol.PluginEvent }

type pluginEventStream struct {
	seq  uint64
	buf  []protocol.PluginEvent
	subs map[*pluginEventSubscriber]struct{}
}

type pluginEventHub struct {
	mu      sync.Mutex
	streams map[string]*pluginEventStream
	closed  bool
}

func newPluginEventHub() *pluginEventHub {
	return &pluginEventHub{streams: map[string]*pluginEventStream{}}
}

func (h *pluginEventHub) stream(pluginID string) *pluginEventStream {
	stream := h.streams[pluginID]
	if stream == nil {
		stream = &pluginEventStream{subs: map[*pluginEventSubscriber]struct{}{}}
		h.streams[pluginID] = stream
	}
	return stream
}

func (h *pluginEventHub) publish(pluginID, eventType string, data any) protocol.PluginEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return protocol.PluginEvent{}
	}
	stream := h.stream(pluginID)
	stream.seq++
	event := protocol.PluginEvent{Type: eventType, Seq: stream.seq, Data: data}
	stream.buf = append(stream.buf, event)
	if len(stream.buf) > pluginEventLogCapacity {
		stream.buf = append([]protocol.PluginEvent(nil), stream.buf[len(stream.buf)-pluginEventLogCapacity:]...)
	}
	for subscriber := range stream.subs {
		select {
		case subscriber.ch <- event:
		default:
			delete(stream.subs, subscriber)
			close(subscriber.ch)
		}
	}
	return event
}

func (h *pluginEventHub) subscribe(pluginID string, lastID uint64) (*pluginEventSubscriber, []protocol.PluginEvent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	subscriber := &pluginEventSubscriber{ch: make(chan protocol.PluginEvent, 64)}
	if h.closed {
		close(subscriber.ch)
		return subscriber, nil, false
	}
	stream := h.stream(pluginID)
	stream.subs[subscriber] = struct{}{}
	if lastID == 0 {
		return subscriber, nil, true
	}
	if len(stream.buf) == 0 || stream.buf[0].Seq > lastID+1 || lastID > stream.seq {
		return subscriber, nil, false
	}
	var replay []protocol.PluginEvent
	for _, event := range stream.buf {
		if event.Seq > lastID {
			replay = append(replay, event)
		}
	}
	return subscriber, replay, true
}

func (h *pluginEventHub) unsubscribe(pluginID string, subscriber *pluginEventSubscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	stream := h.streams[pluginID]
	if stream == nil {
		return
	}
	if _, ok := stream.subs[subscriber]; ok {
		delete(stream.subs, subscriber)
		close(subscriber.ch)
	}
}

func (h *pluginEventHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for _, stream := range h.streams {
		for subscriber := range stream.subs {
			close(subscriber.ch)
		}
	}
	h.streams = map[string]*pluginEventStream{}
}
