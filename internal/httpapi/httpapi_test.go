package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rumpl/daw/internal/adapter/fake"
	"github.com/rumpl/daw/internal/httpapi"
	"github.com/rumpl/daw/internal/pathsec"
	"github.com/rumpl/daw/internal/protocol"
)

type harness struct {
	t    *testing.T
	srv  *httptest.Server
	fake *fake.Adapter
	csrf string
	root string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	guard, skipped, err := pathsec.NewGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	s := httpapi.New(httpapi.Options{
		Adapter: f, Guard: guard, AppVersion: "test",
		TailscaleHosts: []string{"dash.tailnet.ts.net"},
		SkippedRoots:   skipped,
	})
	ts := httptest.NewServer(s)
	t.Cleanup(func() {
		ts.Close()
		s.Shutdown(context.Background())
	})
	h := &harness{t: t, srv: ts, fake: f, csrf: s.CSRFToken(), root: root}
	return h
}

func (h *harness) do(method, path string, body any, mutate ...func(*http.Request)) *http.Response {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, rdr)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpapi.CSRFHeader, h.csrf)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, m := range mutate {
		m(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp
}

func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func (h *harness) openWorkspace() protocol.Workspace {
	h.t.Helper()
	resp := h.do(http.MethodPost, "/api/workspaces/open", protocol.OpenWorkspaceRequest{Path: h.root})
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("open workspace: %d", resp.StatusCode)
	}
	return decodeJSON[protocol.Workspace](h.t, resp)
}

func (h *harness) resolveAgent(ws protocol.Workspace, name string) protocol.ResolvedAgent {
	h.t.Helper()
	p := filepath.Join(h.root, name)
	if err := os.WriteFile(p, []byte("agents:\n  root: {}\n"), 0o600); err != nil {
		h.t.Fatal(err)
	}
	resp := h.do(http.MethodPost, "/api/agents/resolve",
		protocol.ResolveAgentRequest{Source: p, WorkspaceID: ws.WorkspaceID})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("resolve agent: %d %s", resp.StatusCode, body)
	}
	return decodeJSON[protocol.ResolvedAgent](h.t, resp)
}

// setStrict switches a chat to the confirm-everything mode, for tests that
// exercise the confirmation dialog (the server default auto-approves).
func (h *harness) setStrict(chatID string) {
	h.t.Helper()
	strict := protocol.PostureStrict
	resp := h.do(http.MethodPatch, "/api/chats/"+chatID+"/config",
		protocol.UpdateConfigRequest{Posture: &strict})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("set strict posture: %d", resp.StatusCode)
	}
}

func (h *harness) newChat() (protocol.ChatRef, protocol.Workspace, protocol.ResolvedAgent) {
	h.t.Helper()
	ws := h.openWorkspace()
	ag := h.resolveAgent(ws, "agent.yaml")
	resp := h.do(http.MethodPost, "/api/chats",
		protocol.CreateChatRequest{WorkspaceID: ws.WorkspaceID, AgentID: ag.AgentID})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("create chat: %d %s", resp.StatusCode, body)
	}
	return decodeJSON[protocol.ChatRef](h.t, resp), ws, ag
}

// ---------------------------------------------------------------------------

func TestHealthAndBootstrap(t *testing.T) {
	h := newHarness(t)
	resp := h.do(http.MethodGet, "/api/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %d", resp.StatusCode)
	}
	hl := decodeJSON[protocol.Health](t, resp)
	if hl.Status != "ok" {
		t.Fatalf("status %q", hl.Status)
	}

	b := decodeJSON[protocol.Bootstrap](t, h.do(http.MethodGet, "/api/bootstrap", nil))
	if b.CSRFToken == "" {
		t.Fatal("bootstrap must issue a CSRF token")
	}
	if b.Sandboxed {
		t.Fatal("bootstrap must never claim to be sandboxed")
	}
	if len(b.WorkspaceRoots) == 0 {
		t.Fatal("bootstrap must report workspace roots")
	}
	found := false
	for _, n := range b.Notices {
		if n.Code == "no_sandbox" {
			found = true
		}
	}
	if !found {
		t.Fatal("bootstrap must carry the no-sandbox notice")
	}
}

func TestWorkspaceContainmentEnforcedByAPI(t *testing.T) {
	h := newHarness(t)
	outside := t.TempDir()
	resp := h.do(http.MethodPost, "/api/workspaces/open", protocol.OpenWorkspaceRequest{Path: outside})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for path outside roots, got %d", resp.StatusCode)
	}
	e := decodeJSON[protocol.APIError](t, resp)
	if e.Code != "outside_roots" {
		t.Fatalf("code %q", e.Code)
	}
}

