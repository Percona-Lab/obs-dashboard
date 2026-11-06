package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

const testOrigin = "http://dash.test"

type harness struct {
	t     *testing.T
	svc   *Service
	fake  *fakeGoogle
	h     http.Handler
	clock time.Time
}

// newHarness builds an enabled service (admins given) behind a router that
// also exposes POST /api/protected behind RequireUser+CheckOrigin.
func newHarness(t *testing.T, admins ...string) *harness {
	t.Helper()
	hs := &harness{t: t, fake: newFakeGoogle(t), clock: t0}
	hs.svc = NewService(newTestDB(t), Options{
		Provider:   hs.fake.provider(),
		PublicURL:  testOrigin,
		Admins:     admins,
		SessionTTL: time.Hour,
		Now:        func() time.Time { return hs.clock },
	})
	hs.h = testRouter(hs.svc)
	return hs
}

func testRouter(svc *Service) http.Handler {
	r := chi.NewRouter()
	r.Use(svc.LoadUser)
	svc.Mount(r)
	r.With(svc.RequireUser, svc.CheckOrigin).Post("/api/protected", func(w http.ResponseWriter, r *http.Request) {
		email, _ := UserFrom(r.Context())
		w.Header().Set("X-User", email)
		w.WriteHeader(http.StatusNoContent)
	})
	return r
}

