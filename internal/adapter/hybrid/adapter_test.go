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

// The host is the authority for the gateway, but a sandbox's docker-agent has
// its own user configuration, so a write must reach both runtimes.
func TestSetModelsGatewayReachesHostAndSandbox(t *testing.T) {
	host := fake.New()
	sandbox := fake.New()
	router, err := New(host, sandbox, protocol.ExecutionTargetSandbox)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.SetModelsGateway(t.Context(), "https://gateway.example.com"); err != nil {
		t.Fatal(err)
	}
	hostValue, err := host.ModelsGateway(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sandboxValue, err := sandbox.ModelsGateway(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if hostValue != "https://gateway.example.com" {
		t.Fatalf("host gateway = %q", hostValue)
	}
	if sandboxValue != "https://gateway.example.com" {
		t.Fatalf("sandbox gateway = %q; the runner would ignore the gateway", sandboxValue)
	}
}

// Reads come from the host so the two runtimes can never appear to disagree.
func TestModelsGatewayReadsTheHost(t *testing.T) {
	host := fake.New()
	sandbox := fake.New()
	router, err := New(host, sandbox, protocol.ExecutionTargetSandbox)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.SetModelsGateway(t.Context(), "https://host.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.SetModelsGateway(t.Context(), "https://drifted.example.com"); err != nil {
		t.Fatal(err)
	}
	got, err := router.ModelsGateway(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://host.example.com" {
		t.Fatalf("ModelsGateway() = %q; want the host value", got)
	}
}