func TestAgentSourceContainment(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()
	outside := filepath.Join(t.TempDir(), "evil.yaml")
	if err := os.WriteFile(outside, []byte("agents: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := h.do(http.MethodPost, "/api/agents/resolve",
		protocol.ResolveAgentRequest{Source: outside, WorkspaceID: ws.WorkspaceID})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for agent outside roots, got %d", resp.StatusCode)
	}
}

func TestOCIRequiresExplicitFetch(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()
	resp := h.do(http.MethodPost, "/api/agents/resolve",
		protocol.ResolveAgentRequest{Source: "docker.io/some/agent:latest", WorkspaceID: ws.WorkspaceID})
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("expected 428 without explicit confirmation, got %d", resp.StatusCode)
	}
	resp = h.do(http.MethodPost, "/api/agents/resolve", protocol.ResolveAgentRequest{
		Source: "docker.io/some/agent:latest", WorkspaceID: ws.WorkspaceID, AllowRemoteFetch: true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with explicit confirmation, got %d", resp.StatusCode)
	}
}

func TestUnknownFieldsRejected(t *testing.T) {
	h := newHarness(t)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/api/workspaces/open",
		strings.NewReader(`{"path":"/tmp","surprise":1}`))
	req.Header.Set(httpapi.CSRFHeader, h.csrf)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d", resp.StatusCode)
	}
}

func TestRequestBodyLimit(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	big := strings.Repeat("a", 300*1024)
	resp := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: big, Mode: protocol.DeliveryNormal})
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
}

func TestCSRFRequiredForMutations(t *testing.T) {
	h := newHarness(t)
	resp := h.do(http.MethodPost, "/api/workspaces/open",
		protocol.OpenWorkspaceRequest{Path: h.root},
		func(r *http.Request) { r.Header.Del(httpapi.CSRFHeader) })
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF token, got %d", resp.StatusCode)
	}
	e := decodeJSON[protocol.APIError](t, resp)
	if e.Code != "forbidden_csrf" {
		t.Fatalf("code %q", e.Code)
	}
	// GETs do not require the token.
	if r := h.do(http.MethodGet, "/api/health", nil,
		func(r *http.Request) { r.Header.Del(httpapi.CSRFHeader) }); r.StatusCode != http.StatusOK {
		t.Fatalf("GET should not require CSRF, got %d", r.StatusCode)
	}
}

func TestCrossSiteRejected(t *testing.T) {
	h := newHarness(t)
	resp := h.do(http.MethodPost, "/api/workspaces/open",
		protocol.OpenWorkspaceRequest{Path: h.root},
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") })
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-site, got %d", resp.StatusCode)
	}
	resp = h.do(http.MethodPost, "/api/workspaces/open",
		protocol.OpenWorkspaceRequest{Path: h.root},
		func(r *http.Request) {
			r.Header.Del("Sec-Fetch-Site")
			r.Header.Set("Origin", "https://evil.example")
		})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for foreign origin, got %d", resp.StatusCode)
	}
}

func TestHostAllowList(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		host string
		want int
	}{
		{"127.0.0.1:4788", http.StatusOK},
		{"localhost:4788", http.StatusOK},
		{"[::1]:4788", http.StatusOK},
		{"dash.tailnet.ts.net", http.StatusOK},
		{"evil.example", http.StatusForbidden},
		{"192.168.1.5:4788", http.StatusForbidden},
	}
	for _, tc := range cases {
		resp := h.do(http.MethodGet, "/api/health", nil,
			func(r *http.Request) { r.Host = tc.host })
		if resp.StatusCode != tc.want {
			t.Fatalf("host %q: got %d want %d", tc.host, resp.StatusCode, tc.want)
		}
		resp.Body.Close()
	}
}

// TestTailscaleProxiedOriginAccepted covers the Serve deployment: the browser's
// Origin is the tailnet HTTPS name while the backend still sees loopback.
func TestTailscaleProxiedOriginAccepted(t *testing.T) {
	h := newHarness(t)
	resp := h.do(http.MethodPost, "/api/workspaces/open",
		protocol.OpenWorkspaceRequest{Path: h.root},
		func(r *http.Request) {
			r.Header.Del("Sec-Fetch-Site")
			r.Header.Set("Origin", "https://dash.tailnet.ts.net")
			r.Header.Set("X-Forwarded-Host", "dash.tailnet.ts.net")
			r.Header.Set("X-Forwarded-Proto", "https")
		})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected the Tailscale Serve origin to be accepted, got %d %s", resp.StatusCode, body)
	}
}

