package api

import (
	"encoding/json"
	"net/http"

	"github.com/percona/obs-dashboard/internal/version"
)

// healthzHandler is the liveness probe the host apply script polls: an
// upgrade succeeds only when it reports the requested version.
func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": version.Version})
}
