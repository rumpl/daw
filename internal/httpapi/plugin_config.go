package httpapi

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

const maxPluginConfigBytes = 64 << 10

var pluginConfigID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type pluginConfigStore struct {
	mu  sync.Mutex
	dir string
}

func newPluginConfigStore(dir string) *pluginConfigStore { return &pluginConfigStore{dir: dir} }

func (s *pluginConfigStore) path(pluginID string) (string, error) {
	if s.dir == "" || !pluginConfigID.MatchString(pluginID) {
		return "", os.ErrNotExist
	}
	return filepath.Join(s.dir, pluginID+".json"), nil
}

func (s *pluginConfigStore) get(pluginID string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(pluginID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil || len(data) > maxPluginConfigBytes {
		return nil, errors.New("plugin configuration could not be read")
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil || value == nil {
		return nil, errors.New("plugin configuration is invalid")
	}
	return value, nil
}

func (s *pluginConfigStore) set(pluginID string, value map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(pluginID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > maxPluginConfigBytes {
		return errors.New("plugin configuration is too large")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
