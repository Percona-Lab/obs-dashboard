package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/upgrade"
	"golang.org/x/oauth2"
)

type staticSource []upgrade.Release

func (s staticSource) Releases(context.Context) ([]upgrade.Release, error) { return s, nil }

func upgradeRouter(t *testing.T, applyDir, hostFiles string) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "obsboard.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	opts := auth.Options{PublicURL: dashOrigin, Admins: []string{"alice@percona.com"}, Provider: auth.NewProvider(oauth2.Config{}, nil)}
	sid, err := auth.NewStore(db).CreateSession("alice@percona.com", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pub := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	m := upgrade.NewManager(upgrade.Options{
		DB: db, DBPath: dbPath, Repo: "Percona-Lab/obs-dashboard", Current: "v0.1.0",
		HostFilesVersion: hostFiles, ApplyDir: applyDir,
		Source: staticSource{
			{Version: "v0.2.0", Notes: "n2", URL: "u2", PublishedAt: pub},
			{Version: "v0.3.0", Notes: "n3", URL: "u3", PublishedAt: pub, HostChanges: "add FOO"},
		},
	})
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	obsClient := obs.SingleFleet(obs.NewClient(stubOBSServer(t).URL, "user", "pass"), "isv:percona")
	r := NewRouter(db, hub.New(), obsClient, fakeStatter{}, new(atomic.Bool), time.Duration(0), fakePollState{s: "active"}, auth.NewService(db, opts), m, nil, nil)
	return r, sid
}

func callRec(h http.Handler, method, path, origin, sid string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: "obsboard_session", Value: sid})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUpgradesRequireSignIn(t *testing.T) {
	h, sid := upgradeRouter(t, t.TempDir(), "v0.3.0")
	if rec := callRec(h, http.MethodGet, "/api/upgrades", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon GET: %d", rec.Code)
	}
	if rec := callRec(h, http.MethodPost, "/api/upgrades/v0.2.0/apply", "http://evil.test", sid); rec.Code != http.StatusForbidden {
		t.Fatalf("bad origin: %d", rec.Code)
	}
	if rec := callRec(h, http.MethodPost, "/api/upgrades/check", "", sid); rec.Code != http.StatusForbidden {
		t.Fatalf("missing origin: %d", rec.Code)
	}
}

func TestUpgradesListAndApply(t *testing.T) {
	applyDir := t.TempDir()
	h, sid := upgradeRouter(t, applyDir, "v0.3.0")
	rec := callRec(h, http.MethodGet, "/api/upgrades", "", sid)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d", rec.Code)
	}
	var v upgrade.View
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Current != "v0.1.0" || len(v.Releases) != 2 || !v.ApplyEnabled || v.Repo != "Percona-Lab/obs-dashboard" {
		t.Fatalf("view = %+v", v)
	}
	if rec := callRec(h, http.MethodPost, "/api/upgrades/v9.9.9/apply", dashOrigin, sid); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
	if rec := callRec(h, http.MethodPost, "/api/upgrades/v0.2.0/apply", dashOrigin, sid); rec.Code != http.StatusAccepted {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(applyDir, "apply-request")); err != nil {
		t.Fatal("no request written")
	}
	if rec := callRec(h, http.MethodPost, "/api/upgrades/v0.3.0/apply", dashOrigin, sid); rec.Code != http.StatusConflict {
		t.Fatalf("in flight: %d", rec.Code)
	}
}

func TestUpgradesHostUpdateRequired(t *testing.T) {
	h, sid := upgradeRouter(t, t.TempDir(), "")
	rec := callRec(h, http.MethodPost, "/api/upgrades/v0.3.0/apply", dashOrigin, sid)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "host_update_required" || body["requires_host"] != "v0.3.0" {
		t.Fatalf("body %v", body)
	}
}

func TestUpgradesDisabled(t *testing.T) {
	h, sid := upgradeRouter(t, "", "v0.3.0")
	if rec := callRec(h, http.MethodPost, "/api/upgrades/v0.2.0/apply", dashOrigin, sid); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestUpgradesCheck(t *testing.T) {
	h, sid := upgradeRouter(t, t.TempDir(), "v0.3.0")
	if rec := callRec(h, http.MethodPost, "/api/upgrades/check", dashOrigin, sid); rec.Code != http.StatusOK {
		t.Fatalf("check: %d", rec.Code)
	}
}
