package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rumpl/daw/internal/adapter/fake"
	"github.com/rumpl/daw/internal/httpapi"
	"github.com/rumpl/daw/internal/pathsec"
	"github.com/rumpl/daw/internal/protocol"
)

type harness struct {
	t         *testing.T
	srv       *httptest.Server
	fake      *fake.Adapter
	csrf      string
	root      string
	pluginDir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	guard, _, err := pathsec.NewGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	pluginDir := t.TempDir()
	s := httpapi.New(httpapi.Options{
		Adapter: f, Guard: guard, AppVersion: "test",
		TailscaleHosts: []string{"dash.tailnet.ts.net"},
		PluginDir:      pluginDir,
	})
	ts := httptest.NewServer(s)
	t.Cleanup(func() {
		ts.Close()
		s.Shutdown(context.WithoutCancel(t.Context()))
	})
	h := &harness{t: t, srv: ts, fake: f, csrf: s.CSRFToken(), root: root, pluginDir: pluginDir}
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
	req, err := http.NewRequestWithContext(h.t.Context(), method, h.srv.URL+path, rdr)
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
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (h *harness) upload(chatID, name, contentType string, data []byte) *http.Response {
	h.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		h.t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		h.t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(h.t.Context(), http.MethodPost, h.srv.URL+"/api/chats/"+chatID+"/attachments", &body)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(httpapi.CSRFHeader, h.csrf)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	_ = contentType
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
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
	h.t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("open workspace: %d", resp.StatusCode)
	}
	return decodeJSON[protocol.Workspace](h.t, resp)
}

func (h *harness) newChat() (protocol.ChatRef, protocol.Workspace) {
	h.t.Helper()
	ws := h.openWorkspace()
	resp := h.do(http.MethodPost, "/api/chats",
		protocol.CreateChatRequest{WorkspaceID: ws.WorkspaceID})
	h.t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("create chat: %d %s", resp.StatusCode, body)
	}
	return decodeJSON[protocol.ChatRef](h.t, resp), ws
}

// ---------------------------------------------------------------------------

func TestMessageAttachmentRoundTrip(t *testing.T) {
	h := newHarness(t)
	ref, _ := h.newChat()
	upload := h.upload(ref.ChatID, "notes.txt", "text/plain", []byte("important context"))
	if upload.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d", upload.StatusCode)
	}
	attachment := decodeJSON[protocol.Attachment](t, upload)
	if attachment.Name != "notes.txt" || attachment.Size != 17 {
		t.Fatalf("unexpected attachment: %+v", attachment)
	}
	resp := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages", protocol.SendMessageRequest{
		Text: "review this", Mode: protocol.DeliveryNormal, Attachments: []string{attachment.ID},
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send attachment: %d", resp.StatusCode)
	}
	time.Sleep(20 * time.Millisecond)
	snap := decodeJSON[protocol.Snapshot](t, h.do(http.MethodGet, "/api/chats/"+ref.ChatID, nil))
	if len(snap.Items) == 0 || snap.Items[0].Message == nil || len(snap.Items[0].Message.Attachments) != 1 {
		t.Fatalf("attachment missing from snapshot: %+v", snap.Items)
	}
	if snap.Items[0].Message.Attachments[0].Name != "notes.txt" {
		t.Fatalf("wrong attachment metadata: %+v", snap.Items[0].Message.Attachments)
	}
}

func TestAttachmentRejectsUnsupportedBinary(t *testing.T) {
	h := newHarness(t)
	ref, _ := h.newChat()
	resp := h.upload(ref.ChatID, "program.bin", "application/octet-stream", []byte{0, 1, 2, 3})
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported upload: %d", resp.StatusCode)
	}
}

