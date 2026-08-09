package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/rumpl/daw/internal/plugins"
	"github.com/rumpl/daw/internal/protocol"
)

// pluginManagement keeps operator intent separate from plugin source. Enabled
// is persisted (and controls startup); running is process-local.
type pluginManagement struct {
	mu       sync.RWMutex
	path     string
	disabled map[string]bool
	stopped  map[string]bool
}

type pluginManagementState struct {
	Disabled []string `json:"disabled"`
}

func newPluginManagement(dataDir string) *pluginManagement {
	m := &pluginManagement{
		path:     filepath.Join(dataDir, "plugin-management.json"),
		disabled: map[string]bool{}, stopped: map[string]bool{},
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		return m
	}
	var state pluginManagementState
	if json.Unmarshal(data, &state) == nil {
		for _, id := range state.Disabled {
			m.disabled[id] = true
			m.stopped[id] = true
		}
	}
	return m
}

func (m *pluginManagement) enabled(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return !m.disabled[id]
}

func (m *pluginManagement) running(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return !m.stopped[id]
}

func (m *pluginManagement) setRunning(id string, running bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if running {
		delete(m.stopped, id)
	} else {
		m.stopped[id] = true
	}
}

func (m *pluginManagement) setEnabled(id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if enabled {
		delete(m.disabled, id)
	} else {
		m.disabled[id] = true
	}
	return m.saveLocked()
}

func (m *pluginManagement) remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.disabled, id)
	delete(m.stopped, id)
	return m.saveLocked()
}

func (m *pluginManagement) saveLocked() error {
	state := pluginManagementState{Disabled: []string{}}
	for id := range m.disabled {
		state.Disabled = append(state.Disabled, id)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (s *Server) allPlugins() protocol.PluginCatalog { return plugins.Catalog(s.pluginDir) }

func (s *Server) pluginCatalog() protocol.PluginCatalog {
	catalog := s.allPlugins()
	active := catalog.Plugins[:0]
	for _, plugin := range catalog.Plugins {
		if s.pluginManagement.running(plugin.ID) {
			active = append(active, plugin)
		}
	}
	catalog.Plugins = active
	return catalog
}

func (s *Server) managedPlugin(id string) (protocol.ManagedPlugin, bool) {
	for _, plugin := range s.allPlugins().Plugins {
		if plugin.ID == id {
			return protocol.ManagedPlugin{Plugin: plugin, Enabled: s.pluginManagement.enabled(id), Running: s.pluginManagement.running(id)}, true
		}
	}
	return protocol.ManagedPlugin{}, false
}

func (s *Server) publishPluginsChanged() {
	s.events.publish(protocol.DashboardEvent{Type: protocol.DashboardEventPluginsChanged, Revision: pluginCatalogRevision(s.pluginCatalog())})
}

func (s *Server) handlePluginManagement(w http.ResponseWriter, _ *http.Request) {
	catalog := s.allPlugins()
	result := protocol.PluginManagementCatalog{Plugins: []protocol.ManagedPlugin{}, Errors: catalog.Errors}
	for _, plugin := range catalog.Plugins {
		result.Plugins = append(result.Plugins, protocol.ManagedPlugin{
			Plugin: plugin, Enabled: s.pluginManagement.enabled(plugin.ID), Running: s.pluginManagement.running(plugin.ID),
		})
	}
	s.json(w, http.StatusOK, result)
}

func (s *Server) handlePluginLifecycle(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("pluginId"), filepath.Base(r.URL.Path)
	if _, ok := s.managedPlugin(id); !ok {
		s.fail(w, http.StatusNotFound, "plugin_not_found", "plugin not found")
		return
	}
	switch action {
	case "start":
		if !s.pluginManagement.enabled(id) {
			s.fail(w, http.StatusConflict, "plugin_disabled", "enable the plugin before starting it")
			return
		}
		s.pluginManagement.setRunning(id, true)
	case "stop":
		s.pluginManagement.setRunning(id, false)
		s.backends.stopPlugin(id, "stopped by user")
	case "enable":
		if err := s.pluginManagement.setEnabled(id, true); err != nil {
			s.fail(w, http.StatusInternalServerError, "plugin_management_failed", "plugin state could not be saved")
			return
		}
	case "disable":
		if err := s.pluginManagement.setEnabled(id, false); err != nil {
			s.fail(w, http.StatusInternalServerError, "plugin_management_failed", "plugin state could not be saved")
			return
		}
		s.pluginManagement.setRunning(id, false)
		s.backends.stopPlugin(id, "disabled by user")
	default:
		s.fail(w, http.StatusNotFound, "not_found", "no such endpoint")
		return
	}
	s.backends.activateAll()
	s.publishPluginsChanged()
	managed, _ := s.managedPlugin(id)
	s.json(w, http.StatusOK, managed)
}

func (s *Server) handleDeletePlugin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("pluginId")
	if _, ok := s.managedPlugin(id); !ok {
		s.fail(w, http.StatusNotFound, "plugin_not_found", "plugin not found")
		return
	}
	s.pluginManagement.setRunning(id, false)
	s.backends.stopPlugin(id, "plugin deleted")
	if err := os.RemoveAll(filepath.Join(s.pluginDir, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.fail(w, http.StatusInternalServerError, "plugin_delete_failed", "plugin could not be deleted")
		return
	}
	if err := s.pluginManagement.remove(id); err != nil {
		s.log.Warn("clear deleted plugin state", "plugin", id, "error", err)
	}
	s.publishPluginsChanged()
	s.json(w, http.StatusOK, protocol.Accepted{Accepted: true})
}
