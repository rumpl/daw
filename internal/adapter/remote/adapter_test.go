package remote_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/adapter/fake"
	"github.com/rumpl/daw/internal/adapter/remote"
	"github.com/rumpl/daw/internal/protocol"
	"github.com/rumpl/daw/internal/runnerapi"
	"github.com/rumpl/daw/internal/sessionlineage"
	"github.com/rumpl/daw/internal/stdiomux"
)

func TestAdapterRoundTripAndMCPCallbackRewrite(t *testing.T) {
	const token = "test-runner-token"
	local := fake.New()
	api := runnerapi.New(local, token)
	server := httptest.NewServer(api)
	t.Cleanup(func() { server.Close(); api.Shutdown(context.Background()) })

	client, err := remote.New(remote.Config{
		Endpoint: server.URL, Token: token,
		CallbackOrigin: "http://host.docker.internal:4788", CallbackToken: "bridge-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.Info(t.Context())
	if err != nil || info.AgentVersion != "fake" {
		t.Fatalf("Info() = %#v, %v", info, err)
	}

	lineage := sessionlineage.Origin{
		ParentSessionID: "parent-session", RootSessionID: "root-session",
		Kind: sessionlineage.KindAgent, PluginID: "agent-gossip",
	}.Attributes()
	chat, err := client.OpenChat(t.Context(), adapter.OpenRequest{
		ChatID: "chat-1", WorkingDir: t.TempDir(), SessionAttributes: lineage,
		MCPServers: []adapter.MCPServer{{
			Name: "plugin", Command: "/plugin/mcp", Env: []string{
				"KEEP=value", "DAW_API_ORIGIN=http://127.0.0.1:4788", "DAW_API_SOCKET=/tmp/daw.sock",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = chat.Close(context.Background()) })
	if got := chat.Meta().Attributes; got[sessionlineage.AttributeParentSessionID] != "parent-session" {
		t.Fatalf("open-chat lineage attributes = %q", got)
	}
	sessions, err := client.ListSessions(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Attributes[sessionlineage.AttributeRootSessionID] != "root-session" {
		t.Fatalf("listed session lineage attributes = %#v", sessions)
	}

	got := local.LastOpenRequest.MCPServers[0].Env
	want := map[string]bool{
		"KEEP=value": true, "DAW_API_ORIGIN=http://host.docker.internal:4788": true,
		"DAW_API_TOKEN=bridge-secret": true, "DAW_PLUGIN_TOKEN=bridge-secret": true,
	}
	if len(got) != len(want) {
		t.Fatalf("rewritten MCP env = %q", got)
	}
	for _, value := range got {
		if !want[value] {
			t.Fatalf("unexpected rewritten MCP env value %q in %q", value, got)
		}
	}

	if _, _, _, err := chat.Send(t.Context(), "hello", nil, protocol.DeliveryNormal); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	metaSeen := false
	for {
		select {
		case event, ok := <-chat.Events():
			if !ok {
				t.Fatal("event stream closed before the fake turn started")
			}
			if event.Type == protocol.EventSessionMeta && event.Meta != nil {
				if event.Meta.Attributes[sessionlineage.AttributePluginID] != "agent-gossip" {
					t.Fatalf("streamed session lineage attributes = %q", event.Meta.Attributes)
				}
				metaSeen = true
			}
			if event.Type == protocol.EventMessageItem && metaSeen {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for an event through the runner transport")
		}
	}
}

func TestAdapterOverStdioMux(t *testing.T) {
	hostRead, runnerWrite := io.Pipe()
	runnerRead, hostWrite := io.Pipe()
	host, err := stdiomux.New(hostRead, hostWrite, stdiomux.Host)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := stdiomux.New(runnerRead, runnerWrite, stdiomux.Runner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(); _ = runner.Close() })
	api := runnerapi.New(fake.New(), "secret")
	server := &http.Server{Handler: api}
	go func() { _ = server.Serve(runner) }()
	t.Cleanup(func() { _ = server.Close(); api.Shutdown(context.Background()) })
	client, err := remote.New(remote.Config{Endpoint: "http://runner", Token: "secret", DialContext: host.DialContext})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	info, err := client.Info(t.Context())
	if err != nil || info.AgentVersion != "fake" {
		t.Fatalf("Info() = %#v, %v", info, err)
	}
	chat, err := client.OpenChat(t.Context(), adapter.OpenRequest{ChatID: "stdio-chat", WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer chat.Close(context.Background())
	if _, _, _, err := chat.Send(t.Context(), "hello", nil, protocol.DeliveryNormal); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-chat.Events():
		if event.Type == "" {
			t.Fatal("empty streamed event")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for stdio event stream")
	}
}

func TestRunnerRejectsMissingToken(t *testing.T) {
	api := runnerapi.New(fake.New(), "secret")
	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}
