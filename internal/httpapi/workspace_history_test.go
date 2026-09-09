package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/api/workspaces/open", bytes.NewReader(body))
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
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/api/bootstrap", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
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
		s.Shutdown(t.Context())
	}

	s1, ts1 := start()
	open(s1, ts1, first)
	open(s1, ts1, second)
	open(s1, ts1, first) // reopening promotes it to the front

	folderBody, err := json.Marshal(protocol.UpdateProjectFoldersRequest{Folders: []protocol.ProjectFolder{{
		ID: "work", Name: "Work", Paths: []string{canonicalFirst},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	folderReq, err := http.NewRequestWithContext(t.Context(), http.MethodPut, ts1.URL+"/api/project-folders", bytes.NewReader(folderBody))
	if err != nil {
		t.Fatal(err)
	}
	folderReq.Header.Set("Content-Type", "application/json")
	folderReq.Header.Set(CSRFHeader, s1.CSRFToken())
	folderReq.Header.Set("Sec-Fetch-Site", "same-origin")
	folderResp, err := http.DefaultClient.Do(folderReq)
	if err != nil {
		t.Fatal(err)
	}
	folderResp.Body.Close()
	if folderResp.StatusCode != http.StatusOK {
		t.Fatalf("update folders: status %d", folderResp.StatusCode)
	}

	removeURL := ts1.URL + "/api/workspaces?path=" + url.QueryEscape(canonicalSecond)
	removeReq, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, removeURL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	removeReq.Header.Set(CSRFHeader, s1.CSRFToken())
	removeReq.Header.Set("Sec-Fetch-Site", "same-origin")
	removeResp, err := http.DefaultClient.Do(removeReq)
	if err != nil {
		t.Fatal(err)
	}
	removeResp.Body.Close()
	if removeResp.StatusCode != http.StatusOK {
		t.Fatalf("remove workspace: status %d", removeResp.StatusCode)
	}
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
	if len(b.WorkspaceHints) != 1 || b.WorkspaceHints[0].Path != canonicalFirst {
		t.Fatalf("workspace hints after removal = %#v", b.WorkspaceHints)
	}
	if len(b.ProjectFolders) != 1 || b.ProjectFolders[0].Name != "Work" || len(b.ProjectFolders[0].Paths) != 1 || b.ProjectFolders[0].Paths[0] != canonicalFirst {
		t.Fatalf("project folders after restart = %#v", b.ProjectFolders)
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
