package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/release"
	"github.com/percona/obs-dashboard/internal/store"
	"golang.org/x/oauth2"
)

type stubRelease struct {
	err   error // returned by every action and read
	email string
	calls []string
}

func (s *stubRelease) rec(name string, ctx context.Context) error {
	s.email, _ = auth.UserFrom(ctx)
	s.calls = append(s.calls, name)
	return s.err
}

func (s *stubRelease) Snapshot(ctx context.Context) (release.Snapshot, error) {
	return release.Snapshot{Releases: []release.Release{}}, s.rec("snapshot", ctx)
}
func (s *stubRelease) Units(ctx context.Context) ([]release.UnitRow, error) {
	return []release.UnitRow{}, s.rec("units", ctx)
}
func (s *stubRelease) History(ctx context.Context, page int) ([]release.HistoryRow, error) {
	return []release.HistoryRow{}, s.rec(fmt.Sprintf("history%d", page), ctx)
}
func (s *stubRelease) Cut(ctx context.Context, email string, units []string) (string, error) {
	return "rel1", s.rec("cut", ctx)
}
func (s *stubRelease) SaveChangelog(ctx context.Context, email, id, head, unit, section string) (string, error) {
	return "h2", s.rec("save", ctx)
}
func (s *stubRelease) ResetChangelog(ctx context.Context, email, id, head, unit string) (string, error) {
	return "h3", s.rec("reset", ctx)
}
func (s *stubRelease) ApproveChangelog(ctx context.Context, email, id, head string) error {
	return s.rec("approve-changelog", ctx)
}
func (s *stubRelease) RetrySync(ctx context.Context, email, id, head string) error {
	return s.rec("retry-sync", ctx)
}
func (s *stubRelease) ApproveQA(ctx context.Context, email, id, head string) error {
	return s.rec("approve-qa", ctx)
}
func (s *stubRelease) Recut(ctx context.Context, email, id, head string) error {
	return s.rec("recut", ctx)
}
func (s *stubRelease) Release(ctx context.Context, email, id, head string, confirmFailed bool) error {
	return s.rec(fmt.Sprintf("release%v", confirmFailed), ctx)
}
func (s *stubRelease) RetryRelease(ctx context.Context, email, id string) error {
	return s.rec("retry-release", ctx)
}
func (s *stubRelease) Abandon(ctx context.Context, email, id, head string) error {
	return s.rec("abandon", ctx)
}

func releaseRouter(t *testing.T, rel ReleaseService) (http.Handler, string) {
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
	return NewRouter(db, hub.New(), obsClient, fakeStatter{}, new(atomic.Bool), time.Duration(0), fakePollState{s: "active"}, auth.NewService(db, opts), nil, nil, rel), sid
}

