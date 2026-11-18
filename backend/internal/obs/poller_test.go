package obs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/workingset"
)

func TestPreservePublishedAcrossTransientStateChange(t *testing.T) {
	// A target that was succeeded+published briefly goes to blocked (dependency
	// waiting), then is succeeded again by the time the next poller tick runs.
	// Published must be preserved so the worker does not fire a spurious succeeded
	// event for a package that never actually rebuilt.
	prev := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "aarch64", State: "succeeded", Published: true},
		},
	}
	next := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "aarch64", State: "blocked", Published: false},
		},
	}
	preservePackageEnrichment(prev, next)
	if !next.Targets[0].Published {
		t.Error("Published should be preserved when target was succeeded+published and is now blocked")
	}
}

func TestPreservePublishedSucceededToSucceeded(t *testing.T) {
	// Unchanged state: Published must still carry over.
	prev := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "x86_64", State: "succeeded", Published: true},
		},
	}
	next := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "x86_64", State: "succeeded", Published: false},
		},
	}
	preservePackageEnrichment(prev, next)
	if !next.Targets[0].Published {
		t.Error("Published should be preserved when state is unchanged")
	}
}

func TestDoNotPreservePublishedWhenPrevNotPublished(t *testing.T) {
	// Prev was not published (active build in progress) — Published must stay false
	// so PublishStateTask can detect the new publish and fire the succeeded event.
	prev := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "x86_64", State: "building", Published: false},
		},
	}
	next := &model.Package{
		Targets: []model.Target{
			{Repo: "UBI_8", Arch: "x86_64", State: "succeeded", Published: false},
		},
	}
	preservePackageEnrichment(prev, next)
	if next.Targets[0].Published {
		t.Error("Published must not be set when previous target was not published")
	}
}

func TestTargetsChangedDetectsDetailsChange(t *testing.T) {
	prev := &model.Package{
		Targets: []model.Target{
			{Repo: "images", Arch: "x86_64", State: "finished"},
		},
	}
	next := &model.Package{
		Targets: []model.Target{
			{Repo: "images", Arch: "x86_64", State: "finished", Details: "succeeded"},
		},
	}

	if !targetsChanged(prev, next) {
		t.Fatal("expected target details change to be detected")
	}
}

func TestNoPollerRollupEvents(t *testing.T) {
	data, err := os.ReadFile("poller.go")
	if err != nil {
		t.Fatalf("read poller.go: %v", err)
	}
	if strings.Contains(string(data), "AppendEvent(") {
		t.Error("poller.go must not call store.AppendEvent — worker is the sole event emitter")
	}
}

func TestEvictPublishFlagsRefetches(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`<project name="p"><publish><disable/></publish></project>`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "u", "p")
	ctx := context.Background()
	if _, err := c.ProjectPublishFlags(ctx, "gone-project"); err != nil {
		t.Fatal(err)
	}
	c.EvictPublishFlags("gone-project")
	if _, err := c.ProjectPublishFlags(ctx, "gone-project"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected refetch after evict, calls=%d", calls)
	}
}

func TestAwaitingPublishReady(t *testing.T) {
	stored := func(targets ...model.Target) *model.Package {
		return &model.Package{Targets: targets}
	}
	succ := func(repo, arch string, published bool) model.Target {
		return model.Target{Repo: repo, Arch: arch, State: "succeeded", Published: published}
	}
	repoStates := map[string]string{
		"images/x86_64":  "published",
		"images/aarch64": "building",
	}

	cases := []struct {
		name string
		pkg  *model.Package
		want bool
	}{
		{"unpublished target, repo now published", stored(succ("images", "x86_64", false)), true},
		{"unpublished target, repo still building", stored(succ("images", "aarch64", false)), false},
		{"already published", stored(succ("images", "x86_64", true)), false},
		{"building target ignored", stored(model.Target{Repo: "images", Arch: "x86_64", State: "building"}), false},
		{"repo/arch missing from map", stored(succ("other", "x86_64", false)), false},
		{"one ready among several", stored(succ("images", "aarch64", false), succ("images", "x86_64", false)), true},
		{"nil package", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := awaitingPublishReady(tc.pkg, repoStates); got != tc.want {
				t.Fatalf("awaitingPublishReady = %v, want %v", got, tc.want)
			}
		})
	}
}

