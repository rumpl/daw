package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rumpl/daw/internal/adapter/fake"
	"github.com/rumpl/daw/internal/pathsec"
	"github.com/rumpl/daw/internal/protocol"
)

func TestWorkspaceHistoryPersistsAndIsSharedThroughBootstrap(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	historyFile := filepath.Join(t.TempDir(), "dawui-workspaces.json")
	canonicalFirst, err := filepath.EvalSymlinks(first)
	if err != nil {
		t.Fatal(err)
	}
	canonicalSecond, err := filepath.EvalSymlinks(second)
	if err != nil {
		t.Fatal(err)
	}

	start := func() (*Server, *httptest.Server) {
		t.Helper()
		guard, _, err := pathsec.NewGuard([]string{root})
		if err != nil {
			t.Fatal(err)
		}
		s := New(Options{
			Adapter: fake.New(), Guard: guard, WorkspaceHistoryFile: historyFile,
		})
		return s, httptest.NewServer(s)
	}
	open := func(s *Server, ts *httptest.Server, path string) {
		t.Helper()
		body, err := json.Marshal(protocol.OpenWorkspaceRequest{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/workspaces/open", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(CSRFHeader, s.CSRFToken())
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("open %s: status %d", path, resp.StatusCode)
		}
	}
	bootstrap := func(ts *httptest.Server) protocol.Bootstrap {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/bootstrap")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b protocol.Bootstrap
		if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	stop := func(s *Server, ts *httptest.Server) {
		t.Helper()
		ts.Close()
		s.Shutdown(context.Background())
	}

	s1, ts1 := start()
	open(s1, ts1, first)
	open(s1, ts1, second)
	open(s1, ts1, first) // reopening promotes it to the front
	stop(s1, ts1)

	if info, err := os.Stat(historyFile); err != nil {
		t.Fatalf("history file: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("history mode = %o, want 600", info.Mode().Perm())
	}

	// A fresh server (and therefore a fresh browser bootstrap) sees the same
	// MRU list without relying on localStorage.
	s2, ts2 := start()
	b := bootstrap(ts2)
	if len(b.WorkspaceHints) != 2 {
		t.Fatalf("workspace hints = %#v", b.WorkspaceHints)
	}
	if b.WorkspaceHints[0].Path != canonicalFirst || b.WorkspaceHints[1].Path != canonicalSecond {
		t.Fatalf("workspace hint order = %#v, want first then second", b.WorkspaceHints)
	}
	stop(s2, ts2)

	// Stored paths are never trusted blindly after a restart.
	if err := os.RemoveAll(second); err != nil {
		t.Fatal(err)
	}
	s3, ts3 := start()
	defer stop(s3, ts3)
	b = bootstrap(ts3)
	if len(b.WorkspaceHints) != 1 || b.WorkspaceHints[0].Path != canonicalFirst {
		t.Fatalf("stale workspace was advertised: %#v", b.WorkspaceHints)
	}
}