func (hs *harness) do(req *http.Request, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	return rec
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// startLogin hits /auth/login and returns the state and nonce cookies.
func (hs *harness) startLogin() (state, nonce *http.Cookie) {
	rec := hs.do(httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	state, nonce = cookieNamed(rec, stateCookie), cookieNamed(rec, nonceCookie)
	if rec.Code != http.StatusFound || state == nil || nonce == nil {
		hs.t.Fatalf("login: code=%d cookies=%v", rec.Code, rec.Result().Cookies())
	}
	return state, nonce
}

// callback completes sign-in with the given claims and returns the response.
func (hs *harness) callback(query string, claims map[string]any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	hs.fake.claims = claims
	return hs.do(httptest.NewRequest(http.MethodGet, "/auth/callback?"+query, nil), cookies...)
}

// signIn runs the full flow for email and returns the session cookie.
func (hs *harness) signIn(email string) *http.Cookie {
	hs.t.Helper()
	st, nn := hs.startLogin()
	rec := hs.callback("code=c&state="+st.Value, validClaims(email, nn.Value), st, nn)
	sess := cookieNamed(rec, sessionCookie)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" || sess == nil {
		hs.t.Fatalf("sign-in %s: code=%d location=%q", email, rec.Code, rec.Header().Get("Location"))
	}
	return sess
}

func (hs *harness) post(path string, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return hs.do(req, cookies...)
}

func me(t *testing.T, rec *httptest.ResponseRecorder) (email *string, enabled bool) {
	t.Helper()
	var body struct {
		Email       *string `json:"email"`
		AuthEnabled bool    `json:"auth_enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("me body %q: %v", rec.Body.String(), err)
	}
	return body.Email, body.AuthEnabled
}

func TestLoginRedirect(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	rec := hs.do(httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("code %d", rec.Code)
	}
	st := cookieNamed(rec, stateCookie)
	if st == nil || !st.HttpOnly || st.Path != "/auth" || st.MaxAge != 600 || st.SameSite != http.SameSiteLaxMode {
		t.Fatalf("state cookie: %+v", st)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if !strings.HasPrefix(loc.String(), hs.fake.srv.URL+"/auth") || loc.Query().Get("state") != st.Value || loc.Query().Get("hd") != "percona.com" {
		t.Fatalf("location: %s", loc)
	}
}

// Review focus 1: mixed-case token email matches a lowercase admin.
func TestCallbackAdminCaseInsensitive(t *testing.T) {
	hs := newHarness(t, "Alice@percona.com")
	sess := hs.signIn("Alice@Percona.com")
	if !sess.HttpOnly || sess.Path != "/" || sess.SameSite != http.SameSiteLaxMode || sess.MaxAge != 3600 {
		t.Fatalf("session cookie: %+v", sess)
	}
	email, enabled := me(t, hs.do(httptest.NewRequest(http.MethodGet, "/api/me", nil), sess))
	if !enabled || email == nil || *email != "alice@percona.com" {
		t.Fatalf("me: %v %v", email, enabled)
	}
}

func TestCallbackAllowlistedUser(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if _, err := hs.svc.store.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	hs.signIn("bob@percona.com")
}

// Review focus 4: user cancels on Google.
func TestCallbackProviderError(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	st, nn := hs.startLogin()
	rec := hs.callback("error=access_denied&state="+st.Value, nil, st, nn)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?auth_error=failed" {
		t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestCallbackFailures(t *testing.T) {
	cases := map[string]func(hs *harness) *httptest.ResponseRecorder{
		"state mismatch": func(hs *harness) *httptest.ResponseRecorder {
			st, nn := hs.startLogin()
			return hs.callback("code=c&state=other", validClaims("alice@percona.com", nn.Value), st, nn)
		},
		"missing state cookie": func(hs *harness) *httptest.ResponseRecorder {
			st, nn := hs.startLogin()
			return hs.callback("code=c&state="+st.Value, validClaims("alice@percona.com", nn.Value), nn)
		},
		"nonce mismatch": func(hs *harness) *httptest.ResponseRecorder {
			st, nn := hs.startLogin()
			return hs.callback("code=c&state="+st.Value, validClaims("alice@percona.com", "replayed"), st, nn)
		},
		"exchange fails": func(hs *harness) *httptest.ResponseRecorder {
			st, nn := hs.startLogin()
			hs.fake.noIDTok = true
			return hs.callback("code=c&state="+st.Value, validClaims("alice@percona.com", nn.Value), st, nn)
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, "alice@percona.com")
			rec := run(hs)
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?auth_error=failed" {
				t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
			}
			if cookieNamed(rec, sessionCookie) != nil {
				t.Fatal("session cookie set on failure")
			}
		})
	}
}

func TestCallbackNotAllowed(t *testing.T) {
	cases := map[string]func(c map[string]any){
		"unverified email": func(c map[string]any) { c["email_verified"] = false },
		"other domain":     func(c map[string]any) { c["hd"] = "gmail.com" },
		"no hd claim":      func(c map[string]any) { delete(c, "hd") },
		"not on allowlist": func(c map[string]any) { c["email"] = "eve@percona.com" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, "alice@percona.com")
			st, nn := hs.startLogin()
			claims := validClaims("alice@percona.com", nn.Value)
			mutate(claims)
			rec := hs.callback("code=c&state="+st.Value, claims, st, nn)
			loc := rec.Header().Get("Location")
			if rec.Code != http.StatusFound || !strings.HasPrefix(loc, "/?auth_error=not_allowed&email=") {
				t.Fatalf("code=%d location=%q", rec.Code, loc)
			}
			if cookieNamed(rec, sessionCookie) != nil {
				t.Fatal("session cookie set for refused user")
			}
		})
	}
}

func TestProtectedRoute(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if rec := hs.post("/api/protected", testOrigin); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	sess := hs.signIn("alice@percona.com")
	rec := hs.post("/api/protected", testOrigin, sess)
	if rec.Code != http.StatusNoContent || rec.Header().Get("X-User") != "alice@percona.com" {
		t.Fatalf("signed in: %d user=%q", rec.Code, rec.Header().Get("X-User"))
	}
	if rec := hs.post("/api/protected", "http://evil.test", sess); rec.Code != http.StatusForbidden {
		t.Fatalf("bad origin: %d", rec.Code)
	}
	if rec := hs.post("/api/protected", "", sess); rec.Code != http.StatusForbidden {
		t.Fatalf("missing origin: %d", rec.Code)
	}
}

// Review focus 2.
func TestExpiredSessionIsAnonymous(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	sess := hs.signIn("alice@percona.com")
	hs.clock = hs.clock.Add(time.Hour)
	if rec := hs.post("/api/protected", testOrigin, sess); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired session: %d", rec.Code)
	}
}

func TestLogout(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	sess := hs.signIn("alice@percona.com")
	rec := hs.post("/auth/logout", testOrigin, sess)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", rec.Code)
	}
	if c := cookieNamed(rec, sessionCookie); c == nil || c.MaxAge >= 0 {
		t.Fatalf("cookie not cleared: %+v", c)
	}
	if rec := hs.post("/api/protected", testOrigin, sess); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", rec.Code)
	}
}

func TestDisabledService(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db, Options{Admins: []string{"alice@percona.com"}})
	h := testRouter(svc)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("login when disabled: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	if email, enabled := me(t, rec); email != nil || enabled {
		t.Fatalf("me when disabled: %v %v", email, enabled)
	}
	sid, err := NewStore(db).CreateSession("alice@percona.com", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/protected", nil)
	req.Header.Set("Origin", testOrigin)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("protected when disabled: %d", rec.Code)
	}
}
