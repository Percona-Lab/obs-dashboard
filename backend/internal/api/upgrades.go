package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/upgrade"
)

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func upgradesListHandler(m *upgrade.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := m.View()
		if err != nil {
			slog.Error("upgrades: view", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, v)
	}
}

func upgradesCheckHandler(m *upgrade.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := m.Check(r.Context()); err != nil {
			slog.Warn("upgrades: manual check failed", "err", err)
			http.Error(w, "release check failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		upgradesListHandler(m)(w, r)
	}
}

func upgradesApplyHandler(m *upgrade.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _ := auth.UserFrom(r.Context())
		row, err := m.Apply(r.Context(), chi.URLParam(r, "version"), actor)
		var hu *upgrade.HostUpdateError
		switch {
		case err == nil:
			respondJSON(w, http.StatusAccepted, row)
		case errors.As(err, &hu):
			respondJSON(w, http.StatusConflict, map[string]string{"error": "host_update_required", "requires_host": hu.RequiresHost})
		case errors.Is(err, upgrade.ErrNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
		case errors.Is(err, upgrade.ErrConflict):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, upgrade.ErrDisabled):
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		default:
			slog.Error("upgrades: apply", "err", err)
			http.Error(w, "upgrade request failed: "+err.Error(), http.StatusInternalServerError)
		}
	}
}
