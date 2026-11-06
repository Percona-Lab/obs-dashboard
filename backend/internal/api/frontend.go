package api

import (
	"net/http"
	"strings"
)

// WithFrontend serves the built SPA from dir and sends API, auth and health
// paths to router. An empty dir returns router unchanged.
func WithFrontend(router http.Handler, dir string) http.Handler {
	if dir == "" {
		return router
	}
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api") || strings.HasPrefix(p, "/auth/") || p == "/healthz" {
			router.ServeHTTP(w, r)
			return
		}
		fs.ServeHTTP(w, r)
	})
}