// The discovery pass is bounded (one call per live project per interval) and
// must not queue behind working-set traffic: its build-results fetch bypasses
// the background rate limiter.
func TestPollerFetchProjectResultsBypassesLimiter(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`<resultlist state="x"></resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	c.SetMinuteBudget(1)
	// Exhaust the minute budget so any limiter-governed request blocks until
	// the next window — far beyond this test's context deadline.
	if err := c.limiter.acquire(context.Background()); err != nil {
		t.Fatalf("drain budget: %v", err)
	}

	p := &Poller{client: NewFleet(NewInstance(InstanceInfo{Slug: "opensuse", Root: "isv:percona"}, c))}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	if _, _, _, err := p.fetchProjectResults(ctx, "ppg:devel:17"); err != nil {
		t.Fatalf("fetch blocked by exhausted limiter (no bypass): %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("OBS endpoint hits = %d, want 1", hits.Load())
	}
}

type stubGate struct {
	active atomic.Bool
	wake   chan struct{}
}

func (s *stubGate) Active() bool               { return s.active.Load() }
func (s *stubGate) Subscribe() <-chan struct{} { return s.wake }

func TestPollerRunGatedByPresence(t *testing.T) {
	var searchHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search/") {
			searchHits.Add(1)
		}
		w.Write([]byte(`<collection matches="0"></collection>`))
	}))
	defer srv.Close()

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	g := &stubGate{wake: make(chan struct{}, 1)}
	p := &Poller{
		client:   NewFleet(NewInstance(InstanceInfo{Slug: "opensuse", Root: "isv:percona"}, NewClient(srv.URL, "u", "p"))),
		db:       db,
		interval: 25 * time.Millisecond,
		gate:     g,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	// Startup tick is unconditional: exactly one discovery fetch.
	waitFor(t, func() bool { return searchHits.Load() == 1 })
	time.Sleep(120 * time.Millisecond) // several ticks pass while idle
	if got := searchHits.Load(); got != 1 {
		t.Fatalf("idle poller fetched: %d discovery calls, want 1 (startup only)", got)
	}

	g.wake <- struct{}{} // wake: immediate tick
	waitFor(t, func() bool { return searchHits.Load() == 2 })

	g.active.Store(true) // active: ticks resume
	waitFor(t, func() bool { return searchHits.Load() >= 3 })
}

func TestConfirmedGone(t *testing.T) {
	x := LegacyInstance(nil, "isv:percona")
	y := NewInstance(InstanceInfo{Slug: "percona", Root: "percona"}, nil)
	all := []*Instance{x, y}
	existing := []*model.Package{
		{Project: "ppg:17", Targets: []model.Target{{Repo: "D", Arch: "a", Instance: "percona"}}},
		{Project: "ppg:18", Targets: []model.Target{{Repo: "R", Arch: "a"}}},
	}
	if confirmedGone(existing, "ppg:17", map[string]bool{"opensuse": true}, all) {
		t.Error("ppg:17 lives on percona, which did not answer")
	}
	if !confirmedGone(existing, "ppg:17", map[string]bool{"opensuse": true, "percona": true}, all) {
		t.Error("all owners answered: gone")
	}
	if confirmedGone(existing, "ppg:18", map[string]bool{"opensuse": true}, all) {
		t.Error("unstamped targets need every instance to answer")
	}
}

func TestPollerCarriesForwardFailedInstance(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	x := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search/project/id"):
			w.Write([]byte(`<collection><project name="isv:percona:ppg:17"/></collection>`))
		case r.URL.Path == "/build/isv:percona:ppg:17/_result":
			w.Write([]byte(`<resultlist><result repository="RHEL_9" arch="x86_64" state="building"><status package="pg" code="building"/></result></resultlist>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer x.Close()
	y := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search/project/id") {
			w.Write([]byte(`<collection><project name="percona:ppg:17"/></collection>`))
			return
		}
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer y.Close()
	fleet := NewFleet(
		NewInstance(InstanceInfo{Slug: "opensuse", Root: "isv:percona"}, NewClient(x.URL, "u", "p")),
		NewInstance(InstanceInfo{Slug: "percona", Root: "percona"}, NewClient(y.URL, "u", "p")),
	)
	now := time.Now().UTC()
	stored := &model.Package{Project: "ppg:17", Name: "deb-only", RollupState: model.RollupFailed, UpdatedAt: now,
		Targets: []model.Target{{Repo: "Debian_12", Arch: "aarch64", State: "failed", Instance: "percona"}}}
	if err := store.UpsertPackageState(db, stored, now); err != nil {
		t.Fatal(err)
	}
	ws := workingset.New(64, time.Minute, time.Minute, 4)
	p := NewPoller(fleet, db, time.Minute, hubpkg.New(), ws, nil)
	p.tick(context.Background())

	got, err := store.GetPackage(db, "ppg:17", "deb-only")
	if err != nil || got == nil {
		t.Fatalf("package on the failed instance was garbage-collected (err=%v)", err)
	}
	if len(got.Targets) != 1 || got.Targets[0].State != "failed" {
		t.Errorf("carried package changed: %+v", got.Targets)
	}
}

