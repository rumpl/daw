package chatprefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Service persists dashboard-only chat controls. It owns all synchronization
// and file I/O for preferences.
type Service struct {
	mu    sync.Mutex
	file  string
	log   *slog.Logger
	state preferences
}

func New(file string, log *slog.Logger) *Service {
	s := &Service{file: file, log: log}
	s.load()
	return s
}

func (s *Service) load() {
	s.state = preferences{
		Version: preferencesVersion, Sessions: map[string]Preference{},
	}
	if s.file == "" {
		return
	}
	preferences, err := read(s.file)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		s.log.Warn("could not read chat preferences", "error", err)
		return
	}
	s.state = preferences
}

func (s *Service) Get(sessionID string) Preference {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" {
		return clonePreference(s.state.Default)
	}
	preference := clonePreference(s.state.Sessions[sessionID])
	preference.DisabledTools = append([]string(nil), s.state.Default.DisabledTools...)
	preference.ExecutionTarget = s.state.Default.ExecutionTarget
	return preference
}

// UpdateDefault changes the process-wide choices inherited by new chats.
// Nil fields are unchanged and an explicit empty string clears a choice.
func (s *Service) UpdateDefault(model, thinkingLevel *string) (Preference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.state.Default
	if model != nil {
		current.Model = strings.TrimSpace(*model)
	}
	if thinkingLevel != nil {
		current.ThinkingLevel = strings.TrimSpace(*thinkingLevel)
	}
	current = sanitizeChatPreference(current)
	current.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.state.Default = current
	if s.file == "" {
		return current, nil
	}
	if err := write(s.file, s.state); err != nil {
		return Preference{}, err
	}
	return current, nil
}

// SetExecutionTarget changes the backend-owned default inherited by new chats.
func (s *Service) SetExecutionTarget(target string) (Preference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.state.Default
	current.ExecutionTarget = sanitizeExecutionTarget(target)
	current.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.state.Default = current
	if s.file != "" {
		if err := write(s.file, s.state); err != nil {
			return Preference{}, err
		}
	}
	return clonePreference(current), nil
}

func (s *Service) Remember(sessionID string, patch Preference) error {
	if s.file == "" {
		return nil
	}
	patch = sanitizeChatPreference(patch)
	if sessionID == "" || (patch.Model == "" && patch.ThinkingLevel == "") {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	merge := func(current Preference) Preference {
		if patch.Model != "" {
			current.Model = patch.Model
		}
		if patch.ThinkingLevel != "" {
			current.ThinkingLevel = patch.ThinkingLevel
		}
		current.UpdatedAt = now
		return current
	}
	s.state.Default = merge(s.state.Default)
	s.state.Sessions[sessionID] = merge(s.state.Sessions[sessionID])
	pruneChatPreferences(s.state.Sessions)
	return write(s.file, s.state)
}

func clonePreference(preference Preference) Preference {
	preference.DisabledTools = append([]string(nil), preference.DisabledTools...)
	return preference
}

// SetToolEnabled changes one entry in the process-wide exclusion set without
// dropping disabled plugin tools that are absent from the current workspace.
func (s *Service) SetToolEnabled(name string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.state.Default
	disabled := append([]string(nil), current.DisabledTools...)
	if enabled {
		disabled = slices.DeleteFunc(disabled, func(item string) bool { return item == name })
	} else {
		disabled = append(disabled, name)
	}
	current.DisabledTools = sanitizeToolNames(disabled)
	current.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.state.Default = current
	if s.file == "" {
		return nil
	}
	return write(s.file, s.state)
}

// UpdateTools replaces the process-wide exclusion set inherited by every
// newly opened or resumed chat.
func (s *Service) UpdateTools(disabled []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.state.Default
	current.DisabledTools = sanitizeToolNames(disabled)
	current.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.state.Default = current
	if s.file == "" {
		return nil
	}
	return write(s.file, s.state)
}

const (
	preferencesVersion     = 1
	maxChatPreferencesSize = 1 << 20 // 1 MiB
	maxSessionPreferences  = 2048
)

// Preference is deliberately small and non-secret. Model and ThinkingLevel
// can have per-session values; DisabledTools and ExecutionTarget are meaningful
// only on Default and are inherited process-wide by every chat.
type Preference struct {
	Model           string   `json:"model,omitempty"`
	ThinkingLevel   string   `json:"thinkingLevel,omitempty"`
	DisabledTools   []string `json:"disabledTools,omitempty"`
	ExecutionTarget string   `json:"executionTarget,omitempty"`
	UpdatedAt       string   `json:"updatedAt,omitempty"`
}

type preferences struct {
	Version  int                   `json:"version"`
	Default  Preference            `json:"default"`
	Sessions map[string]Preference `json:"sessions,omitempty"`
}

func read(path string) (preferences, error) {
	var preferences preferences
	f, err := os.Open(path)
	if err != nil {
		return preferences, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxChatPreferencesSize+1))
	if err != nil {
		return preferences, err
	}
	if len(data) > maxChatPreferencesSize {
		return preferences, fmt.Errorf("chat preferences exceed %d bytes", maxChatPreferencesSize)
	}
	if err := json.Unmarshal(data, &preferences); err != nil {
		return preferences, fmt.Errorf("decode chat preferences: %w", err)
	}
	if preferences.Version != preferencesVersion {
		return preferences, fmt.Errorf("unsupported chat preferences version %d", preferences.Version)
	}
	if preferences.Sessions == nil {
		preferences.Sessions = map[string]Preference{}
	}
	preferences.Default = sanitizeChatPreference(preferences.Default)
	// DisabledTools was briefly stored per session. Fold those values into the
	// global set when reading so existing users keep their choices.
	globalTools := append([]string(nil), preferences.Default.DisabledTools...)
	for id, preference := range preferences.Sessions {
		if strings.TrimSpace(id) == "" {
			delete(preferences.Sessions, id)
			continue
		}
		preference = sanitizeChatPreference(preference)
		globalTools = append(globalTools, preference.DisabledTools...)
		preference.DisabledTools = nil
		preferences.Sessions[id] = preference
	}
	preferences.Default.DisabledTools = sanitizeToolNames(globalTools)
	return preferences, nil
}