func relCall(h http.Handler, method, path, body, origin, sid string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
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

var releaseRoutes = []struct{ method, path, body string }{
	{"GET", "/api/release/units", ""},
	{"GET", "/api/release/releases", ""},
	{"GET", "/api/release/history", ""},
	{"POST", "/api/release/releases", `{"units":["a"]}`},
	{"PUT", "/api/release/releases/r1/changelog", `{"head":"h","unit":"a","section":"s"}`},
	{"POST", "/api/release/releases/r1/changelog/reset", `{"head":"h","unit":"a"}`},
	{"POST", "/api/release/releases/r1/approve-changelog", `{"head":"h"}`},
}

func TestReleaseRoutesRequireUser(t *testing.T) {
	h, _ := releaseRouter(t, &stubRelease{})
	if rec := relCall(h, "GET", "/api/release/status", "", "", ""); rec.Code != 401 {
		t.Fatalf("status anon: %d", rec.Code)
	}
	for _, rt := range releaseRoutes {
		if rec := relCall(h, rt.method, rt.path, rt.body, dashOrigin, ""); rec.Code != 401 {
			t.Errorf("%s %s anon: %d", rt.method, rt.path, rec.Code)
		}
	}
}

func TestReleaseDisabled404(t *testing.T) {
	h, sid := releaseRouter(t, nil)
	for _, rt := range releaseRoutes {
		if rec := relCall(h, rt.method, rt.path, rt.body, dashOrigin, sid); rec.Code != 404 {
			t.Errorf("%s %s: %d", rt.method, rt.path, rec.Code)
		}
	}
}

func TestReleaseStatusWhenDisabled(t *testing.T) {
	h, sid := releaseRouter(t, nil)
	rec := relCall(h, "GET", "/api/release/status", "", "", sid)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"enabled":false}` {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	h, sid = releaseRouter(t, &stubRelease{})
	rec = relCall(h, "GET", "/api/release/status", "", "", sid)
	if strings.TrimSpace(rec.Body.String()) != `{"enabled":true}` {
		t.Fatalf("%s", rec.Body)
	}
}

func TestReleaseErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code int
		body string
	}{
		{fmt.Errorf("%w: x", release.ErrConflict), 409, "conflict"},
		{release.ErrNotFound, 404, ""},
		{fmt.Errorf("%w: pick a unit", release.ErrBadRequest), 400, "pick a unit"},
	}
	for _, c := range cases {
		h, sid := releaseRouter(t, &stubRelease{err: c.err})
		rec := relCall(h, "POST", "/api/release/releases/r1/approve-qa", `{"head":"h"}`, dashOrigin, sid)
		if rec.Code != c.code || (c.body != "" && strings.TrimSpace(rec.Body.String()) != c.body) {
			t.Errorf("%v: %d %q", c.err, rec.Code, rec.Body)
		}
	}
}

func TestReleaseConflictIs409(t *testing.T) {
	h, sid := releaseRouter(t, &stubRelease{err: release.ErrConflict})
	if rec := relCall(h, "POST", "/api/release/releases/r1/recut", `{"head":"h"}`, dashOrigin, sid); rec.Code != 409 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestReleaseBadSectionIs400(t *testing.T) {
	h, sid := releaseRouter(t, &stubRelease{err: fmt.Errorf("%w: section is empty", release.ErrBadRequest)})
	rec := relCall(h, "PUT", "/api/release/releases/r1/changelog", `{"head":"h","unit":"a","section":""}`, dashOrigin, sid)
	if rec.Code != 400 || strings.Contains(rec.Body.String(), "bad request:") || !strings.Contains(rec.Body.String(), "section is empty") {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

func TestReleaseGitHubFailureIs502NoDetails(t *testing.T) {
	h, sid := releaseRouter(t, &stubRelease{err: errors.New("github GET /repos/secret/path: request failed")})
	for _, rt := range []struct{ m, p, b string }{
		{"POST", "/api/release/releases/r1/abandon", `{"head":"h"}`},
		{"GET", "/api/release/units", ""},
		{"GET", "/api/release/releases", ""},
		{"GET", "/api/release/history", ""},
	} {
		rec := relCall(h, rt.m, rt.p, rt.b, dashOrigin, sid)
		if rec.Code != 502 || strings.Contains(rec.Body.String(), "secret") || strings.TrimSpace(rec.Body.String()) != "github request failed" {
			t.Errorf("%s: %d %q", rt.p, rec.Code, rec.Body)
		}
	}
}

func TestReleaseUnknownAction404(t *testing.T) {
	h, sid := releaseRouter(t, &stubRelease{})
	if rec := relCall(h, "POST", "/api/release/releases/r1/explode", `{}`, dashOrigin, sid); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestReleaseWriteNeedsOrigin(t *testing.T) {
	st := &stubRelease{}
	h, sid := releaseRouter(t, st)
	if rec := relCall(h, "POST", "/api/release/releases/r1/abandon", `{"head":"h"}`, "http://evil.test", sid); rec.Code != 403 {
		t.Fatalf("bad origin: %d", rec.Code)
	}
	if rec := relCall(h, "POST", "/api/release/releases", `{"units":["a"]}`, "", sid); rec.Code != 403 {
		t.Fatalf("no origin: %d", rec.Code)
	}
	if len(st.calls) != 0 {
		t.Fatalf("service called: %v", st.calls)
	}
}

func TestReleaseHappyPaths(t *testing.T) {
	st := &stubRelease{}
	h, sid := releaseRouter(t, st)
	rec := relCall(h, "POST", "/api/release/releases", `{"units":["a"]}`, dashOrigin, sid)
	if rec.Code != 201 || strings.TrimSpace(rec.Body.String()) != `{"id":"rel1"}` || st.email != "alice@percona.com" {
		t.Fatalf("cut: %d %s %q", rec.Code, rec.Body, st.email)
	}
	rec = relCall(h, "PUT", "/api/release/releases/r1/changelog", `{"head":"h","unit":"a","section":"s"}`, dashOrigin, sid)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"head":"h2"}` {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	rec = relCall(h, "POST", "/api/release/releases/r1/release", `{"head":"h","confirm_failed":true}`, dashOrigin, sid)
	if rec.Code != 204 || rec.Body.Len() != 0 || st.calls[len(st.calls)-1] != "releasetrue" {
		t.Fatalf("release: %d %v", rec.Code, st.calls)
	}
	rec = relCall(h, "POST", "/api/release/releases/r1/retry-release", ``, dashOrigin, sid)
	if rec.Code != 204 {
		t.Fatalf("retry-release: %d", rec.Code)
	}
	if rec = relCall(h, "GET", "/api/release/history?page=3", "", "", sid); rec.Code != 200 || st.calls[len(st.calls)-1] != "history3" {
		t.Fatalf("history: %d %v", rec.Code, st.calls)
	}
	for _, p := range []string{"0", "x", "-1"} {
		if rec = relCall(h, "GET", "/api/release/history?page="+p, "", "", sid); rec.Code != 400 {
			t.Errorf("page=%s: %d", p, rec.Code)
		}
	}
	if rec = relCall(h, "POST", "/api/release/releases/r1/abandon", `{"head":`, dashOrigin, sid); rec.Code != 400 {
		t.Fatalf("bad json: %d", rec.Code)
	}
}
