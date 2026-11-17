package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/percona/obs-dashboard/internal/cvereport"
	"github.com/percona/obs-dashboard/internal/store"
)

// cveReportHandler serves GET /api/cve/report: the staging work list and the
// released/staging comparison, aggregated by cvereport.
func cveReportHandler(db *sql.DB, cache *ttlCache[cvereport.Report]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep, err := cache.Get(r.Context(), "report", func(ctx context.Context) (cvereport.Report, error) {
			staging, err := store.QueryImageScans(db, "ppg:staging")
			if err != nil {
				return cvereport.Report{}, err
			}
			released, err := store.QueryImageScans(db, "ppg:releases")
			if err != nil {
				return cvereport.Report{}, err
			}
			versions, err := store.QueryContainerVersions(db)
			if err != nil {
				return cvereport.Report{}, err
			}
			return cvereport.Build(staging, released, versions, time.Now()), nil
		})
		if err != nil {
			slog.Error("api: cve report", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rep)
	}
}