func TestForwardedHostNotAllowedRejected(t *testing.T) {
	h := newHarness(t)
	resp := h.do(http.MethodGet, "/api/health", nil,
		func(r *http.Request) { r.Header.Set("X-Forwarded-Host", "attacker.example") })
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for unlisted forwarded host, got %d", resp.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	resp := h.do(http.MethodGet, "/api/health", nil)
	defer resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("missing CSP: %q", csp)
	}
	if resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("missing Referrer-Policy")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
}

// ---------------------------------------------------------------------------
// chat lifecycle
// ---------------------------------------------------------------------------

func TestSessionIDValidatedAgainstFreshListing(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()
	ag := h.resolveAgent(ws, "agent.yaml")
	resp := h.do(http.MethodPost, "/api/chats/resume", protocol.ResumeChatRequest{
		WorkspaceID: ws.WorkspaceID, AgentID: ag.AgentID, SessionID: "../../etc/passwd"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for an unlisted session id, got %d", resp.StatusCode)
	}
}

func TestResumeRestoresStoredHistory(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()
	ag := h.resolveAgent(ws, "agent.yaml")
	h.fake.Seed("sess-seeded", "Older chat", ws.Path, []protocol.Item{
		{Kind: protocol.ItemKindMessage, Message: &protocol.MessageItem{ID: "m1", Role: "user", Text: "hello from before"}},
		{Kind: protocol.ItemKindMessage, Message: &protocol.MessageItem{ID: "m2", Role: "assistant", Text: "hi"}},
	})
	list := decodeJSON[[]protocol.SessionSummary](t,
		h.do(http.MethodGet, "/api/workspaces/"+ws.WorkspaceID+"/sessions", nil))
	if len(list) != 1 || list[0].SessionID != "sess-seeded" {
		t.Fatalf("unexpected session list: %+v", list)
	}

	resp := h.do(http.MethodPost, "/api/chats/resume", protocol.ResumeChatRequest{
		WorkspaceID: ws.WorkspaceID, AgentID: ag.AgentID, SessionID: "sess-seeded"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("resume: %d", resp.StatusCode)
	}
	ref := decodeJSON[protocol.ChatRef](t, resp)
	snap := decodeJSON[protocol.Snapshot](t, h.do(http.MethodGet, "/api/chats/"+ref.ChatID, nil))
	if len(snap.Items) != 2 {
		t.Fatalf("expected the store's own history, got %d items", len(snap.Items))
	}
	if snap.Items[0].Message.Text != "hello from before" {
		t.Fatalf("history not restored from the store: %+v", snap.Items[0].Message)
	}
}

// TestSingleOwnership: a second open of the same session attaches to the live
// chat instead of creating a second writer.
func TestSingleOwnership(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()
	ag := h.resolveAgent(ws, "agent.yaml")
	h.fake.Seed("sess-own", "Owned", ws.Path, nil)

	first := decodeJSON[protocol.ChatRef](t, h.do(http.MethodPost, "/api/chats/resume",
		protocol.ResumeChatRequest{WorkspaceID: ws.WorkspaceID, AgentID: ag.AgentID, SessionID: "sess-own"}))
	second := decodeJSON[protocol.ChatRef](t, h.do(http.MethodPost, "/api/chats/resume",
		protocol.ResumeChatRequest{WorkspaceID: ws.WorkspaceID, AgentID: ag.AgentID, SessionID: "sess-own"}))
	if first.ChatID != second.ChatID {
		t.Fatalf("second open created a second writer: %s vs %s", first.ChatID, second.ChatID)
	}
	list := decodeJSON[[]protocol.SessionSummary](t,
		h.do(http.MethodGet, "/api/workspaces/"+ws.WorkspaceID+"/sessions", nil))
	if len(list) != 1 || !list[0].Live {
		t.Fatalf("expected the session to be marked live: %+v", list)
	}
}

// ---------------------------------------------------------------------------
// SSE
// ---------------------------------------------------------------------------

type sseClient struct {
	t      *testing.T
	resp   *http.Response
	reader *bufio.Reader
}

func (h *harness) openSSE(chatID string, lastEventID uint64) *sseClient {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/api/chats/"+chatID+"/events", nil)
	if lastEventID > 0 {
		req.Header.Set("Last-Event-ID", fmt.Sprint(lastEventID))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("sse: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		h.t.Fatalf("content-type %q", ct)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		h.t.Fatal("missing X-Accel-Buffering: no")
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-transform") {
		h.t.Fatalf("cache-control %q", cc)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return &sseClient{t: h.t, resp: resp, reader: bufio.NewReader(resp.Body)}
}

// next reads one SSE event, skipping heartbeats.
func (c *sseClient) next(timeout time.Duration) (protocol.Event, bool) {
	c.t.Helper()
	type res struct {
		ev protocol.Event
		ok bool
	}
	ch := make(chan res, 1)
	go func() {
		var data string
		for {
			line, err := c.reader.ReadString('\n')
			if err != nil {
				ch <- res{}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, ":"):
				continue
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && data != "":
				var ev protocol.Event
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					ch <- res{}
					return
				}
				ch <- res{ev, true}
				return
			}
		}
	}()
	select {
	case r := <-ch:
		return r.ev, r.ok
	case <-time.After(timeout):
		return protocol.Event{}, false
	}
}

func (c *sseClient) collect(until func(protocol.Event) bool, timeout time.Duration) []protocol.Event {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	var out []protocol.Event
	for time.Now().Before(deadline) {
		ev, ok := c.next(time.Until(deadline))
		if !ok {
			break
		}
		out = append(out, ev)
		if until(ev) {
			return out
		}
	}
	c.t.Fatalf("timed out waiting for the expected event; got %d events", len(out))
	return out
}

func TestSSESnapshotOnConnect(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	ev, ok := sse.next(3 * time.Second)
	if !ok || ev.Type != protocol.EventSnapshot {
		t.Fatalf("expected a snapshot first, got %+v", ev)
	}
	if ev.Snapshot == nil || ev.Snapshot.Meta.SessionID != ref.SessionID {
		t.Fatalf("snapshot missing metadata: %+v", ev.Snapshot)
	}
}

func TestFullTurnStreamsAndSettles(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second) // snapshot

	resp := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/notool hello", Mode: protocol.DeliveryNormal})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d", resp.StatusCode)
	}
	acc := decodeJSON[protocol.Accepted](t, resp)
	if !acc.Accepted || acc.RunID == "" {
		t.Fatalf("bad accept body: %+v", acc)
	}

	events := sse.collect(func(e protocol.Event) bool {
		return e.Type == protocol.EventRunStatus && e.Run != nil && e.Run.State == protocol.RunStateIdle
	}, 5*time.Second)

	var sawDelta, sawReasoning, sawUsage bool
	for _, e := range events {
		switch e.Type {
		case protocol.EventAssistantDelta:
			sawDelta = true
		case protocol.EventReasoningDelta:
			sawReasoning = true
		case protocol.EventUsage:
			sawUsage = true
		}
	}
	if !sawDelta || !sawReasoning || !sawUsage {
		t.Fatalf("missing stream parts: delta=%v reasoning=%v usage=%v", sawDelta, sawReasoning, sawUsage)
	}

	// Sequence numbers must be strictly increasing.
	var prev uint64
	for _, e := range events {
		if e.Seq <= prev {
			t.Fatalf("sequence numbers not monotonic: %d after %d", e.Seq, prev)
		}
		prev = e.Seq
	}
}

