package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"golang.org/x/oauth2"
)

const dashOrigin = "http://dash.test"

func authRouter(t *testing.T, enabled bool) (http.Handler, *atomic.Bool, string) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	opts := auth.Options{PublicURL: dashOrigin, Admins: []string{"alice@percona.com"}}
	sid := ""
	if enabled {
		// A provider that is never called: sessions are created directly.
		opts.Provider = auth.NewProvider(oauth2.Config{}, nil)
		if sid, err = auth.NewStore(db).CreateSession("alice@percona.com", time.Now(), time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	obsClient := obs.SingleFleet(obs.NewClient(stubOBSServer(t).URL, "user", "pass"), "isv:percona")
	tel := new(atomic.Bool)
	r := NewRouter(db, hub.New(), obsClient, fakeStatter{}, tel, time.Duration(0), fakePollState{s: "active"}, auth.NewService(db, opts), nil, nil, nil, nil)
	return r, tel, sid
}

func call(h http.Handler, method, path, body, origin, sid string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: "obsboard_session", Value: sid})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestActionsRequireAuthWhenDisabled(t *testing.T) {
	h, _, _ := authRouter(t, false)
	rebuild := `{"project":"ppg:staging:18","repo":"r","arch":"x86_64","package":"p"}`
	if code := call(h, http.MethodPost, "/api/rebuild", rebuild, dashOrigin, ""); code != http.StatusUnauthorized {
		t.Errorf("rebuild: %d", code)
	}
	if code := call(h, http.MethodPost, "/api/telemetry?enabled=true", "", dashOrigin, ""); code != http.StatusUnauthorized {
		t.Errorf("telemetry set: %d", code)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/telemetry"},
		{http.MethodGet, "/api/me"},
		{http.MethodPost, "/api/presence"},
	} {
		if code := call(h, c.method, c.path, "", "", ""); code == http.StatusUnauthorized {
			t.Errorf("%s %s should stay public, got 401", c.method, c.path)
		}
	}
}

func TestTelemetryWithSession(t *testing.T) {
	h, tel, sid := authRouter(t, true)
	if code := call(h, http.MethodPost, "/api/telemetry?enabled=true", "", "", sid); code != http.StatusForbidden {
		t.Fatalf("missing origin: %d", code)
	}
	if code := call(h, http.MethodPost, "/api/telemetry?enabled=true", "", dashOrigin, sid); code != http.StatusOK {
		t.Fatalf("with session: %d", code)
	}
	if !tel.Load() {
		t.Fatal("telemetry flag not set")
	}
}
