package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/rumpl/daw/internal/pathsec"
	"github.com/rumpl/daw/internal/protocol"
)

const (
	defaultExecutionLocationTTL = 5 * time.Minute
	maxExecutionLocationTTL     = time.Hour
)

func (s *Server) handleRegisterExecutionLocation(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("pluginId")
	if r.Header.Get("X-DAW-Plugin-ID") != pluginID ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get("X-DAW-Plugin-Token")), []byte(s.backends.internalToken)) != 1 {
		s.fail(w, http.StatusForbidden, "execution_location_forbidden", "execution locations are backend-only")
		return
	}
	if !s.validPluginBackend(pluginID) {
		s.fail(w, http.StatusNotFound, "plugin_not_found", "plugin backend not found")
		return
	}
	request, ok := decode[protocol.ExecutionLocationRequest](w, r, s)
	if !ok {
		return
	}
	workspace, ok := s.workspaces.Get(request.WorkspaceID)
	if !ok {
		s.fail(w, http.StatusNotFound, "unknown_workspace", "unknown workspace")
		return
	}
	workingDir, err := s.guard.ResolveDir(request.WorkingDir)
	if err != nil {
		status, code, message := http.StatusBadRequest, "execution_location_invalid", "the execution directory is invalid"
		if errors.Is(err, pathsec.ErrOutsideRoots) {
			status, code, message = http.StatusForbidden, "outside_roots", "the execution directory is outside the allowed roots"
		}
		s.fail(w, status, code, message)
		return
	}
	ttl := defaultExecutionLocationTTL
	if request.TTLSeconds != 0 {
		ttl = time.Duration(request.TTLSeconds) * time.Second
		if ttl < time.Second || ttl > maxExecutionLocationTTL {
			s.fail(w, http.StatusBadRequest, "execution_location_ttl_invalid", "ttlSeconds must be between 1 and 3600")
			return
		}
	}
	location := s.executionLocations.Register(pluginID, workspace.Path, workingDir, ttl)
	s.json(w, http.StatusCreated, protocol.ExecutionLocationRef{
		ExecutionLocationID: location.ID,
		ExpiresAt:           location.ExpiresAt.UTC().Format(time.RFC3339),
	})
}