func TestHealthAndBootstrap(t *testing.T) {
	h := newHarness(t)
	resp := h.do(http.MethodGet, "/api/health", nil)
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %d", resp.StatusCode)
	}
	hl := decodeJSON[protocol.Health](t, resp)
	if hl.Status != "ok" {
		t.Fatalf("status %q", hl.Status)
	}

	bootstrapResp := h.do(http.MethodGet, "/api/bootstrap", nil)
	t.Cleanup(func() { bootstrapResp.Body.Close() })
	b := decodeJSON[protocol.Bootstrap](t, bootstrapResp)
	if b.CSRFToken == "" {
		t.Fatal("bootstrap must issue a CSRF token")
	}
	if b.Sandboxed {
		t.Fatal("bootstrap must never claim to be sandboxed")
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

func TestGlobalPluginCatalogAndAssets(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.pluginDir, "hello")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"apiVersion":1,"id":"hello","name":"Hello","entry":"index.js",
		"pages":[{"id":"main","path":"","label":"Hello","sidebar":true}]
	}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("export function mount() {}"), 0o600); err != nil {
		t.Fatal(err)
	}

	catalogResp := h.do(http.MethodGet, "/api/plugins", nil)
	t.Cleanup(func() { catalogResp.Body.Close() })
	catalog := decodeJSON[protocol.PluginCatalog](t, catalogResp)
	if len(catalog.Plugins) != 1 || catalog.Plugins[0].ID != "hello" {
		t.Fatalf("unexpected plugin catalog: %#v", catalog)
	}
	resp := h.do(http.MethodGet, catalog.Plugins[0].EntryURL, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("plugin asset: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Fatalf("plugin module content type: %q", got)
	}
}

func TestWorkspaceContainmentEnforcedByAPI(t *testing.T) {
	h := newHarness(t)
	outside := t.TempDir()
	resp := h.do(http.MethodPost, "/api/workspaces/open", protocol.OpenWorkspaceRequest{Path: outside})
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for path outside roots, got %d", resp.StatusCode)
	}
	e := decodeJSON[protocol.APIError](t, resp)
	if e.Code != "outside_roots" {
		t.Fatalf("code %q", e.Code)
	}
}

func TestAgentResolutionEndpointDoesNotExist(t *testing.T) {
	h := newHarness(t)
	resp := h.do(http.MethodPost, "/api/agents/resolve", map[string]string{"source": "anything"})
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected removed agent resolution endpoint to return 404, got %d", resp.StatusCode)
	}
}

func TestUnknownFieldsRejected(t *testing.T) {
	h := newHarness(t)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, h.srv.URL+"/api/workspaces/open",
		strings.NewReader(`{"path":"/tmp","surprise":1}`))
	req.Header.Set(httpapi.CSRFHeader, h.csrf)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d", resp.StatusCode)
	}
}

func TestRequestBodyLimit(t *testing.T) {
	h := newHarness(t)
	ref, _ := h.newChat()
	big := strings.Repeat("a", 300*1024)
	resp := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: big, Mode: protocol.DeliveryNormal})
	t.Cleanup(func() { resp.Body.Close() })
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

type dashboardSSEClient struct {
	t      *testing.T
	resp   *http.Response
	reader *bufio.Reader
}

func (h *harness) openDashboardSSE(lastEventID uint64) *dashboardSSEClient {
	h.t.Helper()
	req, _ := http.NewRequestWithContext(h.t.Context(), http.MethodGet, h.srv.URL+"/api/events", http.NoBody)
	if lastEventID > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatUint(lastEventID, 10))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		h.t.Fatalf("dashboard sse: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return &dashboardSSEClient{t: h.t, resp: resp, reader: bufio.NewReader(resp.Body)}
}

func (c *dashboardSSEClient) next(timeout time.Duration) (protocol.DashboardEvent, bool) {
	c.t.Helper()
	type result struct {
		event protocol.DashboardEvent
		ok    bool
	}
	resultCh := make(chan result, 1)
	go func() {
		var data string
		for {
			line, err := c.reader.ReadString('\n')
			if err != nil {
				resultCh <- result{}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, ":"):
				continue
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && data != "":
				var event protocol.DashboardEvent
				if json.Unmarshal([]byte(data), &event) == nil {
					resultCh <- result{event: event, ok: true}
				} else {
					resultCh <- result{}
				}
				return
			}
		}
	}()
	select {
	case result := <-resultCh:
		return result.event, result.ok
	case <-time.After(timeout):
		return protocol.DashboardEvent{}, false
	}
}

