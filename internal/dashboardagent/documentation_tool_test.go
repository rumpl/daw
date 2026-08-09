package dashboardagent

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/tools"
)

func TestDeveloperDocumentationToolReturnsCompleteReference(t *testing.T) {
	tool := DeveloperDocumentationTool()
	if tool.Name != "get_dashboard_developer_documentation" {
		t.Fatalf("tool name = %q", tool.Name)
	}
	if !tool.Annotations.ReadOnlyHint || tool.Handler == nil || tool.Parameters == nil {
		t.Fatal("documentation tool must be read-only and fully defined")
	}
	result, err := tool.Handler(t.Context(), tools.ToolCall{
		Function: tools.FunctionCall{Name: tool.Name, Arguments: `{}`},
	}, tools.NopRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.Output != developerDocumentation {
		t.Fatal("tool did not return the embedded documentation verbatim")
	}
	if len(result.Output) < 10_000 {
		t.Fatalf("developer reference is unexpectedly short: %d bytes", len(result.Output))
	}
}

func TestDeveloperDocumentationCoversEveryBackendRoute(t *testing.T) {
	serverSource, err := os.ReadFile("../httpapi/server.go")
	if err != nil {
		t.Fatal(err)
	}
	routePattern := regexp.MustCompile(`m\.HandleFunc\("([^"]+)"`)
	matches := routePattern.FindAllStringSubmatch(string(serverSource), -1)
	if len(matches) == 0 {
		t.Fatal("found no HTTP routes in server.go")
	}
	for _, match := range matches {
		route := match[1]
		if !strings.Contains(developerDocumentation, route) {
			t.Errorf("developer documentation is missing backend route %q", route)
		}
	}
}

func TestDeveloperDocumentationCoversEveryExposedHostComponentAndHook(t *testing.T) {
	for _, name := range []string{
		"Chat", "Markdown", "Mermaid", "ChatHeader", "Composer", "Conversation",
		"ElicitationDialog", "ToolConfirmDialog", "ModelPicker", "PendingDialogs", "ToolCard",
		"useChat", "useDraft",
	} {
		if !strings.Contains(developerDocumentation, name) {
			t.Errorf("developer documentation is missing frontend API %q", name)
		}
	}
}
