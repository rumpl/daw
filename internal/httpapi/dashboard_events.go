package httpapi

import (
	"sync"

	"github.com/rumpl/daw/internal/protocol"
)

const dashboardEventLogCapacity = 256

type dashboardSubscriber struct {
	ch chan protocol.DashboardEvent
}

type dashboardEvents struct {
	mu     sync.Mutex
	seq    uint64
	buf    []protocol.DashboardEvent
	subs   map[*dashboardSubscriber]struct{}
	closed bool
}

func newDashboardEvents() *dashboardEvents {
	return &dashboardEvents{subs: map[*dashboardSubscriber]struct{}{}}
}

func (d *dashboardEvents) publish(ev protocol.DashboardEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.seq++
	ev.Seq = d.seq
	d.buf = append(d.buf, ev)
	if len(d.buf) > dashboardEventLogCapacity {
		d.buf = append([]protocol.DashboardEvent(nil), d.buf[len(d.buf)-dashboardEventLogCapacity:]...)
	}
	for sub := range d.subs {
		select {
		case sub.ch <- ev:
		default:
			delete(d.subs, sub)
			close(sub.ch)
		}
	}
}

func (d *dashboardEvents) subscribe(lastID uint64) (*dashboardSubscriber, []protocol.DashboardEvent, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	sub := &dashboardSubscriber{ch: make(chan protocol.DashboardEvent, 64)}
	if d.closed {
		close(sub.ch)
		return sub, nil, false
	}
	d.subs[sub] = struct{}{}
	if lastID == 0 {
		return sub, nil, false
	}
	if len(d.buf) == 0 || d.buf[0].Seq > lastID+1 || lastID > d.seq {
		return sub, nil, false
	}
	var replay []protocol.DashboardEvent
	for _, ev := range d.buf {
		if ev.Seq > lastID {
			replay = append(replay, ev)
		}
	}
	return sub, replay, true
}

func (d *dashboardEvents) snapshot() protocol.DashboardEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return protocol.DashboardEvent{Type: protocol.DashboardEventSnapshot, Seq: d.seq}
}

func (d *dashboardEvents) unsubscribe(sub *dashboardSubscriber) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.subs[sub]; ok {
		delete(d.subs, sub)
		close(sub.ch)
	}
}

func (d *dashboardEvents) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	for sub := range d.subs {
		close(sub.ch)
	}
	d.subs = map[*dashboardSubscriber]struct{}{}
}
