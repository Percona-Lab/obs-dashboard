package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
)

// validDomainEmail reports whether e is local@percona.com with a plain local part.
func validDomainEmail(e string) bool {
	local, domain, ok := strings.Cut(e, "@")
	return ok && local != "" && domain == AllowedDomain && !strings.ContainsAny(local, "@ \t")
}

func (s *Service) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers()
	if err != nil {
		slog.Error("auth: list users", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"admins": s.adminList, "users": users})
}

func (s *Service) handleAddUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	email := normalizeEmail(body.Email)
	if !validDomainEmail(email) {
		http.Error(w, "email must be a @"+AllowedDomain+" address", http.StatusBadRequest)
		return
	}
	if s.admins[email] {
		http.Error(w, "already an admin", http.StatusConflict)
		return
	}
	caller, _ := UserFrom(r.Context())
	u, err := s.store.AddUser(email, caller, s.now())
	if errors.Is(err, ErrExists) {
		http.Error(w, "already allowed", http.StatusConflict)
		return
	}
	if err != nil {
		slog.Error("auth: add user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("auth: user added", "email", email, "by", caller)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Service) handleRemoveUser(w http.ResponseWriter, r *http.Request) {
	raw, err := url.PathUnescape(chi.URLParam(r, "email"))
	if err != nil {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}
	email := normalizeEmail(raw)
	caller, _ := UserFrom(r.Context())
	if s.admins[email] {
		http.Error(w, "config admins cannot be removed", http.StatusBadRequest)
		return
	}
	if email == caller {
		http.Error(w, "you cannot remove yourself", http.StatusBadRequest)
		return
	}
	err = s.store.RemoveUser(email)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("auth: remove user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("auth: user removed", "email", email, "by", caller)
	w.WriteHeader(http.StatusNoContent)
}