// An instance that fails discovery (e.g. down at startup, so it never set
// membership) must be treated as down for the tick: its packages in shared
// projects are carried forward, never garbage-collected.
func TestPollerCarriesForwardInstanceDownAtDiscovery(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	x := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search/project/id"):
			w.Write([]byte(`<collection><project name="isv:percona:ppg:17"/></collection>`))
		case r.URL.Path == "/build/isv:percona:ppg:17/_result":
			w.Write([]byte(`<resultlist><result repository="RHEL_9" arch="x86_64" state="building"><status package="pg" code="building"/></result></resultlist>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer x.Close()
	y := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer y.Close()
	fleet := NewFleet(
		NewInstance(InstanceInfo{Slug: "opensuse", Root: "isv:percona"}, NewClient(x.URL, "u", "p")),
		NewInstance(InstanceInfo{Slug: "percona", Root: "percona"}, NewClient(y.URL, "u", "p")),
	)
	now := time.Now().UTC()
	debOnly := &model.Package{Project: "ppg:17", Name: "deb-only", RollupState: model.RollupFailed, UpdatedAt: now,
		Targets: []model.Target{{Repo: "Debian_12", Arch: "aarch64", State: "failed", Instance: "percona"}}}
	mixed := &model.Package{Project: "ppg:17", Name: "pg", RollupState: model.RollupFailed, UpdatedAt: now,
		Targets: []model.Target{
			{Repo: "RHEL_9", Arch: "x86_64", State: "succeeded", Instance: "opensuse"},
			{Repo: "Debian_12", Arch: "aarch64", State: "failed", Instance: "percona"},
		}}
	for _, pkg := range []*model.Package{debOnly, mixed} {
		if err := store.UpsertPackageState(db, pkg, now); err != nil {
			t.Fatal(err)
		}
	}
	ws := workingset.New(64, time.Minute, time.Minute, 4)
	p := NewPoller(fleet, db, time.Minute, hubpkg.New(), ws, nil)
	p.tick(context.Background())

	got, err := store.GetPackage(db, "ppg:17", "deb-only")
	if err != nil || got == nil {
		t.Fatalf("package on the undiscovered instance was garbage-collected (err=%v)", err)
	}
	if len(got.Targets) != 1 || got.Targets[0].State != "failed" || got.Targets[0].Instance != "percona" {
		t.Errorf("carried package changed: %+v", got.Targets)
	}
	pg, err := store.GetPackage(db, "ppg:17", "pg")
	if err != nil || pg == nil {
		t.Fatalf("mixed package missing (err=%v)", err)
	}
	byRepo := map[string]model.Target{}
	for _, tg := range pg.Targets {
		byRepo[tg.Repo] = tg
	}
	if len(pg.Targets) != 2 || byRepo["Debian_12"].State != "failed" || byRepo["RHEL_9"].State != "building" {
		t.Errorf("mixed package: want fresh RHEL_9 + carried Debian_12, got %+v", pg.Targets)
	}
}

// An instance that answers discovery-failed but still serves _result must not
// get its fresh targets duplicated by carry-forward.
func TestPollerNoDuplicateWhenUndiscoveredInstanceAnswersResults(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	x := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search/project/id"):
			w.Write([]byte(`<collection><project name="isv:percona:ppg:17"/></collection>`))
		case r.URL.Path == "/build/isv:percona:ppg:17/_result":
			w.Write([]byte(`<resultlist><result repository="RHEL_9" arch="x86_64" state="building"><status package="pg" code="building"/></result></resultlist>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer x.Close()
	y := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/build/percona:ppg:17/_result" {
			w.Write([]byte(`<resultlist><result repository="Debian_12" arch="aarch64" state="building"><status package="pg" code="succeeded"/></result></resultlist>`))
			return
		}
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer y.Close()
	fleet := NewFleet(
		NewInstance(InstanceInfo{Slug: "opensuse", Root: "isv:percona"}, NewClient(x.URL, "u", "p")),
		NewInstance(InstanceInfo{Slug: "percona", Root: "percona"}, NewClient(y.URL, "u", "p")),
	)
	now := time.Now().UTC()
	mixed := &model.Package{Project: "ppg:17", Name: "pg", RollupState: model.RollupFailed, UpdatedAt: now,
		Targets: []model.Target{
			{Repo: "RHEL_9", Arch: "x86_64", State: "succeeded", Instance: "opensuse"},
			{Repo: "Debian_12", Arch: "aarch64", State: "failed", Instance: "percona"},
		}}
	if err := store.UpsertPackageState(db, mixed, now); err != nil {
		t.Fatal(err)
	}
	fleet.SeedOwners([]*model.Package{mixed})
	p := NewPoller(fleet, db, time.Minute, hubpkg.New(), workingset.New(64, time.Minute, time.Minute, 4), nil)
	p.tick(context.Background())

	pg, err := store.GetPackage(db, "ppg:17", "pg")
	if err != nil || pg == nil {
		t.Fatalf("mixed package missing (err=%v)", err)
	}
	if len(pg.Targets) != 2 {
		t.Fatalf("want 2 targets (no duplicate), got %+v", pg.Targets)
	}
	for _, tg := range pg.Targets {
		if tg.Repo == "Debian_12" && tg.State != "succeeded" {
			t.Errorf("Debian_12 must be the fresh state, got %+v", tg)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

// A published release container that already has tags must still be queued
// for a tag re-read on the first tick after startup, and then not again
// until releaseTagsInterval has passed: a new release into the same project
// changes no build state, so nothing else would refresh its version.
func TestPollerRefreshesReleaseContainerTags(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search/project/id"):
			w.Write([]byte(`<collection><project name="isv:percona:ppg:releases:14:containers"/></collection>`))
		case r.URL.Path == "/build/isv:percona:ppg:releases:14:containers/_result":
			w.Write([]byte(`<resultlist><result repository="ubi9" arch="x86_64" state="published"><status package="img" code="disabled"/><status package="rpm" code="disabled"/></result></resultlist>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	fleet := NewFleet(NewInstance(InstanceInfo{Slug: "opensuse", Root: "isv:percona"}, NewClient(srv.URL, "u", "p")))

	now := time.Now().UTC()
	yes, no := true, false
	for _, pkg := range []*model.Package{
		{Project: "ppg:releases:14:containers", Name: "img", IsRelease: true, IsContainer: &yes,
			RollupState: model.RollupPublished, Version: "14.24-1", ContainerTags: []string{"14.24-1", "14"}, UpdatedAt: now},
		{Project: "ppg:releases:14:containers", Name: "rpm", IsRelease: true, IsContainer: &no,
			RollupState: model.RollupPublished, Version: "14.24-1", UpdatedAt: now},
	} {
		if err := store.UpsertPackageState(db, pkg, now); err != nil {
			t.Fatal(err)
		}
	}

	ws := workingset.New(64, time.Minute, time.Minute, 4)
	p := NewPoller(fleet, db, time.Minute, hubpkg.New(), ws, nil)

	dispatched := func() []string {
		var names []string
		for {
			select {
			case job := <-ws.Dispatch():
				for _, pkg := range job.Pkgs {
					names = append(names, pkg.Name)
					ws.Remove(pkg.Project + "/" + pkg.Name)
				}
			case <-time.After(100 * time.Millisecond):
				return names
			}
		}
	}

	p.tick(context.Background())
	if got := dispatched(); len(got) != 1 || got[0] != "img" {
		t.Fatalf("first tick dispatched %v, want [img]", got)
	}
	p.tick(context.Background())
	if got := dispatched(); len(got) != 0 {
		t.Fatalf("second tick dispatched %v, want nothing", got)
	}
	p.releaseTagsAt["ppg:releases:14:containers"] = time.Now().Add(-releaseTagsInterval)
	p.tick(context.Background())
	if got := dispatched(); len(got) != 1 || got[0] != "img" {
		t.Fatalf("tick after the interval dispatched %v, want [img]", got)
	}
}