func TestToolConfirmationRoundTrip(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	h.setStrict(ref.ChatID)
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)

	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "list the files", Mode: protocol.DeliveryNormal}).Body.Close()

	events := sse.collect(func(e protocol.Event) bool {
		return e.Type == protocol.EventToolConfirmation
	}, 5*time.Second)
	req := events[len(events)-1].Confirmation
	if req.Pattern == "" {
		t.Fatal("confirmation must carry the exact pattern to be granted")
	}
	if !strings.Contains(req.PatternLabel, req.Pattern) {
		t.Fatalf("dialog label %q must show the pattern %q", req.PatternLabel, req.Pattern)
	}

	resp := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/tool-confirmation",
		protocol.ToolConfirmationReply{ToolCallID: req.ToolCallID, Decision: protocol.DecisionApproveAlways})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("confirm: %d", resp.StatusCode)
	}
	resolved := sse.collect(func(e protocol.Event) bool {
		return e.Type == protocol.EventToolResolved
	}, 5*time.Second)
	got := resolved[len(resolved)-1].ToolResolved
	// Pattern fidelity: what the dialog showed is exactly what was granted.
	if got.Pattern != req.Pattern {
		t.Fatalf("granted pattern %q != dialog pattern %q", got.Pattern, req.Pattern)
	}

	// The grant is reflected in the honest permission view.
	sse.collect(func(e protocol.Event) bool {
		return e.Type == protocol.EventRunStatus && e.Run != nil && e.Run.State == protocol.RunStateIdle
	}, 5*time.Second)
	snap := decodeJSON[protocol.Snapshot](t, h.do(http.MethodGet, "/api/chats/"+ref.ChatID, nil))
	found := false
	for _, g := range snap.Meta.Permissions.SessionGrants {
		if g == req.Pattern {
			found = true
		}
	}
	if !found {
		t.Fatalf("granted pattern missing from the permission view: %+v", snap.Meta.Permissions)
	}
}

