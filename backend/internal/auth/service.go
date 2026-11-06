package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	sessionCookie = "obsboard_session"
	stateCookie   = "obsboard_oauth_state"
	nonceCookie   = "obsboard_oauth_nonce"
)

// Options configures a Service. A nil Provider disables sign-in.
type Options struct {
	Provider   *Provider
	PublicURL  string
	Admins     []string
	SessionTTL time.Duration
	Now        func() time.Time
}

// Service owns sign-in, sessions and the allowlist.
type Service struct {
	store     *Store
	provider  *Provider
	publicURL string
	secure    bool
	admins    map[string]bool
	adminList []string
	ttl       time.Duration
	now       func() time.Time
}

func NewService(db *sql.DB, o Options) *Service {
	s := &Service{
		store:     NewStore(db),
		provider:  o.Provider,
		publicURL: strings.TrimRight(o.PublicURL, "/"),
		secure:    strings.HasPrefix(o.PublicURL, "https://"),
		admins:    map[string]bool{},
		adminList: []string{},
		ttl:       o.SessionTTL,
		now:       o.Now,
	}
	if s.ttl <= 0 {
		s.ttl = 168 * time.Hour
	}
	if s.now == nil {
		s.now = time.Now
	}
	for _, a := range o.Admins {
		a = normalizeEmail(a)
		if a != "" && !s.admins[a] {
			s.admins[a] = true
			s.adminList = append(s.adminList, a)
		}
	}
	return s
}

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Enabled reports whether Google sign-in is configured.
func (s *Service) Enabled() bool { return s.provider != nil }

// IsAllowed reports whether email is a config admin or on the allowlist.
func (s *Service) IsAllowed(email string) (bool, error) {
	if s.admins[email] {
		return true, nil
	}
	return s.store.HasUser(email)
}

// PruneExpired deletes expired sessions; run once at startup.
func (s *Service) PruneExpired() error {
	_, err := s.store.PruneExpired(s.now())
	return err
}

type ctxKey struct{}

// UserFrom returns the signed-in email set by LoadUser.
func UserFrom(ctx context.Context) (string, bool) {
	e, ok := ctx.Value(ctxKey{}).(string)
	return e, ok
}

// LoadUser resolves the session cookie, if any, to an allowed email. Requests
// without a valid session pass through anonymously.
func (s *Service) LoadUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if !s.Enabled() || err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		email, ok, err := s.store.SessionEmail(c.Value, s.now())
		if err != nil {
			slog.Warn("auth: session lookup failed", "err", err)
		}
		if ok {
			allowed, err := s.IsAllowed(email)
			if err != nil {
				slog.Warn("auth: allowlist lookup failed", "err", err)
			} else if allowed {
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, email))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireUser rejects requests without a signed-in user.
func (s *Service) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CheckOrigin rejects state-changing requests whose Origin is not public_url.
func (s *Service) CheckOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("Origin") != s.publicURL {
				http.Error(w, "forbidden: bad origin", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Mount registers the auth routes. /auth/* exists only when sign-in is
// enabled; /api/me always exists.
func (s *Service) Mount(r chi.Router) {
	r.Get("/api/me", s.handleMe)
	r.Group(func(r chi.Router) {
		r.Use(s.RequireUser, s.CheckOrigin)
		r.Get("/api/settings/users", s.handleListUsers)
		r.Post("/api/settings/users", s.handleAddUser)
		r.Delete("/api/settings/users/{email}", s.handleRemoveUser)
	})
	if !s.Enabled() {
		return
	}
	r.Get("/auth/login", s.handleLogin)
	r.Get("/auth/callback", s.handleCallback)
	r.With(s.RequireUser, s.CheckOrigin).Post("/auth/logout", s.handleLogout)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	resp := struct {
		Email       *string `json:"email"`
		AuthEnabled bool    `json:"auth_enabled"`
	}{AuthEnabled: s.Enabled()}
	if e, ok := UserFrom(r.Context()); ok {
		resp.Email = &e
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) setCookie(w http.ResponseWriter, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: maxAge,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, stateCookie, state, "/auth", 600)
	s.setCookie(w, nonceCookie, nonce, "/auth", 600)
	http.Redirect(w, r, s.provider.AuthCodeURL(state, nonce), http.StatusFound)
}

func equalToken(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Service) handleCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(reason string, err error) {
		slog.Warn("auth: sign-in failed", "reason", reason, "err", err)
		http.Redirect(w, r, "/?auth_error=failed", http.StatusFound)
	}
	q := r.URL.Query()
	stateC, stateErr := r.Cookie(stateCookie)
	nonceC, nonceErr := r.Cookie(nonceCookie)
	// The state and nonce are single-use whatever the outcome.
	s.setCookie(w, stateCookie, "", "/auth", -1)
	s.setCookie(w, nonceCookie, "", "/auth", -1)

	if e := q.Get("error"); e != "" {
		fail("provider returned "+e, nil)
		return
	}
	if stateErr != nil || nonceErr != nil || !equalToken(stateC.Value, q.Get("state")) {
		fail("state mismatch", nil)
		return
	}
	id, err := s.provider.Exchange(r.Context(), q.Get("code"))
	if err != nil {
		fail("exchange", err)
		return
	}
	if !equalToken(nonceC.Value, id.Nonce) {
		fail("nonce mismatch", nil)
		return
	}
	allowed := id.EmailVerified && id.HostedDomain == AllowedDomain && id.Email != ""
	if allowed {
		if allowed, err = s.IsAllowed(id.Email); err != nil {
			fail("allowlist lookup", err)
			return
		}
	}
	if !allowed {
		slog.Info("auth: sign-in refused", "email", id.Email, "verified", id.EmailVerified, "hd", id.HostedDomain)
		http.Redirect(w, r, "/?auth_error=not_allowed&email="+url.QueryEscape(id.Email), http.StatusFound)
		return
	}
	sid, err := s.store.CreateSession(id.Email, s.now(), s.ttl)
	if err != nil {
		fail("create session", err)
		return
	}
	s.setCookie(w, sessionCookie, sid, "/", int(s.ttl.Seconds()))
	slog.Info("auth: signed in", "email", id.Email)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.store.DeleteSession(c.Value); err != nil {
			slog.Warn("auth: delete session failed", "err", err)
		}
	}
	s.setCookie(w, sessionCookie, "", "/", -1)
	w.WriteHeader(http.StatusNoContent)
}