func TestDashboardSSESessionChangesAndReplay(t *testing.T) {
	h := newHarness(t)
	stream := h.openDashboardSSE(0)
	initial, ok := stream.next(3 * time.Second)
	if !ok || initial.Type != protocol.DashboardEventSnapshot {
		t.Fatalf("expected dashboard snapshot, got %+v", initial)
	}
	ref, ws := h.newChat()
	changed, ok := stream.next(3 * time.Second)
	if !ok || changed.Type != protocol.DashboardEventSessionsChanged || changed.Reason != "opened" {
		t.Fatalf("expected opened invalidation, got %+v", changed)
	}
	if len(changed.WorkspaceIDs) != 1 || changed.WorkspaceIDs[0] != ws.WorkspaceID ||
		len(changed.SessionIDs) != 1 || changed.SessionIDs[0] != ref.SessionID {
		t.Fatalf("unexpected invalidation scope: %+v", changed)
	}

	resp := h.do(http.MethodDelete, "/api/chats/"+ref.ChatID, nil)
	resp.Body.Close()
	closed, ok := stream.next(3 * time.Second)
	if !ok || closed.Type != protocol.DashboardEventSessionsChanged {
		t.Fatalf("expected disposed invalidation, got %+v", closed)
	}
	stream.resp.Body.Close()

	replay := h.openDashboardSSE(changed.Seq)
	replayed, ok := replay.next(3 * time.Second)
	if !ok || replayed.Seq != closed.Seq || replayed.Type != protocol.DashboardEventSessionsChanged {
		t.Fatalf("expected replayed invalidation, got %+v", replayed)
	}
}

func TestDashboardSSEPluginChanges(t *testing.T) {
	h := newHarness(t)
	stream := h.openDashboardSSE(0)
	stream.next(3 * time.Second)
	dir := filepath.Join(h.pluginDir, "watched")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"apiVersion":1,"id":"watched","name":"Watched","entry":"index.js","pages":[{"id":"main","path":"","label":"Watched","sidebar":true}]}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("export function mount() {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	event, ok := stream.next(5 * time.Second)
	if !ok || event.Type != protocol.DashboardEventPluginsChanged || event.Revision == "" {
		t.Fatalf("expected plugin invalidation, got %+v", event)
	}
}

// ---------------------------------------------------------------------------
// chat lifecycle
// ---------------------------------------------------------------------------

func TestSessionIDValidatedAgainstFreshListing(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()
	resp := h.do(http.MethodPost, "/api/chats/resume", protocol.ResumeChatRequest{
		WorkspaceID: ws.WorkspaceID, SessionID: "../../etc/passwd",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for an unlisted session id, got %d", resp.StatusCode)
	}
}

func TestResumeRestoresStoredHistory(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()
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
		WorkspaceID: ws.WorkspaceID, SessionID: "sess-seeded",
	})
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

func TestLiveSessionsListsEveryProject(t *testing.T) {
	h := newHarness(t)

	empty := decodeJSON[[]protocol.SessionSummary](t,
		h.do(http.MethodGet, "/api/sessions/live", nil))
	if len(empty) != 0 {
		t.Fatalf("expected no live sessions, got %+v", empty)
	}

	paths := []string{filepath.Join(h.root, "project-a"), filepath.Join(h.root, "project-b")}
	canonicalPaths := make([]string, 0, len(paths))
	refs := make([]protocol.ChatRef, 0, len(paths))
	for _, path := range paths {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		ws := decodeJSON[protocol.Workspace](t, h.do(http.MethodPost, "/api/workspaces/open",
			protocol.OpenWorkspaceRequest{Path: path}))
		canonicalPaths = append(canonicalPaths, ws.Path)
		ref := decodeJSON[protocol.ChatRef](t, h.do(http.MethodPost, "/api/chats",
			protocol.CreateChatRequest{WorkspaceID: ws.WorkspaceID}))
		refs = append(refs, ref)
	}

	live := decodeJSON[[]protocol.SessionSummary](t,
		h.do(http.MethodGet, "/api/sessions/live", nil))
	if len(live) != 2 {
		t.Fatalf("expected live sessions from both projects, got %+v", live)
	}
	gotPaths := map[string]bool{}
	for _, session := range live {
		if !session.Live || session.ChatID == "" || session.RunState == nil {
			t.Fatalf("global session is missing live status: %+v", session)
		}
		if *session.RunState != protocol.RunStateIdle {
			t.Fatalf("new session should be idle: %+v", session)
		}
		gotPaths[session.WorkingDir] = true
	}
	for _, path := range canonicalPaths {
		if !gotPaths[path] {
			t.Fatalf("missing live session for %s: %+v", path, live)
		}
	}

	h.fake.Delay = time.Second
	resp := h.do(http.MethodPost, "/api/chats/"+refs[0].ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/notool keep working", Mode: protocol.DeliveryNormal})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send: %d", resp.StatusCode)
	}
	deadline := time.Now().Add(time.Second)
	for {
		live = decodeJSON[[]protocol.SessionSummary](t,
			h.do(http.MethodGet, "/api/sessions/live", nil))
		running := false
		for _, session := range live {
			running = running || session.SessionID == refs[0].SessionID &&
				session.RunState != nil && *session.RunState == protocol.RunStateRunning
		}
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("executing session was never marked running: %+v", live)
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}

	resp = h.do(http.MethodDelete, "/api/chats/"+refs[0].ChatID, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispose: %d", resp.StatusCode)
	}
	remaining := decodeJSON[[]protocol.SessionSummary](t,
		h.do(http.MethodGet, "/api/sessions/live", nil))
	if len(remaining) != 1 || remaining[0].SessionID != refs[1].SessionID {
		t.Fatalf("disposed session remained live: %+v", remaining)
	}
}