func TestUnknownConfirmationRejected(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	resp := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/tool-confirmation",
		protocol.ToolConfirmationReply{ToolCallID: "nope", Decision: protocol.DecisionApprove})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestElicitationCorrelatedByID(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)

	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/elicit please", Mode: protocol.DeliveryNormal}).Body.Close()
	events := sse.collect(func(e protocol.Event) bool { return e.Type == protocol.EventElicitation }, 5*time.Second)
	req := events[len(events)-1].Elicitation
	if req.ElicitationID == "" {
		t.Fatal("elicitation must carry an id for correlation")
	}

	// A wrong id must not resolve the pending request.
	bad := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/elicitation",
		protocol.ElicitationReply{ElicitationID: req.ElicitationID + "-wrong", Action: protocol.ElicitAccept})
	if bad.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown elicitation id, got %d", bad.StatusCode)
	}

	ok := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/elicitation",
		protocol.ElicitationReply{ElicitationID: req.ElicitationID, Action: protocol.ElicitAccept,
			Content: map[string]any{"branch": "main"}})
	if ok.StatusCode != http.StatusAccepted {
		t.Fatalf("elicitation reply: %d", ok.StatusCode)
	}
	sse.collect(func(e protocol.Event) bool {
		return e.Type == protocol.EventElicitResolved && e.ElicitResolved.ElicitationID == req.ElicitationID
	}, 5*time.Second)
}

func TestSteerFollowUpAndAbort(t *testing.T) {
	h := newHarness(t)
	h.fake.Delay = 60 * time.Millisecond
	ref, _, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)

	// Steer while idle is refused.
	if r := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "steer", Mode: protocol.DeliverySteer}); r.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 steering an idle chat, got %d", r.StatusCode)
	}

	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/notool long", Mode: protocol.DeliveryNormal}).Body.Close()

	if r := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "actually, do this", Mode: protocol.DeliverySteer}); r.StatusCode != http.StatusAccepted {
		t.Fatalf("steer while running: %d", r.StatusCode)
	}
	// A normal send while busy is a conflict, not a silent queue.
	if r := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "another", Mode: protocol.DeliveryNormal}); r.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for a normal send while busy, got %d", r.StatusCode)
	}

	sawQueue := false
	sse.collect(func(e protocol.Event) bool {
		if e.Type == protocol.EventRunStatus && e.Run != nil && e.Run.Queue.SteerDepth > 0 {
			sawQueue = true
		}
		return e.Type == protocol.EventRunStatus && e.Run != nil && e.Run.State == protocol.RunStateIdle
	}, 6*time.Second)
	if !sawQueue {
		t.Fatal("expected a queue update reporting the steer depth")
	}
}

func TestAbortSettles(t *testing.T) {
	h := newHarness(t)
	h.fake.Delay = 200 * time.Millisecond
	ref, _, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)

	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/notool slow", Mode: protocol.DeliveryNormal}).Body.Close()
	time.Sleep(100 * time.Millisecond)
	if r := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/abort", nil); r.StatusCode != http.StatusAccepted {
		t.Fatalf("abort: %d", r.StatusCode)
	}
	sawStopping := false
	sse.collect(func(e protocol.Event) bool {
		if e.Type == protocol.EventRunStatus && e.Run != nil && e.Run.State == protocol.RunStateStopping {
			sawStopping = true
		}
		return e.Type == protocol.EventRunStatus && e.Run != nil && e.Run.State == protocol.RunStateIdle
	}, 6*time.Second)
	if !sawStopping {
		t.Fatal("expected a Stopping state before the run settled")
	}
}

func TestIdempotentSubmission(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	key := "abc-123"
	r1 := decodeJSON[protocol.Accepted](t, h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/notool hi", Mode: protocol.DeliveryNormal, IdempotencyKey: key}))
	r2 := decodeJSON[protocol.Accepted](t, h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/notool hi", Mode: protocol.DeliveryNormal, IdempotencyKey: key}))
	if r1.RunID != r2.RunID {
		t.Fatalf("idempotent replay started a second run: %s vs %s", r1.RunID, r2.RunID)
	}
}

func TestReconnectReplayWithoutDuplicates(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)
	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/notool hello", Mode: protocol.DeliveryNormal}).Body.Close()
	events := sse.collect(func(e protocol.Event) bool {
		return e.Type == protocol.EventRunStatus && e.Run != nil && e.Run.State == protocol.RunStateIdle
	}, 5*time.Second)

	// Reconnect from the middle of the stream: only later events replay.
	mid := events[len(events)/2].Seq
	sse2 := h.openSSE(ref.ChatID, mid)
	first, ok := sse2.next(3 * time.Second)
	if !ok {
		t.Fatal("no event after reconnect")
	}
	if first.Type == protocol.EventSnapshot {
		t.Fatal("expected replay, not a resnapshot, for an in-buffer resume point")
	}
	if first.Seq != mid+1 {
		t.Fatalf("replay started at %d, expected %d", first.Seq, mid+1)
	}

	// The snapshot after the turn contains each item exactly once.
	snap := decodeJSON[protocol.Snapshot](t, h.do(http.MethodGet, "/api/chats/"+ref.ChatID, nil))
	seen := map[string]int{}
	for _, it := range snap.Items {
		if it.Message != nil {
			seen["m:"+it.Message.ID]++
		}
		if it.Tool != nil {
			seen["t:"+it.Tool.ID]++
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("item %s appears %d times after reconnect", id, n)
		}
	}
}

func TestReconnectBeyondBufferResnapshots(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 999999)
	ev, ok := sse.next(3 * time.Second)
	if !ok || ev.Type != protocol.EventGap {
		t.Fatalf("expected a gap marker for an evicted resume point, got %+v", ev)
	}
	ev2, ok := sse.next(3 * time.Second)
	if !ok || ev2.Type != protocol.EventSnapshot {
		t.Fatalf("expected a resnapshot after the gap, got %+v", ev2)
	}
}

