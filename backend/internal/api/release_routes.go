package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/release"
)

// maxReleaseBody caps request bodies of the release routes.
const maxReleaseBody = 256 << 10

// ReleaseService is the part of *release.Service the handlers call.
type ReleaseService interface {
	Snapshot(ctx context.Context) (release.Snapshot, error)
	Units(ctx context.Context) ([]release.UnitRow, error)
	History(ctx context.Context, page int) ([]release.HistoryRow, bool, error)
	Cut(ctx context.Context, email string, units []string) (string, error)
	SaveChangelog(ctx context.Context, email, id, head, unit, section string) (string, error)
	ResetChangelog(ctx context.Context, email, id, head, unit string) (string, error)
	ApproveChangelog(ctx context.Context, email, id, head string) error
	RetrySync(ctx context.Context, email, id, head string) error
	ApproveQA(ctx context.Context, email, id, head string) error
	RetryQA(ctx context.Context, email, id, head string) error
	SkipQA(ctx context.Context, email, id, head string) error
	RerunQA(ctx context.Context, email, id, head, lane string) error
	Recut(ctx context.Context, email, id, head string) error
	Release(ctx context.Context, email, id, head string, confirmFailed bool) error
	RetryRelease(ctx context.Context, email, id string) error
	Abandon(ctx context.Context, email, id, head string) error
}

const githubFailed = "github request failed"

// relFail maps a service error to a status. Details of unexpected errors are
// logged, never returned.
func relFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, release.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, release.ErrConflict):
		http.Error(w, "conflict", http.StatusConflict)
	case errors.Is(err, release.ErrBadRequest):
		msg := strings.TrimPrefix(err.Error(), release.ErrBadRequest.Error()+": ")
		http.Error(w, msg, http.StatusBadRequest)
	default:
		slog.Error("release request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		http.Error(w, githubFailed, http.StatusBadGateway)
	}
}

func relJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// relBody decodes the capped JSON body; an empty body is the zero value.
func relBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxReleaseBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
		}
		return false
	}
	return true
}

func relEmail(r *http.Request) string {
	email, _ := auth.UserFrom(r.Context())
	return email
}

func releaseStatusHandler(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
	}
}

// mountRelease registers the release routes; the caller has applied RequireUser.
func mountRelease(r chi.Router, svc ReleaseService, origin func(http.Handler) http.Handler) {
	r.Get("/api/release/units", func(w http.ResponseWriter, req *http.Request) {
		units, err := svc.Units(req.Context())
		if err != nil {
			relFail(w, req, err)
			return
		}
		if units == nil {
			units = []release.UnitRow{}
		}
		relJSON(w, http.StatusOK, map[string]any{"units": units})
	})
	r.Get("/api/release/releases", func(w http.ResponseWriter, req *http.Request) {
		snap, err := svc.Snapshot(req.Context())
		if err != nil {
			relFail(w, req, err)
			return
		}
		relJSON(w, http.StatusOK, snap)
	})
	r.Get("/api/release/history", func(w http.ResponseWriter, req *http.Request) {
		page := 1
		if q := req.URL.Query().Get("page"); q != "" {
			n, err := strconv.Atoi(q)
			if err != nil || n < 1 {
				http.Error(w, "page must be a positive number", http.StatusBadRequest)
				return
			}
			page = n
		}
		rows, more, err := svc.History(req.Context(), page)
		if err != nil {
			relFail(w, req, err)
			return
		}
		if rows == nil {
			rows = []release.HistoryRow{}
		}
		relJSON(w, http.StatusOK, map[string]any{"rows": rows, "more": more})
	})

	r.With(origin).Post("/api/release/releases", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Units []string `json:"units"`
		}
		if !relBody(w, req, &body) {
			return
		}
		id, err := svc.Cut(req.Context(), relEmail(req), body.Units)
		if err != nil {
			relFail(w, req, err)
			return
		}
		relJSON(w, http.StatusCreated, map[string]string{"id": id})
	})
	r.With(origin).Put("/api/release/releases/{id}/changelog", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Head    string `json:"head"`
			Unit    string `json:"unit"`
			Section string `json:"section"`
		}
		if !relBody(w, req, &body) {
			return
		}
		head, err := svc.SaveChangelog(req.Context(), relEmail(req), chi.URLParam(req, "id"), body.Head, body.Unit, body.Section)
		if err != nil {
			relFail(w, req, err)
			return
		}
		relJSON(w, http.StatusOK, map[string]string{"head": head})
	})
	r.With(origin).Post("/api/release/releases/{id}/changelog/reset", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Head string `json:"head"`
			Unit string `json:"unit"`
		}
		if !relBody(w, req, &body) {
			return
		}
		head, err := svc.ResetChangelog(req.Context(), relEmail(req), chi.URLParam(req, "id"), body.Head, body.Unit)
		if err != nil {
			relFail(w, req, err)
			return
		}
		relJSON(w, http.StatusOK, map[string]string{"head": head})
	})
	r.With(origin).Post("/api/release/releases/{id}/{action}", func(w http.ResponseWriter, req *http.Request) {
		id, action := chi.URLParam(req, "id"), chi.URLParam(req, "action")
		switch action {
		case "approve-changelog", "retry-sync", "approve-qa", "retry-qa", "skip-qa", "rerun-qa", "recut", "release", "retry-release", "abandon":
		default:
			http.NotFound(w, req)
			return
		}
		var body struct {
			Head          string `json:"head"`
			ConfirmFailed bool   `json:"confirm_failed"`
			Lane          string `json:"lane"` // rerun-qa: the lane, "" for every failed one
		}
		if !relBody(w, req, &body) {
			return
		}
		ctx, email := req.Context(), relEmail(req)
		var err error
		switch action {
		case "approve-changelog":
			err = svc.ApproveChangelog(ctx, email, id, body.Head)
		case "retry-sync":
			err = svc.RetrySync(ctx, email, id, body.Head)
		case "approve-qa":
			err = svc.ApproveQA(ctx, email, id, body.Head)
		case "retry-qa":
			err = svc.RetryQA(ctx, email, id, body.Head)
		case "skip-qa":
			err = svc.SkipQA(ctx, email, id, body.Head)
		case "rerun-qa":
			err = svc.RerunQA(ctx, email, id, body.Head, body.Lane)
		case "recut":
			err = svc.Recut(ctx, email, id, body.Head)
		case "release":
			err = svc.Release(ctx, email, id, body.Head, body.ConfirmFailed)
		case "retry-release":
			err = svc.RetryRelease(ctx, email, id)
		case "abandon":
			err = svc.Abandon(ctx, email, id, body.Head)
		}
		if err != nil {
			relFail(w, req, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
