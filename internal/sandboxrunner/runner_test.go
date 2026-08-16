package sandboxrunner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sbx "github.com/rumpl/go-sbx"
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

func TestWithinDetectsAlreadyMountedExecutionDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if !within(root, filepath.Join(root, "nested-worktree")) {
		t.Fatal("nested execution directory should be covered by the workspace mount")
	}
	if within(root, filepath.Join(filepath.Dir(root), "sibling-worktree")) {
		t.Fatal("sibling worktree should require an additional mount")
	}
}

func TestStartRunnerUsesPostRunExecContext(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	binary := filepath.Join(dir, "sbx")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >" + argsFile + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	process, err := startRunner(context.Background(), sbx.New(sbx.WithBinary(binary)), "session-one", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(args)
	for _, want := range []string{"exec\n", "session-one\n", "DAW_SESSION_STORE_TOKEN", "start-daw-runner"} {
		if !strings.Contains(got, want) {
			t.Fatalf("start command %q does not contain %q", got, want)
		}
	}
}

func TestStageKitOmitsRunnerWhenTemplateContainsIt(t *testing.T) {
	kit := t.TempDir()
	binary := filepath.Join(kit, "files", "home", ".local", "lib", "daw-runner")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kit, "spec.yaml"), []byte("schemaVersion: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged, cleanup, err := stageKit(kit, strings.Repeat("a", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(staged, "files", "home", ".local", "lib", "daw-runner")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("template-baked runner remains in staged kit: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(staged, "files", "home", ".config", "daw", "runner-token")); err != nil || strings.TrimSpace(string(data)) != strings.Repeat("a", 64) {
		t.Fatalf("staged token = %q, %v", data, err)
	}
}