func sanitizeChatPreference(preference Preference) Preference {
	preference.Model = strings.TrimSpace(preference.Model)
	preference.ThinkingLevel = strings.TrimSpace(preference.ThinkingLevel)
	// Corrupt or hand-edited files must not feed unbounded strings into model
	// resolution or logs. Valid model refs and effort names are far smaller.
	if len(preference.Model) > 512 {
		preference.Model = ""
	}
	if len(preference.ThinkingLevel) > 64 {
		preference.ThinkingLevel = ""
	}
	preference.DisabledTools = sanitizeToolNames(preference.DisabledTools)
	preference.ExecutionTarget = sanitizeExecutionTarget(preference.ExecutionTarget)
	return preference
}

func sanitizeExecutionTarget(target string) string {
	switch strings.TrimSpace(target) {
	case "host":
		return "host"
	case "sandbox":
		return "sandbox"
	default:
		return ""
	}
}

func sanitizeToolNames(names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 256 || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func pruneChatPreferences(sessions map[string]Preference) {
	if len(sessions) <= maxSessionPreferences {
		return
	}
	type entry struct {
		id        string
		updatedAt string
	}
	entries := make([]entry, 0, len(sessions))
	for id, preference := range sessions {
		entries = append(entries, entry{id: id, updatedAt: preference.UpdatedAt})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].updatedAt == entries[j].updatedAt {
			return entries[i].id < entries[j].id
		}
		return entries[i].updatedAt < entries[j].updatedAt
	})
	for _, item := range entries[:len(entries)-maxSessionPreferences] {
		delete(sessions, item.id)
	}
}

// writeChatPreferences follows the same owner-only, same-directory atomic
// replacement scheme as workspace history. A crash leaves either the old
// complete file or the new complete file, never a partially written setting.
func write(path string, preferences preferences) (retErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create chat preferences directory: %w", err)
	}
	data, err := json.Marshal(preferences)
	if err != nil {
		return fmt.Errorf("encode chat preferences: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".dawui-chat-preferences-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary chat preferences: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if retErr != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("protect chat preferences: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write chat preferences: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync chat preferences: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close chat preferences: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace chat preferences: %w", err)
	}
	return nil
}
