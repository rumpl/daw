package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rumpl/daw/internal/protocol"
)

func (s *Server) handleDashboardEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.fail(w, http.StatusInternalServerError, "no_streaming", "streaming is unavailable")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Content-Encoding", "identity")
	w.WriteHeader(http.StatusOK)

	var lastID uint64
	if value := r.Header.Get("Last-Event-ID"); value != "" {
		lastID, _ = strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	} else if value := r.URL.Query().Get("lastEventId"); value != "" {
		lastID, _ = strconv.ParseUint(value, 10, 64)
	}
	sub, replay, resumed := s.events.subscribe(lastID)
	defer s.events.unsubscribe(sub)
	write := func(ev protocol.DashboardEvent) bool {
		data, err := json.Marshal(ev)
		if err != nil {
			return true
		}
		if ev.Seq > 0 {
			if _, err := fmt.Fprintf(w, "id: %d\n", ev.Seq); err != nil {
				return false
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if resumed {
		for _, ev := range replay {
			if !write(ev) {
				return
			}
		}
	} else {
		if lastID > 0 && !write(protocol.DashboardEvent{Type: protocol.DashboardEventGap}) {
			return
		}
		if !write(s.events.snapshot()) {
			return
		}
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-sub.ch:
			if !open || !write(ev) {
				return
			}
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ---------------------------------------------------------------------------
// SSE
// ---------------------------------------------------------------------------

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.fail(w, http.StatusInternalServerError, "no_streaming", "streaming is unavailable")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Content-Encoding", "identity")
	w.WriteHeader(http.StatusOK)

	var lastID uint64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil {
			lastID = n
		}
	} else if v := r.URL.Query().Get("lastEventId"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			lastID = n
		}
	}

	sub, replay, resumed := c.subscribe(lastID)
	defer c.unsubscribe(sub)

	write := func(ev protocol.Event) bool {
		buf, err := json.Marshal(ev)
		if err != nil {
			return true
		}
		if ev.Seq > 0 {
			if _, err := fmt.Fprintf(w, "id: %d\n", ev.Seq); err != nil {
				return false
			}
		}
		// Deliberately unnamed events: the discriminator already lives in the
		// JSON payload, and unnamed events reach EventSource.onmessage without
		// the client having to register a listener per type.
		if _, err := fmt.Fprintf(w, "data: %s\n\n", buf); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if resumed {
		for _, ev := range replay {
			if !write(ev) {
				return
			}
		}
	} else {
		if lastID > 0 {
			// The resume point fell out of the ring buffer: tell the client
			// explicitly, then resnapshot (upstream's gapEvent semantics).
			if !write(protocol.Event{Type: protocol.EventGap}) {
				return
			}
		}
		snap := c.snapshot()
		if !write(protocol.Event{Type: protocol.EventSnapshot, Seq: snap.Seq, Snapshot: &snap}) {
			return
		}
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.ch:
			if !open {
				return
			}
			if !write(ev) {
				return
			}
		case <-ticker.C:
			// SSE comment: invisible to EventSource, carries no id.
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