func TestToolPreviewIsBounded(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	h.setStrict(ref.ChatID)
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)
	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "run it", Mode: protocol.DeliveryNormal}).Body.Close()
	events := sse.collect(func(e protocol.Event) bool { return e.Type == protocol.EventToolConfirmation }, 5*time.Second)
	req := events[len(events)-1].Confirmation
	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/tool-confirmation",
		protocol.ToolConfirmationReply{ToolCallID: req.ToolCallID, Decision: protocol.DecisionApprove}).Body.Close()
	end := sse.collect(func(e protocol.Event) bool { return e.Type == protocol.EventToolEnd }, 5*time.Second)
	tool := end[len(end)-1].Tool
	if len(tool.Preview) > 4096+128 {
		t.Fatalf("tool preview was not bounded: %d bytes", len(tool.Preview))
	}
}

func TestConfigChangesRequireIdleAndAutoApproveConfirmation(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()

	// Move down first: this server's default is already autonomous.
	strict := protocol.PostureStrict
	if r := h.do(http.MethodPatch, "/api/chats/"+ref.ChatID+"/config",
		protocol.UpdateConfigRequest{Posture: &strict}); r.StatusCode != http.StatusOK {
		t.Fatalf("downgrade to strict: %d", r.StatusCode)
	}

	auto := protocol.PostureAutonomous
	resp := h.do(http.MethodPatch, "/api/chats/"+ref.ChatID+"/config",
		protocol.UpdateConfigRequest{Posture: &auto})
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("expected 428 without explicit confirmation, got %d", resp.StatusCode)
	}
	resp = h.do(http.MethodPatch, "/api/chats/"+ref.ChatID+"/config",
		protocol.UpdateConfigRequest{Posture: &auto, ConfirmAutoApprove: true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("posture change: %d", resp.StatusCode)
	}
	meta := decodeJSON[protocol.SessionMeta](t, resp)
	if meta.Permissions.Posture != protocol.PostureAutonomous || !meta.Permissions.AutoApproveAll {
		t.Fatalf("posture not applied honestly: %+v", meta.Permissions)
	}

	model := "fake/model-b"
	resp = h.do(http.MethodPatch, "/api/chats/"+ref.ChatID+"/config",
		protocol.UpdateConfigRequest{Model: &model})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("model change: %d", resp.StatusCode)
	}
	level := "high"
	resp = h.do(http.MethodPatch, "/api/chats/"+ref.ChatID+"/config",
		protocol.UpdateConfigRequest{ThinkingLevel: &level})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("thinking change: %d", resp.StatusCode)
	}
}

// TestConfiguredDefaultPostureApplies: this deployment is configured to start
// new chats in autonomous mode, and that must be what the session really gets.
func TestConfiguredDefaultPostureApplies(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	snap := decodeJSON[protocol.Snapshot](t, h.do(http.MethodGet, "/api/chats/"+ref.ChatID, nil))
	if snap.Meta.Permissions.Posture != protocol.PostureAutonomous {
		t.Fatalf("new chats must use the configured default posture, got %q", snap.Meta.Permissions.Posture)
	}
	if !snap.Meta.Permissions.AutoApproveAll {
		t.Fatal("autonomous must report auto-approve honestly")
	}
	b := decodeJSON[protocol.Bootstrap](t, h.do(http.MethodGet, "/api/bootstrap", nil))
	if b.DefaultPosture != protocol.PostureAutonomous {
		t.Fatalf("bootstrap must advertise the default posture, got %q", b.DefaultPosture)
	}
	var warned bool
	for _, n := range b.Notices {
		if n.Code == "default_autonomous" {
			warned = true
		}
	}
	if !warned {
		t.Fatal("an auto-approve default must be stated plainly in bootstrap")
	}
}

