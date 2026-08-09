package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rumpl/daw/internal/plugins"
	"github.com/rumpl/daw/internal/protocol"
)

const pluginWatchDebounce = 150 * time.Millisecond

type pluginWatcher struct {
	watcher *fsnotify.Watcher
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

func startPluginWatcher(dir string, events *dashboardEvents, log *slog.Logger) *pluginWatcher {
	if dir == "" {
		log.Info("plugin watcher disabled", "reason", "plugin directory is empty")
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Warn("starting plugin watcher", "directory", dir, "error", err)
		return nil
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Warn("starting plugin watcher", "directory", dir, "error", err)
		return nil
	}
	pw := &pluginWatcher{watcher: watcher, stop: make(chan struct{}), done: make(chan struct{})}
	if err := pw.addDirectories(dir); err != nil {
		watcher.Close()
		log.Warn("starting plugin watcher", "directory", dir, "error", err)
		return nil
	}
	catalog := plugins.Catalog(dir)
	log.Info("plugin catalog loaded", "directory", dir, "plugins", len(catalog.Plugins), "errors", len(catalog.Errors))
	for _, diagnostic := range catalog.Errors {
		log.Warn("invalid plugin", "plugin", diagnostic.PluginID, "error", diagnostic.Message)
	}
	go pw.run(dir, events, log)
	return pw
}

func (p *pluginWatcher) addDirectories(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return p.watcher.Add(path)
		}
		return nil
	})
}

func pluginCatalogRevision(catalog protocol.PluginCatalog) string {
	data, _ := json.Marshal(catalog)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (p *pluginWatcher) run(dir string, events *dashboardEvents, log *slog.Logger) {
	defer close(p.done)
	defer p.watcher.Close()
	revision := pluginCatalogRevision(plugins.Catalog(dir))
	var timer *time.Timer
	var timerC <-chan time.Time
	schedule := func() {
		if timer == nil {
			timer = time.NewTimer(pluginWatchDebounce)
		} else if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
			timer.Reset(pluginWatchDebounce)
		}
		timerC = timer.C
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-p.stop:
			return
		case err, ok := <-p.watcher.Errors:
			if ok {
				log.Warn("plugin watcher", "directory", dir, "error", err)
			}
		case _, ok := <-p.watcher.Events:
			if !ok {
				return
			}
			schedule()
		case <-timerC:
			timerC = nil
			// Refresh watches as directories may have been added or replaced.
			if err := p.addDirectories(dir); err != nil {
				log.Warn("refreshing plugin watcher", "directory", dir, "error", err)
			}
			catalog := plugins.Catalog(dir)
			next := pluginCatalogRevision(catalog)
			if next != revision {
				revision = next
				log.Info("plugin catalog changed", "plugins", len(catalog.Plugins), "errors", len(catalog.Errors), "revision", next)
				for _, diagnostic := range catalog.Errors {
					log.Warn("invalid plugin", "plugin", diagnostic.PluginID, "error", diagnostic.Message)
				}
				events.publish(protocol.DashboardEvent{
					Type: protocol.DashboardEventPluginsChanged, Revision: next,
				})
			}
		}
	}
}

func (p *pluginWatcher) close() {
	if p == nil {
		return
	}
	p.once.Do(func() { close(p.stop) })
	<-p.done
}
