// Package webassets embeds the built frontend so the production binary has no
// loose-file dependency.
package webassets

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Available reports whether a real build (not just the placeholder) is embedded.
func Available() bool {
	entries, err := fs.ReadDir(dist, "dist/assets")
	return err == nil && len(entries) > 0
}

// Handler serves the embedded SPA. Unknown non-asset paths fall back to
// index.html so client-side routing works; /api/* never reaches this handler.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		if strings.HasPrefix(p, "assets/") {
			// Vite emits content-hashed asset names.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
