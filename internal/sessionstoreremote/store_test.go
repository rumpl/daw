package sessionstoreremote

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/session/sqlitestore"
	"github.com/rumpl/daw/internal/sessionstorebridge"
	"github.com/rumpl/daw/internal/stdiomux"
)

func TestRemoteStoreContractAndFidelity(t *testing.T) {
	host := session.NewInMemorySessionStore()
	bridge, err := sessionstorebridge.New(sessionstorebridge.Config{Store: host, Token: "secret", Target: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(bridge)
	defer server.Close()
	remote, err := New(Config{URL: server.URL, Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Check(t.Context()); err != nil {
		t.Fatal(err)
	}

	root := session.New(session.WithTitle("original"), session.WithWorkingDir("/workspace"), session.WithAttributes(map[string]string{"daw.execution.target": "host", "lineage.root": "root"}))
	root.Messages = []session.Item{{Message: session.UserMessage("hello", chat.MessagePart{Type: chat.MessagePartTypeText, Text: "attachment text"})}}
	if err := remote.AddSession(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	stored, err := remote.GetSession(t.Context(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AttributesSnapshot()["daw.execution.target"] != "sandbox" || stored.AttributesSnapshot()["lineage.root"] != "root" {
		t.Fatalf("attributes = %#v", stored.AttributesSnapshot())
	}
	if len(stored.Messages) != 1 || stored.Messages[0].Message.Message.Content != "hello" || len(stored.Messages[0].Message.Message.MultiContent) != 1 {
		t.Fatalf("initial message did not round trip: %#v", stored.Messages)
	}

	message := session.NewAgentMessage("root", &chat.Message{Role: chat.MessageRoleAssistant, Content: "partial", Model: "provider/model"})
	messageID, err := remote.AddMessage(t.Context(), root.ID, message)
	if err != nil || messageID == 0 {
		t.Fatalf("AddMessage() = %d, %v", messageID, err)
	}
	message.Message.Content = "final"
	if err := remote.UpdateMessage(t.Context(), messageID, message); err != nil {
		t.Fatal(err)
	}
	if err := remote.AddSummary(t.Context(), root.ID, session.Item{Summary: "summary", FirstKeptEntry: 1, Cost: 0.25, Model: "provider/model", Usage: &chat.Usage{InputTokens: 3, OutputTokens: 4}}); err != nil {
		t.Fatal(err)
	}
	if err := remote.AddError(t.Context(), root.ID, &session.Error{Message: "recorded", Code: "test"}); err != nil {
		t.Fatal(err)
	}

	child := session.New(session.WithTitle("child"), session.WithAttributes(map[string]string{"daw.execution.target": "host", "lineage.parent": root.ID}))
	if err := remote.AddSubSession(t.Context(), root.ID, child); err != nil {
		t.Fatal(err)
	}
	if err := remote.UpdateSessionTitle(t.Context(), root.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := remote.UpdateSessionTokens(t.Context(), root.ID, 10, 20, 1.5); err != nil {
		t.Fatal(err)
	}
	if err := remote.SetSessionStarred(t.Context(), root.ID, true); err != nil {
		t.Fatal(err)
	}

	stored, err = remote.GetSession(t.Context(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TitleSnapshot() != "renamed" || !stored.Starred || stored.InputTokens != 10 || stored.OutputTokens != 20 || stored.Cost != 1.5 {
		t.Fatalf("metadata = %#v", stored)
	}
	if len(stored.Messages) != 5 {
		t.Fatalf("items = %d, want 5: %#v", len(stored.Messages), stored.Messages)
	}
	if got := stored.Messages[1].Message.Message.Content; got != "final" {
		t.Fatalf("updated message = %q", got)
	}
	if got := stored.Messages[2]; got.Summary != "summary" || got.FirstKeptEntry != 1 || got.Usage == nil || got.Usage.OutputTokens != 4 {
		t.Fatalf("summary = %#v", got)
	}
	if got := stored.Messages[3].Error; got == nil || got.Code != "test" {
		t.Fatalf("error = %#v", got)
	}
	if got := stored.Messages[4].SubSession; got == nil || got.ParentID != root.ID || got.AttributesSnapshot()["daw.execution.target"] != "sandbox" {
		t.Fatalf("child = %#v", got)
	}

	sessions, err := remote.GetSessions(t.Context())
	if err != nil || len(sessions) == 0 {
		t.Fatalf("GetSessions() = %d, %v", len(sessions), err)
	}
	summaries, err := remote.GetSessionSummaries(t.Context())
	if err != nil || len(summaries) != 1 || summaries[0].Title != "renamed" || summaries[0].NumMessages != 2 {
		t.Fatalf("summaries = %#v, %v", summaries, err)
	}

	stored.SetAttribute("daw.execution.target", "host")
	stored.SetAttribute("custom", "preserved")
	if err := remote.UpdateSession(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	stored, err = host.GetSession(t.Context(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AttributesSnapshot()["daw.execution.target"] != "sandbox" || stored.AttributesSnapshot()["custom"] != "preserved" {
		t.Fatalf("updated attributes = %#v", stored.AttributesSnapshot())
	}

	if err := remote.DeleteSession(t.Context(), root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.GetSession(t.Context(), root.ID); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("GetSession after delete = %v", err)
	}
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing a runner-side client must not close the authoritative host store.
	probe := session.New(session.WithTitle("probe"))
	if err := host.AddSession(t.Context(), probe); err != nil {
		t.Fatalf("host store was closed: %v", err)
	}
}

func TestRemoteStoreOverReverseStdioMux(t *testing.T) {
	hostRead, runnerWrite := io.Pipe()
	runnerRead, hostWrite := io.Pipe()
	hostPeer, err := stdiomux.New(hostRead, hostWrite, stdiomux.Host)
	if err != nil {
		t.Fatal(err)
	}
	runnerPeer, err := stdiomux.New(runnerRead, runnerWrite, stdiomux.Runner)
	if err != nil {
		t.Fatal(err)
	}
	defer hostPeer.Close()
	defer runnerPeer.Close()
	hostStore := session.NewInMemorySessionStore()
	bridge, err := sessionstorebridge.New(sessionstorebridge.Config{Store: hostStore, Token: "secret", Target: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: bridge}
	go func() { _ = server.Serve(hostPeer) }()
	defer server.Close()
	remote, err := New(Config{URL: "http://session-store", Token: "secret", DialContext: runnerPeer.DialContext})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	if err := remote.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	value := session.New(session.WithTitle("stdio"))
	if err := remote.AddSession(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	stored, err := hostStore.GetSession(t.Context(), value.ID)
	if err != nil || stored.AttributesSnapshot()["daw.execution.target"] != "sandbox" {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
}

func TestRemoteStoreOverSQLite(t *testing.T) {
	host, err := sqlitestore.New(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	bridge, err := sessionstorebridge.New(sessionstorebridge.Config{Store: host, Token: "secret", Target: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(bridge)
	defer server.Close()
	remote, err := New(Config{URL: server.URL, Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	value := session.New(session.WithTitle("SQLite"), session.WithAttributes(map[string]string{"origin": "test"}))
	if err := remote.AddSession(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	id, err := remote.AddMessage(t.Context(), value.ID, session.UserMessage("persisted"))
	if err != nil || id == 0 {
		t.Fatalf("AddMessage() = %d, %v", id, err)
	}
	stored, err := remote.GetSession(t.Context(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MessageCount() != 1 || stored.AttributesSnapshot()["daw.execution.target"] != "sandbox" || stored.AttributesSnapshot()["origin"] != "test" {
		t.Fatalf("stored session = %#v", stored)
	}
}

func TestRemoteStoreRejectsBadCredentials(t *testing.T) {
	bridge, err := sessionstorebridge.New(sessionstorebridge.Config{Store: session.NewInMemorySessionStore(), Token: "right"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(bridge)
	defer server.Close()
	remote, err := New(Config{URL: server.URL, Token: "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Check(t.Context()); err == nil {
		t.Fatal("Check succeeded with bad credentials")
	}
}