// TestSingleOwnership: a second open of the same session attaches to the live
// chat instead of creating a second writer.
func TestSingleOwnership(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()
	h.fake.Seed("sess-own", "Owned", ws.Path, nil)

	first := decodeJSON[protocol.ChatRef](t, h.do(http.MethodPost, "/api/chats/resume",
		protocol.ResumeChatRequest{WorkspaceID: ws.WorkspaceID, SessionID: "sess-own"}))
	second := decodeJSON[protocol.ChatRef](t, h.do(http.MethodPost, "/api/chats/resume",
		protocol.ResumeChatRequest{WorkspaceID: ws.WorkspaceID, SessionID: "sess-own"}))
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
	req, _ := http.NewRequestWithContext(h.t.Context(), http.MethodGet, h.srv.URL+"/api/chats/"+chatID+"/events", http.NoBody)
	if lastEventID > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatUint(lastEventID, 10))
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
	ref, _ := h.newChat()
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
	ref, _ := h.newChat()
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
	ref, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)

	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/confirm list the files", Mode: protocol.DeliveryNormal}).Body.Close()

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
	ref, _ := h.newChat()
	resp := h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/tool-confirmation",
		protocol.ToolConfirmationReply{ToolCallID: "nope", Decision: protocol.DecisionApprove})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestElicitationCorrelatedByID(t *testing.T) {
	h := newHarness(t)
	ref, _ := h.newChat()
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
		protocol.ElicitationReply{
			ElicitationID: req.ElicitationID, Action: protocol.ElicitAccept,
			Content: map[string]any{"branch": "main"},
		})
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
	ref, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)

	// A stale steer hint received while idle starts a normal turn.
	idleDispatch := decodeJSON[protocol.Accepted](t, h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/notool start", Mode: protocol.DeliverySteer}))
	if idleDispatch.Mode != protocol.DeliveryNormal || idleDispatch.Queued {
		t.Fatalf("idle dispatch = %+v, want a normal turn", idleDispatch)
	}

	// A stale normal hint received while that turn is running steers it.
	runningDispatch := decodeJSON[protocol.Accepted](t, h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "actually, do this", Mode: protocol.DeliveryNormal}))
	if runningDispatch.Mode != protocol.DeliverySteer || !runningDispatch.Queued {
		t.Fatalf("running dispatch = %+v, want a steer", runningDispatch)
	}

	// An explicit follow-up remains distinct while running.
	followUp := decodeJSON[protocol.Accepted](t, h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "then do another turn", Mode: protocol.DeliveryFollowUp}))
	if followUp.Mode != protocol.DeliveryFollowUp || !followUp.Queued {
		t.Fatalf("follow-up dispatch = %+v", followUp)
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
	ref, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)

	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/notool slow", Mode: protocol.DeliveryNormal}).Body.Close()
	sse.collect(func(e protocol.Event) bool {
		return e.Type == protocol.EventRunStatus && e.Run != nil && e.Run.State == protocol.RunStateRunning
	}, 3*time.Second)
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

