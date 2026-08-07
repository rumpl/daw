package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	chatPreferencesVersion = 1
	maxChatPreferencesSize = 1 << 20 // 1 MiB
	maxSessionPreferences  = 2048
)

// chatPreference is deliberately small and non-secret. A per-session entry
// preserves the controls when that session is resumed; Default makes the most
// recently selected values the starting point for a brand-new chat.
type chatPreference struct {
	Model         string `json:"model,omitempty"`
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type chatPreferences struct {
	Version  int                       `json:"version"`
	Default  chatPreference            `json:"default"`
	Sessions map[string]chatPreference `json:"sessions,omitempty"`
}

func (s *Server) loadChatPreferences() {
	s.preferences = chatPreferences{
		Version:  chatPreferencesVersion,
		Sessions: map[string]chatPreference{},
	}
	if s.chatPreferencesFile == "" {
		return
	}

	preferences, err := readChatPreferences(s.chatPreferencesFile)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		s.log.Warn("could not read chat preferences", "error", err)
		return
	}
	s.preferences = preferences
}

func readChatPreferences(path string) (chatPreferences, error) {
	var preferences chatPreferences
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
	if preferences.Version != chatPreferencesVersion {
		return preferences, fmt.Errorf("unsupported chat preferences version %d", preferences.Version)
	}
	if preferences.Sessions == nil {
		preferences.Sessions = map[string]chatPreference{}
	}
	preferences.Default = sanitizeChatPreference(preferences.Default)
	for id, preference := range preferences.Sessions {
		if strings.TrimSpace(id) == "" {
			delete(preferences.Sessions, id)
			continue
		}
		preferences.Sessions[id] = sanitizeChatPreference(preference)
	}
	return preferences, nil
}

func sanitizeChatPreference(preference chatPreference) chatPreference {
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
	return preference
}

// chatPreference returns the persisted defaults for a new chat, or the exact
// per-session choice for a resumed chat. Defaults are intentionally not laid
// over old sessions: sessions without a dashboard override retain their own
// docker-agent configuration.
func (s *Server) chatPreference(sessionID string) chatPreference {
	s.preferencesMu.Lock()
	defer s.preferencesMu.Unlock()
	if sessionID == "" {
		return s.preferences.Default
	}
	return s.preferences.Sessions[sessionID]
}

// rememberChatPreference atomically updates both the session's choice and the
// defaults used by future chats. Holding preferencesMu through the rename
// serializes concurrent browser updates, so an older write cannot replace a
// newer complete snapshot.
func (s *Server) rememberChatPreference(sessionID string, patch chatPreference) error {
	if s.chatPreferencesFile == "" {
		return nil
	}
	patch = sanitizeChatPreference(patch)
	if sessionID == "" || (patch.Model == "" && patch.ThinkingLevel == "") {
		return nil
	}

	s.preferencesMu.Lock()
	defer s.preferencesMu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	merge := func(current chatPreference) chatPreference {
		if patch.Model != "" {
			current.Model = patch.Model
		}
		if patch.ThinkingLevel != "" {
			current.ThinkingLevel = patch.ThinkingLevel
		}
		current.UpdatedAt = now
		return current
	}
	s.preferences.Default = merge(s.preferences.Default)
	s.preferences.Sessions[sessionID] = merge(s.preferences.Sessions[sessionID])
	pruneChatPreferences(s.preferences.Sessions)
	return writeChatPreferences(s.chatPreferencesFile, s.preferences)
}

func pruneChatPreferences(sessions map[string]chatPreference) {
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
func writeChatPreferences(path string, preferences chatPreferences) (retErr error) {
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