// TestAutonomousSkipsConfirmationDialog is the end-to-end form of the fix:
// with auto-approve on, a tool call must run without ever raising a dialog.
func TestAutonomousSkipsConfirmationDialog(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)

	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "list the files", Mode: protocol.DeliveryNormal}).Body.Close()

	events := sse.collect(func(e protocol.Event) bool {
		return e.Type == protocol.EventRunStatus && e.Run != nil && e.Run.State == protocol.RunStateIdle
	}, 6*time.Second)

	for _, e := range events {
		if e.Type == protocol.EventToolConfirmation {
			t.Fatal("autonomous mode must never raise a tool-confirmation dialog")
		}
	}
	var ranTool bool
	for _, e := range events {
		if e.Type == protocol.EventToolEnd && e.Tool != nil && e.Tool.State == protocol.ToolStateSuccess {
			ranTool = true
		}
	}
	if !ranTool {
		t.Fatal("the tool should have run and completed without asking")
	}
}

// TestStrictStillAsks: the safe mode is still reachable and still enforced.
func TestStrictStillAsks(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	strict := protocol.PostureStrict
	if r := h.do(http.MethodPatch, "/api/chats/"+ref.ChatID+"/config",
		protocol.UpdateConfigRequest{Posture: &strict}); r.StatusCode != http.StatusOK {
		t.Fatalf("switch to strict: %d", r.StatusCode)
	}
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)
	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "list the files", Mode: protocol.DeliveryNormal}).Body.Close()
	sse.collect(func(e protocol.Event) bool { return e.Type == protocol.EventToolConfirmation }, 6*time.Second)
}

// TestApproveForSessionEscalatesMode: "approve all for this session" from the
// dialog must really change the session's mode, not just relabel a cache.
func TestApproveForSessionEscalatesMode(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	strict := protocol.PostureStrict
	h.do(http.MethodPatch, "/api/chats/"+ref.ChatID+"/config",
		protocol.UpdateConfigRequest{Posture: &strict}).Body.Close()

	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)
	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "list the files", Mode: protocol.DeliveryNormal}).Body.Close()
	events := sse.collect(func(e protocol.Event) bool { return e.Type == protocol.EventToolConfirmation }, 6*time.Second)
	req := events[len(events)-1].Confirmation

	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/tool-confirmation",
		protocol.ToolConfirmationReply{ToolCallID: req.ToolCallID,
			Decision: protocol.DecisionApproveSession}).Body.Close()
	sse.collect(func(e protocol.Event) bool {
		return e.Type == protocol.EventRunStatus && e.Run != nil && e.Run.State == protocol.RunStateIdle
	}, 6*time.Second)

	snap := decodeJSON[protocol.Snapshot](t, h.do(http.MethodGet, "/api/chats/"+ref.ChatID, nil))
	if snap.Meta.Permissions.Posture != protocol.PostureAutonomous || !snap.Meta.Permissions.AutoApproveAll {
		t.Fatalf("approve-for-session must escalate the real mode, got %+v", snap.Meta.Permissions)
	}
}

func TestRetitleCompactStatsAndDispose(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()

	if r := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/retitle",
		protocol.RetitleRequest{Title: "Renamed"}); r.StatusCode != http.StatusOK {
		t.Fatalf("retitle: %d", r.StatusCode)
	}
	if r := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/retitle",
		protocol.RetitleRequest{Title: "   "}); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a blank title, got %d", r.StatusCode)
	}
	if r := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/compact", nil); r.StatusCode != http.StatusAccepted {
		t.Fatalf("compact: %d", r.StatusCode)
	}
	stats := decodeJSON[protocol.Stats](t, h.do(http.MethodGet, "/api/chats/"+ref.ChatID+"/stats", nil))
	if stats.AgentName == "" {
		t.Fatalf("stats missing agent: %+v", stats)
	}
	if r := h.do(http.MethodDelete, "/api/chats/"+ref.ChatID, nil); r.StatusCode != http.StatusOK {
		t.Fatalf("dispose: %d", r.StatusCode)
	}
	if r := h.do(http.MethodGet, "/api/chats/"+ref.ChatID, nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after dispose, got %d", r.StatusCode)
	}
}

// TestDisposeCancelsPendingDialogs: disposal must never leave the runtime
// blocked on a dialog nobody can answer.
func TestDisposeCancelsPendingDialogs(t *testing.T) {
	h := newHarness(t)
	ref, _, _ := h.newChat()
	h.setStrict(ref.ChatID)
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)
	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "run it", Mode: protocol.DeliveryNormal}).Body.Close()
	sse.collect(func(e protocol.Event) bool { return e.Type == protocol.EventToolConfirmation }, 5*time.Second)

	h.do(http.MethodDelete, "/api/chats/"+ref.ChatID, nil).Body.Close()
	sse.collect(func(e protocol.Event) bool { return e.Type == protocol.EventChatClosed }, 5*time.Second)
}

