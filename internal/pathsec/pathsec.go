// Package pathsec performs filesystem-aware containment checks for working
// directories selected by the browser.
//
// Containment is *not* a string-prefix test. Both the roots and the candidate
// are canonicalized with filepath.EvalSymlinks and compared component-wise, so
// "/home/user-evil" is not inside "/home/user" and a symlink pointing out of a
// root is rejected rather than followed.
package pathsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrOutsideRoots means the path is real but not inside any allowed root.
	ErrOutsideRoots = errors.New("path is outside the allowed workspace roots")
	// ErrNotDirectory means the path exists but is not a directory.
	ErrNotDirectory = errors.New("path is not a directory")
	// ErrNotAbsolute means a relative path was supplied.
	ErrNotAbsolute = errors.New("path must be absolute")
	// ErrMissing means the path does not exist.
	ErrMissing = errors.New("path does not exist")
	// ErrNoRoots means no usable root could be configured.
	ErrNoRoots = errors.New("no allowed workspace roots are configured")
)

// Guard holds the canonicalized set of allowed roots.
type Guard struct {
	roots []string // canonical, cleaned
}

// NewGuard canonicalizes the supplied roots. Roots that do not exist or are
// not directories are skipped (and reported in skipped) rather than making the
// whole server unusable.
func NewGuard(roots []string) (*Guard, []string, error) {
	g := &Guard{}
	var skipped []string
	seen := map[string]bool{}
	for _, r := range roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		abs, err := filepath.Abs(expandHome(r))
		if err != nil {
			skipped = append(skipped, r)
			continue
		}
		canon, err := filepath.EvalSymlinks(abs)
		if err != nil {
			skipped = append(skipped, r)
			continue
		}
		fi, err := os.Stat(canon)
		if err != nil || !fi.IsDir() {
			skipped = append(skipped, r)
			continue
		}
		canon = filepath.Clean(canon)
		if seen[canon] {
			continue
		}
		seen[canon] = true
		g.roots = append(g.roots, canon)
	}
	if len(g.roots) == 0 {
		return nil, skipped, ErrNoRoots
	}
	return g, skipped, nil
}

// Roots returns the canonical workspace roots.
func (g *Guard) Roots() []string { return append([]string(nil), g.roots...) }

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~"+string(os.PathSeparator)) || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), string(os.PathSeparator)))
		}
	}
	return p
}

// ResolveDir validates that candidate is an existing directory contained by an
// allowed root and returns its canonical path.
func (g *Guard) ResolveDir(candidate string) (string, error) {
	canon, err := g.resolve(candidate)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(canon)
	if err != nil {
		return "", ErrMissing
	}
	if !fi.IsDir() {
		return "", ErrNotDirectory
	}
	return canon, nil
}

func (g *Guard) resolve(candidate string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", ErrMissing
	}
	candidate = expandHome(candidate)
	if !filepath.IsAbs(candidate) {
		return "", ErrNotAbsolute
	}
	if strings.ContainsRune(candidate, 0) {
		return "", ErrMissing
	}
	canon, err := filepath.EvalSymlinks(filepath.Clean(candidate))
	if err != nil {
		return "", ErrMissing
	}
	canon = filepath.Clean(canon)
	for _, root := range g.roots {
		if isDescendant(root, canon) {
			return canon, nil
		}
	}
	return "", fmt.Errorf("%w", ErrOutsideRoots)
}

// isDescendant reports whether child is root itself or lives beneath it,
// comparing whole path components (never raw string prefixes).
func isDescendant(root, child string) bool {
	rel, err := filepath.Rel(root, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if filepath.IsAbs(rel) {
		return false
	}
	return true
}

// HomeRoots returns the only workspace root supported by the dashboard.
func HomeRoots() []string {
	if home, err := os.UserHomeDir(); err == nil {
		return []string{home}
	}
	return nil
}
