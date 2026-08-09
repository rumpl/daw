package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rumpl/daw/internal/plugins"
	"github.com/rumpl/daw/internal/protocol"
)

func (s *Server) validPlugin(pluginID string) bool {
	for _, plugin := range s.pluginCatalog().Plugins {
		if plugin.ID == pluginID {
			return true
		}
	}
	return false
}

func (s *Server) validPluginBackend(pluginID string) bool {
	for _, plugin := range s.pluginCatalog().Plugins {
		if plugin.ID == pluginID && plugin.BackendURL != "" {
			return true
		}
	}
	return false
}

func (s *Server) handlePluginEvents(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("pluginId")
	if !s.validPluginBackend(pluginID) {
		s.fail(w, http.StatusNotFound, "plugin_not_found", "plugin backend not found")
		return
	}
	lastID, _ := strconv.ParseUint(r.URL.Query().Get("lastEventId"), 10, 64)
	subscriber, replay, available := s.pluginEvents.subscribe(pluginID, lastID)
	defer s.pluginEvents.unsubscribe(pluginID, subscriber)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.fail(w, http.StatusInternalServerError, "stream_unsupported", "streaming is unavailable")
		return
	}
	write := func(event protocol.PluginEvent) bool {
		data, err := json.Marshal(event)
		if err != nil {
			return false
		}
		_, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.Seq, data)
		flusher.Flush()
		return err == nil
	}
	if lastID > 0 && !available {
		if !write(protocol.PluginEvent{Type: "gap", Seq: lastID}) {
			return
		}
	}
	for _, event := range replay {
		if !write(event) {
			return
		}
	}
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-subscriber.ch:
			if !open || !write(event) {
				return
			}
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handlePluginPublish(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("pluginId")
	if r.Header.Get("X-DAW-Plugin-ID") != pluginID ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get("X-DAW-Plugin-Token")), []byte(s.backends.internalToken)) != 1 {
		s.fail(w, http.StatusForbidden, "plugin_publish_forbidden", "plugin event publishing is backend-only")
		return
	}
	if !s.validPluginBackend(pluginID) {
		s.fail(w, http.StatusNotFound, "plugin_not_found", "plugin backend not found")
		return
	}
	var request struct {
		Type string `json:"type"`
		Data any    `json:"data,omitempty"`
	}
	value, ok := decode[struct {
		Type string `json:"type"`
		Data any    `json:"data,omitempty"`
	}](w, r, s)
	if !ok {
		return
	}
	request = value
	request.Type = strings.TrimSpace(request.Type)
	if request.Type == "" || len(request.Type) > 80 {
		s.fail(w, http.StatusBadRequest, "invalid_plugin_event", "plugin event type is invalid")
		return
	}
	event := s.pluginEvents.publish(pluginID, request.Type, request.Data)
	s.json(w, http.StatusAccepted, event)
}

func (s *Server) handleGetPluginConfig(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("pluginId")
	if !s.validPlugin(pluginID) {
		s.fail(w, http.StatusNotFound, "plugin_not_found", "plugin not found")
		return
	}
	values, err := s.pluginConfig.get(pluginID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "plugin_config_failed", "plugin configuration could not be read")
		return
	}
	s.json(w, http.StatusOK, protocol.PluginConfiguration{Values: values})
}

func (s *Server) handlePutPluginConfig(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("pluginId")
	if !s.validPlugin(pluginID) {
		s.fail(w, http.StatusNotFound, "plugin_not_found", "plugin not found")
		return
	}
	request, ok := decode[protocol.PluginConfiguration](w, r, s)
	if !ok {
		return
	}
	if request.Values == nil {
		request.Values = map[string]any{}
	}
	if err := s.pluginConfig.set(pluginID, request.Values); err != nil {
		s.fail(w, http.StatusBadRequest, "plugin_config_failed", "plugin configuration could not be saved")
		return
	}
	s.pluginEvents.publish(pluginID, "configuration_changed", request.Values)
	s.json(w, http.StatusOK, request)
}

func (s *Server) pluginWebhookToken(pluginID, webhookID string) (string, error) {
	tokenPath := filepath.Join(s.backends.dataDir, "webhook-tokens", pluginID, webhookID)
	token, err := os.ReadFile(tokenPath)
	if err == nil {
		return strings.TrimSpace(string(token)), nil
	}
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		return "", err
	}
	value := newToken()
	if err := os.WriteFile(tokenPath, []byte(value), 0o600); err != nil {
		return "", err
	}
	return value, nil
}

func (s *Server) isAuthenticatedWebhook(r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 5 || parts[0] != "api" || parts[1] != "plugins" || parts[3] != "webhooks" {
		return false
	}
	pluginID, webhookID := parts[2], parts[4]
	if !plugins.HasWebhook(s.pluginDir, pluginID, webhookID) {
		return false
	}
	token, err := s.pluginWebhookToken(pluginID, webhookID)
	return err == nil && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
}

func (s *Server) handlePluginWebhookToken(w http.ResponseWriter, r *http.Request) {
	pluginID, webhookID := r.PathValue("pluginId"), r.PathValue("webhookId")
	if r.Header.Get("X-DAW-Plugin-ID") != pluginID ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get("X-DAW-Plugin-Token")), []byte(s.backends.internalToken)) != 1 {
		s.fail(w, http.StatusForbidden, "plugin_webhook_forbidden", "webhook tokens are backend-only")
		return
	}
	if !plugins.HasWebhook(s.pluginDir, pluginID, webhookID) {
		s.fail(w, http.StatusNotFound, "plugin_webhook_not_found", "plugin webhook not found")
		return
	}
	token, err := s.pluginWebhookToken(pluginID, webhookID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "plugin_webhook_failed", "plugin webhook is unavailable")
		return
	}
	s.json(w, http.StatusOK, map[string]string{
		"url":   "/api/plugins/" + pluginID + "/webhooks/" + webhookID,
		"token": token,
	})
}

func (s *Server) handlePluginWebhook(w http.ResponseWriter, r *http.Request) {
	pluginID, webhookID := r.PathValue("pluginId"), r.PathValue("webhookId")
	if !plugins.HasWebhook(s.pluginDir, pluginID, webhookID) {
		s.fail(w, http.StatusNotFound, "plugin_webhook_not_found", "plugin webhook not found")
		return
	}
	token, err := s.pluginWebhookToken(pluginID, webhookID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "plugin_webhook_failed", "plugin webhook is unavailable")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
		s.fail(w, http.StatusUnauthorized, "plugin_webhook_unauthorized", "plugin webhook authentication failed")
		return
	}
	if err := s.backends.proxyWebhook(w, r, pluginID, webhookID); err != nil {
		s.fail(w, http.StatusBadGateway, "plugin_backend_unavailable", "plugin backend unavailable")
	}
}
