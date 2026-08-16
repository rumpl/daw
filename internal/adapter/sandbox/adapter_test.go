package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sbx "github.com/rumpl/go-sbx"
)

func TestBootstrapAndOptionsDoNotStartSandbox(t *testing.T) {
	a, err := New(Config{
		Client:    sbx.New(sbx.WithBinary(filepath.Join(t.TempDir(), "must-not-run-sbx"))),
		Workspace: t.TempDir(), Kit: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.Info(t.Context())
	if err != nil || !info.ModelsAvailable {
		t.Fatalf("Info() = %#v, %v", info, err)
	}
	models, thinking, tools, err := a.ChatOptions(t.Context(), "", nil)
	if err != nil || len(models) != 0 || len(tools) != 0 || len(thinking) == 0 {
		t.Fatalf("ChatOptions() = %#v, %#v, %#v, %v", models, thinking, tools, err)
	}
}

func TestSessionSandboxNameSeparatesSessions(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	first := sessionSandboxName(workspace, "chat-a")
	if first == sessionSandboxName(workspace, "chat-b") {
		t.Fatal("different sessions received the same sandbox name")
	}
	if first != sessionSandboxName(workspace, "chat-a") {
		t.Fatal("session sandbox name is not stable")
	}
}

func TestIndexContainsLifecycleMetadataOnly(t *testing.T) {
	workspace := t.TempDir()
	kit := t.TempDir()
	index := filepath.Join(t.TempDir(), "sessions.json")
	a, err := New(Config{Workspace: workspace, Kit: kit, IndexFile: index})
	if err != nil {
		t.Fatal(err)
	}
	a.records["session"] = &record{SessionID: "session", Sandbox: "daw-session-test", WorkingDir: "/worktrees/child"}
	if err := a.saveLocked(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := New(Config{Workspace: workspace, Kit: kit, IndexFile: index})
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.records["session"]
	if got == nil || got.WorkingDir != "/worktrees/child" {
		t.Fatalf("reloaded record = %#v", got)
	}
	data, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "summary") || strings.Contains(string(data), "attributes") {
		t.Fatalf("lifecycle index contains catalog data: %s", data)
	}
}

func TestWithinAllowsWorkspaceChildrenButNotSiblingWorktrees(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if !within(root, filepath.Join(root, "nested")) {
		t.Fatal("workspace child was not covered by the workspace mount")
	}
	if within(root, filepath.Join(filepath.Dir(root), "repo-worktree")) {
		t.Fatal("sibling worktree was incorrectly covered by the workspace mount")
	}
}
