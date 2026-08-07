package workspaces

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/rumpl/daw/internal/pathsec"
	"github.com/rumpl/daw/internal/protocol"
)

type Workspace struct {
	ID   string
	Path string
}

// Service is the authority for validated workspace paths and their persisted
// most-recently-used list.
type Service struct {
	mu          sync.Mutex
	guard       *pathsec.Guard
	historyFile string
	log         *slog.Logger
	entries     map[string]*Workspace
	hints       []protocol.WorkspaceHint
}

func New(guard *pathsec.Guard, historyFile string, log *slog.Logger) *Service {
	s := &Service{
		guard: guard, historyFile: historyFile, log: log,
		entries: map[string]*Workspace{},
	}
	s.loadHistory()
	return s
}

func (s *Service) Open(path string) (*Workspace, error) {
	canon, err := s.guard.ResolveDir(path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.entries {
		if entry.Path == canon {
			s.rememberLocked(canon)
			return entry, nil
		}
	}
	entry := &Workspace{ID: newID(), Path: canon}
	s.entries[entry.ID] = entry
	s.rememberLocked(canon)
	return entry, nil
}

func (s *Service) Get(id string) (*Workspace, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[id]
	return entry, ok
}

func (s *Service) Add(id, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[id] = &Workspace{ID: id, Path: path}
}

func (s *Service) Hints() []protocol.WorkspaceHint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]protocol.WorkspaceHint(nil), s.hints...)
}

func (s *Service) Path(id string) (string, bool) {
	entry, ok := s.Get(id)
	if !ok {
		return "", false
	}
	return entry.Path, true
}

func (s *Service) rememberLocked(path string) {
	next := make([]protocol.WorkspaceHint, 0, min(len(s.hints)+1, maxWorkspaceHints))
	next = append(next, protocol.WorkspaceHint{Path: path, Label: filepath.Base(path)})
	for _, hint := range s.hints {
		if hint.Path == path {
			continue
		}
		next = append(next, hint)
		if len(next) == maxWorkspaceHints {
			break
		}
	}
	s.hints = next
	if s.historyFile != "" {
		if err := writeWorkspaceHistory(s.historyFile, s.hints); err != nil {
			s.log.Warn("could not persist workspace history", "error", err)
		}
	}
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "ws_" + hex.EncodeToString(b)
}

const (
	workspaceHistoryVersion = 1
	maxWorkspaceHints       = 10
	maxWorkspaceHistorySize = 64 << 10
)

type workspaceHistory struct {
	Version int      `json:"version"`
	Paths   []string `json:"paths"`
}

// loadHistory restores the server-wide project list. Every stored path is
// resolved again: deleted projects and paths outside the user's home directory
// are not advertised to browsers.
func (s *Service) loadHistory() {
	if s.historyFile == "" {
		return
	}

	history, err := readWorkspaceHistory(s.historyFile)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		s.log.Warn("could not read workspace history", "error", err)
		return
	}

	seen := make(map[string]bool, len(history.Paths))
	for _, path := range history.Paths {
		canon, err := s.guard.ResolveDir(path)
		if err != nil || seen[canon] {
			continue
		}
		seen[canon] = true
		s.hints = append(s.hints, protocol.WorkspaceHint{
			Path: canon, Label: filepath.Base(canon),
		})
		if len(s.hints) == maxWorkspaceHints {
			break
		}
	}
}

func readWorkspaceHistory(path string) (workspaceHistory, error) {
	var history workspaceHistory
	f, err := os.Open(path)
	if err != nil {
		return history, err
	}
	defer f.Close()

	limited := io.LimitReader(f, maxWorkspaceHistorySize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return history, err
	}
	if len(data) > maxWorkspaceHistorySize {
		return history, fmt.Errorf("workspace history exceeds %d bytes", maxWorkspaceHistorySize)
	}
	if err := json.Unmarshal(data, &history); err != nil {
		return history, fmt.Errorf("decode workspace history: %w", err)
	}
	if history.Version != workspaceHistoryVersion {
		return history, fmt.Errorf("unsupported workspace history version %d", history.Version)
	}
	return history, nil
}

// writeWorkspaceHistory uses a same-directory rename so a crash can leave at
// most the previous complete list. Paths are private host information, hence
// both the directory and file are owner-only when they are created here.
func writeWorkspaceHistory(path string, hints []protocol.WorkspaceHint) (retErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create workspace history directory: %w", err)
	}

	paths := make([]string, 0, len(hints))
	for _, hint := range hints {
		paths = append(paths, hint.Path)
	}
	data, err := json.Marshal(workspaceHistory{Version: workspaceHistoryVersion, Paths: paths})
	if err != nil {
		return fmt.Errorf("encode workspace history: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".dawui-workspaces-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary workspace history: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if retErr != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("protect workspace history: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write workspace history: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync workspace history: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close workspace history: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace workspace history: %w", err)
	}
	return nil
}
