package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// MCPBridge returns a narrowly scoped handler for local plugin MCP processes
// running in a sandbox. The sandbox receives only bridgeToken, never the
// dashboard CSRF token or the plugin-backend internal token. Requests are
// limited to the calling plugin's backend proxy route before being translated
// to an ordinary trusted internal request.
func (s *Server) MCPBridge(bridgeToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csrf := r.Header.Get(CSRFHeader)
		pluginToken := r.Header.Get("X-DAW-Plugin-Token")
		if bridgeToken == "" ||
			subtle.ConstantTimeCompare([]byte(csrf), []byte(bridgeToken)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pluginToken), []byte(bridgeToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		pluginID := strings.TrimSpace(r.Header.Get("X-DAW-Plugin-ID"))
		prefix := "/api/plugins/" + pluginID + "/backend"
		if pluginID == "" || (r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		request := r.Clone(r.Context())
		request.Host = "127.0.0.1"
		request.Header = r.Header.Clone()
		request.Header.Set(CSRFHeader, s.csrf)
		request.Header.Set("X-DAW-Plugin-Token", s.backends.internalToken)
		s.ServeHTTP(w, request)
	})
}