func TestReconnectReplayWithoutDuplicates(t *testing.T) {
	h := newHarness(t)
	ref, _ := h.newChat()
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
	ref, _ := h.newChat()
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
	ref, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)
	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/confirm run it", Mode: protocol.DeliveryNormal}).Body.Close()
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

func TestConfigChanges(t *testing.T) {
	h := newHarness(t)
	ref, _ := h.newChat()

	model := "fake/model-b"
	resp := h.do(http.MethodPatch, "/api/chats/"+ref.ChatID+"/config",
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

func TestBootstrapWarnsThatToolsAreAutoApproved(t *testing.T) {
	h := newHarness(t)
	b := decodeJSON[protocol.Bootstrap](t, h.do(http.MethodGet, "/api/bootstrap", nil))
	var warned bool
	for _, n := range b.Notices {
		if n.Code == "tools_auto_approved" {
			warned = true
		}
	}
	if !warned {
		t.Fatal("automatic tool approval must be stated plainly in bootstrap")
	}
}

func TestToolsRunWithoutConfirmationDialog(t *testing.T) {
	h := newHarness(t)
	ref, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)

	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "list the files", Mode: protocol.DeliveryNormal}).Body.Close()

	events := sse.collect(func(e protocol.Event) bool {
		return e.Type == protocol.EventRunStatus && e.Run != nil && e.Run.State == protocol.RunStateIdle
	}, 6*time.Second)

	for _, e := range events {
		if e.Type == protocol.EventToolConfirmation {
			t.Fatal("a normal tool call must not raise a confirmation dialog")
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

func TestRetitleCompactStatsAndDispose(t *testing.T) {
	h := newHarness(t)
	ref, _ := h.newChat()

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
	ref, _ := h.newChat()
	sse := h.openSSE(ref.ChatID, 0)
	sse.next(3 * time.Second)
	h.do(http.MethodPost, "/api/chats/"+ref.ChatID+"/messages",
		protocol.SendMessageRequest{Text: "/confirm run it", Mode: protocol.DeliveryNormal}).Body.Close()
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
	ref, ws := h.newChat()
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
		t.Fatalf("expected workspace-only chat creation to work, got %d %s", resp.StatusCode, body)
	}
	ref := decodeJSON[protocol.ChatRef](t, resp)

	snap := decodeJSON[protocol.Snapshot](t, h.do(http.MethodGet, "/api/chats/"+ref.ChatID, nil))
	// Model and thinking controls must still be available.
	models := decodeJSON[[]protocol.ModelOption](t, h.do(http.MethodGet, "/api/chats/"+ref.ChatID+"/models", nil))
	if len(models) == 0 {
		t.Fatal("the default agent must still expose model choices")
	}
	if len(snap.Meta.ThinkingLevels) == 0 {
		t.Fatal("the default agent must still expose thinking levels")
	}
}

func TestBootstrapDoesNotAdvertiseAgents(t *testing.T) {
	h := newHarness(t)
	b := decodeJSON[map[string]any](t, h.do(http.MethodGet, "/api/bootstrap", nil))
	for _, field := range []string{"defaultAgent", "builtinAgents", "agentSourceHints", "workspaceRoots"} {
		if _, exists := b[field]; exists {
			t.Fatalf("bootstrap must not expose obsolete %q", field)
		}
	}
}

func TestResumeUsesDashboardAgent(t *testing.T) {
	h := newHarness(t)
	ws := h.openWorkspace()
	h.fake.Seed("sess-default", "Older", ws.Path, nil)
	resp := h.do(http.MethodPost, "/api/chats/resume",
		protocol.ResumeChatRequest{WorkspaceID: ws.WorkspaceID, SessionID: "sess-default"})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("resume with the dashboard agent: %d %s", resp.StatusCode, body)
	}
}

func TestUnknownRoutes(t *testing.T) {
	h := newHarness(t)
	if r := h.do(http.MethodGet, "/api/nope", nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", r.StatusCode)
	}
}
