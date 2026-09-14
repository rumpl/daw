package dashboardagent

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/skills"
)

func TestDeveloperDocumentationSkillReturnsCompleteReference(t *testing.T) {
	skill := DeveloperDocumentationSkill()
	if skill.Name != developerDocumentationSkillName {
		t.Fatalf("skill name = %q", skill.Name)
	}
	if skill.Description == "" || !skill.IsInline() {
		t.Fatal("documentation skill must be described and defined inline")
	}
	if !strings.Contains(skill.InlineContent, developerDocumentation) {
		t.Fatal("documentation skill did not include the embedded reference")
	}
	if len(skill.InlineContent) < 10_000 {
		t.Fatalf("developer reference is unexpectedly short: %d bytes", len(skill.InlineContent))
	}
}

func TestDashboardSkillsAddsDocumentationAlongsideLocalSkills(t *testing.T) {
	loaded := []skills.Skill{
		{Name: "other", Description: "another auto-loaded skill"},
		{Name: developerDocumentationSkillName, Description: "local collision", FilePath: "/tmp/SKILL.md"},
	}

	result := dashboardSkills(loaded)
	if len(result) != 2 {
		t.Fatalf("dashboardSkills returned %d skills, want 2", len(result))
	}
	if result[0].Name != developerDocumentationSkillName || !result[0].IsInline() {
		t.Fatal("built-in documentation skill must be present and win name collisions")
	}
	if result[1].Name != "other" {
		t.Fatalf("existing skill was not preserved: %#v", result[1])
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
		"ModelPicker", "ToolCard",
		"useChat", "useDraft",
	} {
		if !strings.Contains(developerDocumentation, name) {
			t.Errorf("developer documentation is missing frontend API %q", name)
		}
	}
}
