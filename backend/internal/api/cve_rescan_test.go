package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"golang.org/x/oauth2"
)

type fakeRescanner struct {
	n     int
	err   error
	calls int
}

func (f *fakeRescanner) RescanVulnerable() (int, error) { f.calls++; return f.n, f.err }

func rescanRouter(t *testing.T, f *fakeRescanner) (http.Handler, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "obsboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	opts := auth.Options{PublicURL: dashOrigin, Admins: []string{"alice@percona.com"}, Provider: auth.NewProvider(oauth2.Config{}, nil)}
	sid, err := auth.NewStore(db).CreateSession("alice@percona.com", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	obsClient := obs.SingleFleet(obs.NewClient(stubOBSServer(t).URL, "user", "pass"), "isv:percona")
	return NewRouter(db, hub.New(), obsClient, fakeStatter{}, new(atomic.Bool), time.Duration(0), fakePollState{s: "active"}, auth.NewService(db, opts), nil, f, nil), sid
}

func TestCveRescanRequiresSignIn(t *testing.T) {
	f := &fakeRescanner{n: 3}
	h, sid := rescanRouter(t, f)
	if rec := callRec(h, http.MethodPost, "/api/cve/rescan", dashOrigin, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon: %d", rec.Code)
	}
	if rec := callRec(h, http.MethodPost, "/api/cve/rescan", "http://evil.test", sid); rec.Code != http.StatusForbidden {
		t.Fatalf("bad origin: %d", rec.Code)
	}
	if f.calls != 0 {
		t.Fatalf("rescanner called %d times without authorization", f.calls)
	}
}

func TestCveRescanQueues(t *testing.T) {
	f := &fakeRescanner{n: 3}
	h, sid := rescanRouter(t, f)
	rec := callRec(h, http.MethodPost, "/api/cve/rescan", dashOrigin, sid)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d %s", rec.Code, rec.Body)
	}
	var body map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["queued"] != 3 || f.calls != 1 {
		t.Fatalf("body %v calls %d", body, f.calls)
	}

	f.err = errors.New("boom")
	if rec := callRec(h, http.MethodPost, "/api/cve/rescan", dashOrigin, sid); rec.Code != http.StatusInternalServerError {
		t.Fatalf("error case: %d", rec.Code)
	}
}
