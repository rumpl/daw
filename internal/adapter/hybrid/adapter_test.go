package hybrid

import (
	"context"
	"testing"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/adapter/fake"
	"github.com/rumpl/daw/internal/protocol"
)

func TestListSessionsUsesHostCatalogAndTagsTargets(t *testing.T) {
	host := fake.New()
	sandbox := fake.New()
	host.SeedWithAttributes("host-session", "Host", "/workspace", map[string]string{adapter.ExecutionTargetAttribute: "host"}, nil)
	host.SeedWithAttributes("sandbox-session", "Sandbox", "/workspace", map[string]string{adapter.ExecutionTargetAttribute: "sandbox"}, nil)
	hybrid, err := New(host, sandbox, protocol.ExecutionTargetSandbox)
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := hybrid.ListSessions(context.Background(), "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("ListSessions() returned %d sessions", len(sessions))
	}
	got := map[string]string{}
	for _, session := range sessions {
		got[session.SessionID] = session.Attributes[adapter.ExecutionTargetAttribute]
		if string(session.ExecutionTarget) != got[session.SessionID] {
			t.Fatalf("session %q wire target %q != attribute %q", session.SessionID, session.ExecutionTarget, got[session.SessionID])
		}
	}
	if got["host-session"] != "host" || got["sandbox-session"] != "sandbox" {
		t.Fatalf("execution targets = %#v", got)
	}
}

func TestOpenChatSelectsTargetAndTagsMetadata(t *testing.T) {
	host := fake.New()
	sandbox := fake.New()
	hybrid, err := New(host, sandbox, protocol.ExecutionTargetSandbox)
	if err != nil {
		t.Fatal(err)
	}

	chat, err := hybrid.OpenChat(context.Background(), adapter.OpenRequest{
		ChatID: "chat-host", WorkingDir: "/workspace", ExecutionTarget: protocol.ExecutionTargetHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer chat.Close(context.Background())
	if host.LastOpenRequest.ChatID != "chat-host" || sandbox.LastOpenRequest.ChatID != "" {
		t.Fatalf("request routed to wrong adapter: host=%q sandbox=%q", host.LastOpenRequest.ChatID, sandbox.LastOpenRequest.ChatID)
	}
	if got := chat.Meta().Attributes[adapter.ExecutionTargetAttribute]; got != "host" {
		t.Fatalf("chat target attribute = %q", got)
	}
	if got := chat.Meta().ExecutionTarget; got != protocol.ExecutionTargetHost {
		t.Fatalf("chat wire target = %q", got)
	}
}

func TestResumeRetainsOriginalTarget(t *testing.T) {
	host := fake.New()
	sandbox := fake.New()
	host.SeedWithAttributes("existing", "Existing", "/workspace", map[string]string{adapter.ExecutionTargetAttribute: "sandbox"}, nil)
	sandbox.Seed("existing", "Existing", "/workspace", nil)
	hybrid, err := New(host, sandbox, protocol.ExecutionTargetHost)
	if err != nil {
		t.Fatal(err)
	}

	chat, err := hybrid.OpenChat(context.Background(), adapter.OpenRequest{
		ChatID: "resume", WorkingDir: "/workspace", ResumeSessionID: "existing",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer chat.Close(context.Background())
	if sandbox.LastOpenRequest.ResumeSessionID != "existing" || host.LastOpenRequest.ResumeSessionID != "" {
		t.Fatalf("resume routed to wrong adapter: host=%q sandbox=%q", host.LastOpenRequest.ResumeSessionID, sandbox.LastOpenRequest.ResumeSessionID)
	}
}