func TestErrorShapeHasNoInternals(t *testing.T) {
	h := newHarness(t)
	resp := h.do(http.MethodGet, "/api/chats/does-not-exist", nil)
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "/Users/") || strings.Contains(string(body), "goroutine") {
		t.Fatalf("error body leaks internals: %s", body)
	}
	var e protocol.APIError
	if err := json.Unmarshal(body, &e); err != nil || e.Code == "" {
		t.Fatalf("expected the consistent error shape, got %s", body)
	}
}

// TestNoSecretsInResponses samples every read endpoint for credential-looking
// values and raw environment data.
func TestNoSecretsInResponses(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret-value-must-not-leak")
	h := newHarness(t)
	ref, ws, _ := h.newChat()
	paths := []string{
		"/api/health", "/api/bootstrap",
		"/api/workspaces/" + ws.WorkspaceID + "/sessions",
		"/api/chats/" + ref.ChatID,
		"/api/chats/" + ref.ChatID + "/models",
		"/api/chats/" + ref.ChatID + "/commands",
		"/api/chats/" + ref.ChatID + "/stats",
	}
	for _, p := range paths {
		resp := h.do(http.MethodGet, p, nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		for _, needle := range []string{"sk-secret-value-must-not-leak", "API_KEY", "Authorization", "_TOKEN"} {
			if strings.Contains(string(body), needle) {
				t.Fatalf("%s leaked %q", p, needle)
			}
		}
	}
}

// TestChatWithoutChoosingAnAgent: the whole point of the default agent — a
// workspace is enough to start working.
func TestChatWithoutChoosingAnAgent(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()

	resp := h.do(http.MethodPost, "/api/chats", protocol.CreateChatRequest{WorkspaceID: ws.WorkspaceID})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected a chat with no agentId to work, got %d %s", resp.StatusCode, body)
	}
	ref := decodeJSON[protocol.ChatRef](t, resp)

	snap := decodeJSON[protocol.Snapshot](t, h.do(http.MethodGet, "/api/chats/"+ref.ChatID, nil))
	if snap.Meta.AgentSource != "coder" {
		t.Fatalf("expected the built-in coder agent, got %q", snap.Meta.AgentSource)
	}
	// Model and thinking controls must still be available.
	models := decodeJSON[[]protocol.ModelOption](t, h.do(http.MethodGet, "/api/chats/"+ref.ChatID+"/models", nil))
	if len(models) == 0 {
		t.Fatal("the default agent must still expose model choices")
	}
	if len(snap.Meta.ThinkingLevels) == 0 {
		t.Fatal("the default agent must still expose thinking levels")
	}
}

// TestBootstrapAdvertisesDefaultAgent so the UI never has to guess.
func TestBootstrapAdvertisesDefaultAgent(t *testing.T) {
	h := newHarness(t)
	b := decodeJSON[protocol.Bootstrap](t, h.do(http.MethodGet, "/api/bootstrap", nil))
	if b.DefaultAgent != "coder" {
		t.Fatalf("default agent should be coder, got %q", b.DefaultAgent)
	}
	if len(b.BuiltinAgents) == 0 {
		t.Fatal("bootstrap must list the module's built-in agents")
	}
}

// TestBuiltinNameIsNotAPathBypass: only names the *adapter* reports as
// built-in skip containment; a lookalike is still treated as a path/OCI ref.
func TestBuiltinNameIsNotAPathBypass(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()
	resp := h.do(http.MethodPost, "/api/agents/resolve",
		protocol.ResolveAgentRequest{Source: "coder/../../etc/passwd", WorkspaceID: ws.WorkspaceID})
	if resp.StatusCode == http.StatusOK {
		t.Fatal("a lookalike built-in name must not bypass containment")
	}
}

// TestResumeWithoutAgentUsesDefault keeps resume as easy as new.
func TestResumeWithoutAgentUsesDefault(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()
	h.fake.Seed("sess-default", "Older", ws.Path, nil)
	resp := h.do(http.MethodPost, "/api/chats/resume",
		protocol.ResumeChatRequest{WorkspaceID: ws.WorkspaceID, SessionID: "sess-default"})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("resume without an agentId: %d %s", resp.StatusCode, body)
	}
}

func TestUnknownRoutes(t *testing.T) {
	h := newHarness(t)
	if r := h.do(http.MethodGet, "/api/nope", nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", r.StatusCode)
	}
}
