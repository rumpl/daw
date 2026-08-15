package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rumpl/daw/internal/adapter/fake"
	"github.com/rumpl/daw/internal/chatprefs"
	"github.com/rumpl/daw/internal/pathsec"
)

func newPreferenceTestServer(t *testing.T, root, preferencesFile string) *Server {
	t.Helper()
	guard, _, err := pathsec.NewGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{
		Adapter:             fake.New(),
		Guard:               guard,
		ChatPreferencesFile: preferencesFile,
		Logger:              slog.New(slog.DiscardHandler),
	})
}

func TestChatPreferencesPersistDefaultsAndPerSessionChoices(t *testing.T) {
	root := t.TempDir()
	preferencesFile := filepath.Join(t.TempDir(), "dawui-chat-preferences.json")

	first := newPreferenceTestServer(t, root, preferencesFile)
	if err := first.preferences.Remember("session-a", chatprefs.Preference{Model: "fake/model-b"}); err != nil {
		t.Fatal(err)
	}
	if err := first.preferences.Remember("session-a", chatprefs.Preference{ThinkingLevel: "high"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(preferencesFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("preferences mode = %o, want 600", info.Mode().Perm())
	}

	// Constructing another server simulates a process restart and reads only
	// the file, not the first server's in-memory state.
	second := newPreferenceTestServer(t, root, preferencesFile)
	for name, got := range map[string]chatprefs.Preference{
		"new chat":        second.preferences.Get(""),
		"resumed session": second.preferences.Get("session-a"),
	} {
		if got.Model != "fake/model-b" || got.ThinkingLevel != "high" {
			t.Fatalf("%s preference = %+v", name, got)
		}
	}

	// A session which never selected dashboard overrides must retain the
	// docker-agent session's own values instead of inheriting a later default.
	if got := second.preferences.Get("older-session"); got.Model != "" || got.ThinkingLevel != "" {
		t.Fatalf("unconfigured resumed session inherited defaults: %+v", got)
	}
}

func TestOpenChatRestoresPreferencesAndBindsDefaultsToSession(t *testing.T) {
	root := t.TempDir()
	preferencesFile := filepath.Join(t.TempDir(), "preferences.json")
	first := newPreferenceTestServer(t, root, preferencesFile)
	if err := first.preferences.Remember("previous-session", chatprefs.Preference{
		Model: "fake/model-b", ThinkingLevel: "high",
	}); err != nil {
		t.Fatal(err)
	}

	first.workspaces.Add("ws", root)
	recorder := httptest.NewRecorder()
	first.openChat(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/chats", http.NoBody), "ws", "", "", nil, "", "")
	if recorder.Code != 201 {
		t.Fatalf("open new chat: %d %s", recorder.Code, recorder.Body.String())
	}
	var ref struct {
		ChatID    string `json:"chatId"`
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&ref); err != nil {
		t.Fatal(err)
	}
	opened, ok := first.chat(ref.ChatID)
	if !ok {
		t.Fatal("new chat was not registered")
	}
	meta := opened.snapshot().Meta
	if meta.Model != "fake/model-b" || meta.ThinkingLevel != "high" {
		t.Fatalf("new chat did not receive persisted defaults: %+v", meta)
	}
	if bound := first.preferences.Get(ref.SessionID); bound.Model != "fake/model-b" || bound.ThinkingLevel != "high" {
		t.Fatalf("inherited defaults were not bound to the new session: %+v", bound)
	}
	first.Shutdown(t.Context())

	// Simulate docker-agent loading the same session with its configured
	// defaults. The fresh server must layer the sidecar choices back on.
	second := newPreferenceTestServer(t, root, preferencesFile)
	secondFake := second.adapter.(*fake.Adapter)
	secondFake.Seed(ref.SessionID, "Existing", root, nil)
	second.workspaces.Add("ws", root)
	recorder = httptest.NewRecorder()
	second.openChat(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/chats/resume", http.NoBody), "ws", ref.SessionID, "", nil, "", "")
	if recorder.Code != 201 {
		t.Fatalf("resume chat: %d %s", recorder.Code, recorder.Body.String())
	}
	var resumedRef struct {
		ChatID string `json:"chatId"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&resumedRef); err != nil {
		t.Fatal(err)
	}
	resumed, ok := second.chat(resumedRef.ChatID)
	if !ok {
		t.Fatal("resumed chat was not registered")
	}
	meta = resumed.snapshot().Meta
	if meta.Model != "fake/model-b" || meta.ThinkingLevel != "high" {
		t.Fatalf("resumed chat did not restore its preferences: %+v", meta)
	}
	second.Shutdown(t.Context())
}

func TestDefaultToolOptionsCanBeReadAndUpdatedWithoutAChat(t *testing.T) {
	root := t.TempDir()
	preferencesFile := filepath.Join(t.TempDir(), "preferences.json")
	s := newPreferenceTestServer(t, root, preferencesFile)

	request := httptest.NewRequest(http.MethodPatch, "/api/chat-options/tools/shell", strings.NewReader(`{"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("tool", "shell")
	recorder := httptest.NewRecorder()
	s.handleUpdateDefaultTool(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update default tool: %d %s", recorder.Code, recorder.Body.String())
	}

	options, err := s.resolveChatOptions(t.Context(), s.preferences.Get(""))
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range options.Tools {
		if tool.Name == "shell" {
			if tool.Enabled {
				t.Fatal("shell remained enabled")
			}
			return
		}
	}
	t.Fatal("shell was absent from default tool options")
}

func TestGlobalToolFilterUpdatesLiveChats(t *testing.T) {
	root := t.TempDir()
	s := newPreferenceTestServer(t, root, "")
	s.workspaces.Add("ws", root)
	recorder := httptest.NewRecorder()
	s.openChat(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/chats", http.NoBody), "ws", "", "", nil, "", "")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open chat: %d %s", recorder.Code, recorder.Body.String())
	}

	request := httptest.NewRequest(http.MethodPatch, "/api/chat-options/tools/shell", strings.NewReader(`{"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("tool", "shell")
	recorder = httptest.NewRecorder()
	s.handleUpdateDefaultTool(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update global tool: %d %s", recorder.Code, recorder.Body.String())
	}
	got := s.adapter.(*fake.Adapter).LastDisabledTools
	if len(got) != 1 || got[0] != "shell" {
		t.Fatalf("live runtime filter = %v, want [shell]", got)
	}
}

func TestDefaultToolOptionsRejectUnknownTool(t *testing.T) {
	s := newPreferenceTestServer(t, t.TempDir(), "")
	request := httptest.NewRequest(http.MethodPatch, "/api/chat-options/tools/missing", strings.NewReader(`{"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("tool", "missing")
	recorder := httptest.NewRecorder()
	s.handleUpdateDefaultTool(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown tool status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestChatPreferencesMergeIndependentControls(t *testing.T) {
	root := t.TempDir()
	preferencesFile := filepath.Join(t.TempDir(), "preferences.json")
	s := newPreferenceTestServer(t, root, preferencesFile)

	if err := s.preferences.Remember("one", chatprefs.Preference{Model: "fake/model-b", ThinkingLevel: "low"}); err != nil {
		t.Fatal(err)
	}
	if err := s.preferences.Remember("two", chatprefs.Preference{ThinkingLevel: "high"}); err != nil {
		t.Fatal(err)
	}

	one := s.preferences.Get("one")
	if one.Model != "fake/model-b" || one.ThinkingLevel != "low" {
		t.Fatalf("session one was overwritten: %+v", one)
	}
	two := s.preferences.Get("two")
	if two.Model != "" || two.ThinkingLevel != "high" {
		t.Fatalf("session two should contain only its own patch: %+v", two)
	}
	def := s.preferences.Get("")
	if def.Model != "fake/model-b" || def.ThinkingLevel != "high" {
		t.Fatalf("new-chat defaults did not merge latest independent choices: %+v", def)
	}
}
