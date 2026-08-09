package httpapi

import (
	"net/http"
	"os"
	"time"

	"github.com/rumpl/daw/internal/plugins"
	"github.com/rumpl/daw/internal/protocol"
)

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.json(w, http.StatusOK, protocol.Health{
		Status: "ok",
		Uptime: int64(time.Since(s.started).Seconds()),
	})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	info, err := s.adapter.Info(r.Context())
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "sdk_init_failed",
			"docker-agent could not be initialized; check the server log")
		return
	}
	wsHints := s.workspaces.Hints()

	notices := append([]protocol.Notice(nil), info.Notices...)
	notices = append(notices,
		protocol.Notice{
			ID: "tools-auto-approved", Level: protocol.NoticeWarning, Code: "tools_auto_approved",
			Message: "Every tool call, including shell commands, is auto-approved and runs " +
				"on this host as your user.",
		},
		protocol.Notice{
			ID: "sandbox", Level: protocol.NoticeInfo, Code: "no_sandbox",
			Message: "This dashboard embeds docker-agent in-process: tools run directly on this host " +
				"with your user's permissions. There is no sandbox. Use `docker agent run --sandbox` " +
				"in a terminal if you need isolation.",
		},
	)

	s.json(w, http.StatusOK, protocol.Bootstrap{
		AppVersion: s.appVersion, AgentVersion: info.AgentVersion, AgentCommit: info.AgentCommit,
		ConfigDir: info.ConfigDir, DataDir: info.DataDir, CacheDir: info.CacheDir,
		SessionDB: info.SessionDB, PluginDir: s.pluginDir,
		CSRFToken: s.csrf, Sandboxed: false,
		ModelsAvailable: info.ModelsAvailable, ModelsHint: info.ModelsHint,
		WorkspaceHints: wsHints, Notices: notices,
	})
}

func (s *Server) handlePlugins(w http.ResponseWriter, _ *http.Request) {
	catalog := plugins.Catalog(s.pluginDir)
	s.log.Info("listing plugins", "plugins", len(catalog.Plugins), "errors", len(catalog.Errors))
	s.json(w, http.StatusOK, catalog)
}

func (s *Server) handlePluginBackend(w http.ResponseWriter, r *http.Request) {
	if err := s.backends.proxy(w, r, r.PathValue("pluginId")); err != nil {
		if os.IsNotExist(err) {
			s.fail(w, http.StatusNotFound, "plugin_backend_not_found", "plugin backend not found")
			return
		}
		s.log.Warn("plugin backend request", "plugin", r.PathValue("pluginId"), "error", err)
		s.fail(w, http.StatusBadGateway, "plugin_backend_unavailable", "plugin backend unavailable")
	}
}

func (s *Server) handlePluginAsset(w http.ResponseWriter, r *http.Request) {
	path, info, err := plugins.Asset(
		s.pluginDir,
		r.PathValue("pluginId"),
		r.PathValue("fingerprint"),
		r.PathValue("path"),
	)
	if err != nil {
		s.fail(w, http.StatusNotFound, "plugin_asset_not_found", "plugin asset not found")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		s.fail(w, http.StatusNotFound, "plugin_asset_not_found", "plugin asset not found")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", plugins.ContentType(path))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}
