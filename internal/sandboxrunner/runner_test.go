package sandboxrunner

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultNameIsStableAndPathSafe(t *testing.T) {
	workspace := filepath.Join(string(filepath.Separator), "Users", "someone", "work trees", "demo_repo")
	got := DefaultName(workspace)
	if !strings.HasPrefix(got, "daw-demo-repo-") {
		t.Fatalf("DefaultName() = %q", got)
	}
	if strings.ContainsAny(got, `/\\ _`) {
		t.Fatalf("DefaultName() contains a path-unsafe character: %q", got)
	}
	if got != DefaultName(workspace) {
		t.Fatalf("DefaultName() is not stable: %q != %q", got, DefaultName(workspace))
	}
}

func TestDefaultNameSeparatesSameBasename(t *testing.T) {
	first := DefaultName(filepath.Join("one", "project"))
	second := DefaultName(filepath.Join("two", "project"))
	if first == second {
		t.Fatalf("different workspaces received the same name %q", first)
	}
}
