package pathsec_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rumpl/daw/internal/pathsec"
)

func mustGuard(t *testing.T, roots ...string) *pathsec.Guard {
	t.Helper()
	g, _, err := pathsec.NewGuard(roots)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	return g
}

func TestResolveDirInsideRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "project")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	g := mustGuard(t, root)
	got, err := g.ResolveDir(sub)
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	canonSub, _ := filepath.EvalSymlinks(sub)
	if got != canonSub {
		t.Fatalf("got %q want %q", got, canonSub)
	}
}

// TestPathPrefixTrap is the classic "/home/user" vs "/home/user-evil" case:
// a string-prefix check would wrongly allow the sibling.
func TestPathPrefixTrap(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "user")
	evil := filepath.Join(base, "user-evil")
	for _, d := range []string{root, evil} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g := mustGuard(t, root)
	if _, err := g.ResolveDir(evil); !errors.Is(err, pathsec.ErrOutsideRoots) {
		t.Fatalf("expected ErrOutsideRoots, got %v", err)
	}
}

func TestTraversalRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g := mustGuard(t, root)
	if _, err := g.ResolveDir(filepath.Join(root, "..", "outside")); !errors.Is(err, pathsec.ErrOutsideRoots) {
		t.Fatalf("expected ErrOutsideRoots, got %v", err)
	}
}

// TestSymlinkEscape ensures a symlink inside the root pointing outside it is
// rejected after canonicalization, not followed.
func TestSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	base := t.TempDir()
	root := filepath.Join(base, "root")
	secret := filepath.Join(base, "secret")
	for _, d := range []string{root, secret} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	g := mustGuard(t, root)
	if _, err := g.ResolveDir(link); !errors.Is(err, pathsec.ErrOutsideRoots) {
		t.Fatalf("expected ErrOutsideRoots for symlink escape, got %v", err)
	}
}

func TestSymlinkWithinRootAllowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	g := mustGuard(t, root)
	got, err := g.ResolveDir(link)
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	canonReal, _ := filepath.EvalSymlinks(real)
	if got != canonReal {
		t.Fatalf("got %q want %q", got, canonReal)
	}
}

func TestResolveFile(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "agent.yaml")
	if err := os.WriteFile(f, []byte("agents: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := mustGuard(t, root)
	if _, err := g.ResolveFile(f); err != nil {
		t.Fatalf("ResolveFile: %v", err)
	}
	if _, err := g.ResolveFile(root); !errors.Is(err, pathsec.ErrNotFile) {
		t.Fatalf("expected ErrNotFile for a directory, got %v", err)
	}
	// An agent config outside the roots is refused: an agent config is
	// executable configuration.
	outside := filepath.Join(t.TempDir(), "evil.yaml")
	if err := os.WriteFile(outside, []byte("agents: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ResolveFile(outside); !errors.Is(err, pathsec.ErrOutsideRoots) {
		t.Fatalf("expected ErrOutsideRoots, got %v", err)
	}
}

func TestRelativeAndMissingRejected(t *testing.T) {
	root := t.TempDir()
	g := mustGuard(t, root)
	if _, err := g.ResolveDir("relative/path"); !errors.Is(err, pathsec.ErrNotAbsolute) {
		t.Fatalf("expected ErrNotAbsolute, got %v", err)
	}
	if _, err := g.ResolveDir(filepath.Join(root, "nope")); !errors.Is(err, pathsec.ErrMissing) {
		t.Fatalf("expected ErrMissing, got %v", err)
	}
	if _, err := g.ResolveDir(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestSkippedRootsReported(t *testing.T) {
	root := t.TempDir()
	_, skipped, err := pathsec.NewGuard([]string{root, filepath.Join(root, "missing")})
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped root, got %v", skipped)
	}
}

func TestNoRoots(t *testing.T) {
	if _, _, err := pathsec.NewGuard(nil); !errors.Is(err, pathsec.ErrNoRoots) {
		t.Fatalf("expected ErrNoRoots, got %v", err)
	}
}
