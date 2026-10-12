package obs_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
)

func TestBuildStateTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<resultlist>
          <result project="isv:percona" repository="repo" arch="x86_64" state="succeeded">
            <status package="mypkg" code="succeeded"/>
          </result>
        </resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		RollupState: model.RollupFailed,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "failed"}},
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.BuildStateTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.RollupState != model.RollupSucceeded {
		t.Errorf("expected succeeded rollup, got %s", pkg.RollupState)
	}
	if pkg.OKTargets != 1 {
		t.Errorf("expected 1 OK target, got %d", pkg.OKTargets)
	}
}

func TestBlockedReasonTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<resultlist>
          <result project="isv:percona" repository="repo" arch="x86_64" state="building">
            <status package="mypkg" code="blocked">
              <details>not installable</details>
            </status>
          </result>
        </resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		RollupState: model.RollupBlocked,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "blocked"}},
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.BlockedReasonTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.Targets[0].BlockedBy != "not installable" {
		t.Errorf("expected BlockedBy to be set, got %q", pkg.Targets[0].BlockedBy)
	}
}

func TestBlockedReasonTaskSkipsWhenNoBlocked(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`<resultlist></resultlist>`))
	}))
	defer srv.Close()

	c := obs.SingleFleet(obs.NewClient(srv.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "p", Name: "pkg",
		Targets: []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
	}
	if err := (obs.BlockedReasonTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no OBS call when no blocked targets, got %d", calls)
	}
}

func TestBlockedReasonTaskSkipsWhenFresh(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `<resultlist></resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona", Name: "mypkg",
		Targets: []model.Target{{
			Repo: "repo", Arch: "x86_64", State: "blocked",
			BlockedBy:          "waiting on libfoo",
			BlockedByFetchedAt: time.Now().UTC().Add(-time.Minute), // fresh
		}},
	}
	if err := (obs.BlockedReasonTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no OBS call for fresh blocked reason, got %d", calls)
	}
	if pkg.Targets[0].BlockedBy != "waiting on libfoo" {
		t.Errorf("cached BlockedBy lost: %q", pkg.Targets[0].BlockedBy)
	}
}

func TestBlockedReasonTaskRefetchesWhenStale(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<resultlist>
          <result project="isv:percona" repository="repo" arch="x86_64" state="building">
            <status package="mypkg" code="blocked">
              <details>now waiting on libbar</details>
            </status>
          </result>
        </resultlist>`)
	}))
	defer ts.Close()

	stale := time.Now().UTC().Add(-6 * time.Minute)
	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona", Name: "mypkg",
		Targets: []model.Target{{
			Repo: "repo", Arch: "x86_64", State: "blocked",
			BlockedBy:          "waiting on libfoo",
			BlockedByFetchedAt: stale,
		}},
	}
	if err := (obs.BlockedReasonTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.Targets[0].BlockedBy != "now waiting on libbar" {
		t.Errorf("stale BlockedBy not refreshed: %q", pkg.Targets[0].BlockedBy)
	}
	if !pkg.Targets[0].BlockedByFetchedAt.After(stale) {
		t.Error("BlockedByFetchedAt not re-stamped after refetch")
	}
}

func TestPublishStateTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<resultlist>
          <result repository="Ubuntu_24.04" arch="x86_64" state="published">
            <status package="mypkg" code="succeeded"/>
          </result>
          <result repository="Ubuntu_24.04" arch="aarch64" state="building">
            <status package="mypkg" code="succeeded"/>
          </result>
        </resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona",
		Name:    "mypkg",
		Targets: []model.Target{
			{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "succeeded"},  // repo published → Published=true
			{Repo: "Ubuntu_24.04", Arch: "aarch64", State: "succeeded"}, // repo building → Published=false
			{Repo: "RockyLinux_9", Arch: "x86_64", State: "building"},   // not succeeded → skip
		},
	}

	task := obs.PublishStateTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if !pkg.Targets[0].Published {
		t.Error("expected Ubuntu_24.04/x86_64 to be Published=true (repo state=published, target succeeded)")
	}
	if pkg.Targets[1].Published {
		t.Error("expected Ubuntu_24.04/aarch64 to be Published=false (repo state=building)")
	}
	if pkg.Targets[2].Published {
		t.Error("expected RockyLinux_9/x86_64 to be Published=false (target not succeeded)")
	}
}

// Succeeded-unpublished targets in repos that never publish are futile to
// check: their repo state stays "unpublished" forever. PublishStateTask must
// consult the (cached) publish flags and skip the _result fetch entirely.
func TestPublishStateTaskSkipsNonPublishingRepos(t *testing.T) {
	var metaCalls, resultCalls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_meta") {
			atomic.AddInt32(&metaCalls, 1)
			fmt.Fprint(w, `<project name="p"><publish><disable/></publish></project>`)
			return
		}
		atomic.AddInt32(&resultCalls, 1)
		fmt.Fprint(w, `<resultlist></resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona:PR:pr-1:ppg:17", Name: "mypkg",
		Targets: []model.Target{{Repo: "Ubuntu_24.04", Arch: "x86_64", State: "succeeded"}},
	}
	if err := (obs.PublishStateTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&resultCalls) != 0 {
		t.Fatalf("expected no publish-state check for non-publishing repo, got %d", resultCalls)
	}
	if atomic.LoadInt32(&metaCalls) != 1 {
		t.Fatalf("expected exactly 1 cached _meta fetch, got %d", metaCalls)
	}
}

func TestBuildReasonTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<reason>
          <explain>meta change</explain>
          <packagechange change="md5sum" key="libfoo"/>
        </reason>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		RollupState: model.RollupBuilding,
		Targets: []model.Target{
			{Repo: "repo", Arch: "x86_64", State: "building"},
			{Repo: "repo", Arch: "aarch64", State: "succeeded"}, // should be skipped
		},
		UpdatedAt: time.Now().UTC(),
	}

	task := obs.BuildReasonTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.Targets[0].BuildReason != "meta change" {
		t.Errorf("expected 'meta change', got %q", pkg.Targets[0].BuildReason)
	}
	if len(pkg.Targets[0].BuildReasonPackages) != 1 || pkg.Targets[0].BuildReasonPackages[0] != "libfoo" {
		t.Errorf("unexpected BuildReasonPackages: %v", pkg.Targets[0].BuildReasonPackages)
	}
	if pkg.Targets[1].BuildReason != "" {
		t.Error("succeeded target should have no BuildReason")
	}
}

func TestBuildReasonTaskRetriesOnTransientError(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			// Simulate a transient server error on the first two attempts.
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `<reason><explain>source change</explain></reason>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		RollupState: model.RollupBuilding,
		Targets: []model.Target{
			{Repo: "repo", Arch: "x86_64", State: "building"},
		},
		UpdatedAt: time.Now().UTC(),
	}

	task := obs.BuildReasonTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.Targets[0].BuildReason != "source change" {
		t.Errorf("expected 'source change', got %q", pkg.Targets[0].BuildReason)
	}
	if attempts != 3 {
		t.Errorf("expected 3 server attempts (2 retries), got %d", attempts)
	}
}

func TestBuildReasonTaskSkipsCachedTargets(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `<reason><explain>new cycle</explain></reason>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona", Name: "mypkg",
		Targets: []model.Target{
			{Repo: "repo", Arch: "x86_64", State: "building", BuildReason: "meta change"}, // cached → skip
			{Repo: "repo", Arch: "aarch64", State: "building"},                            // empty → fetch
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := (obs.BuildReasonTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected exactly 1 fetch (cached target skipped), got %d", got)
	}
	if pkg.Targets[0].BuildReason != "meta change" {
		t.Errorf("cached reason overwritten: %q", pkg.Targets[0].BuildReason)
	}
	if pkg.Targets[1].BuildReason != "new cycle" {
		t.Errorf("uncached target not fetched: %q", pkg.Targets[1].BuildReason)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestPackageTypeTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<sourceinfo><filename>Dockerfile</filename></sourceinfo>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17:containers",
		Name:        "percona-distribution-postgresql",
		RollupState: model.RollupSucceeded,
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.PackageTypeTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.IsContainer == nil || !*pkg.IsContainer {
		t.Error("expected IsContainer=true for Dockerfile package")
	}
}

func TestPackageTypeTaskRPM(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<sourceinfo><filename>percona-pg_tde.spec</filename></sourceinfo>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "percona-pg_tde",
		RollupState: model.RollupSucceeded,
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.PackageTypeTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.IsContainer != nil && *pkg.IsContainer {
		t.Error("expected IsContainer=false for spec-only package")
	}
}

func TestPackageTypeTaskSkipsWhenAlreadySet(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		fmt.Fprint(w, `<sourceinfo><filename>Dockerfile</filename></sourceinfo>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "percona-pg_tde",
		IsContainer: boolPtr(false),
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.PackageTypeTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("PackageTypeTask should not call OBS when IsContainer is already set")
	}
	if pkg.IsContainer == nil || *pkg.IsContainer {
		t.Error("IsContainer should remain false when task is skipped")
	}
}

func TestVersionTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<resultlist>
			<result repository="UBI_9" arch="x86_64" state="published">
				<status package="percona-pg_tde" code="succeeded" versrel="17.5-1"/>
			</result>
		</resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17",
		Name:        "percona-pg_tde",
		RollupState: model.RollupSucceeded,
		IsContainer: boolPtr(false),
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.VersionTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "17.5-1" {
		t.Errorf("expected %q, got %q", "17.5-1", pkg.Version)
	}
}

func TestVersionTaskSkipsContainers(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		fmt.Fprint(w, `<resultlist></resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mycontainer",
		IsContainer: boolPtr(true),
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.VersionTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("VersionTask should not call OBS for container packages")
	}
}

func TestContainerTagsTask(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".containerinfo") {
			fmt.Fprint(w, `{"tags":["percona-distribution-postgresql:18.4-1-1.7","percona-distribution-postgresql:18.4-1"]}`)
		} else {
			fmt.Fprint(w, `<binarylist>
				<binary filename="percona-distribution-postgresql.x86_64-1.7.containerinfo" size="1" mtime="1"/>
			</binarylist>`)
		}
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:17:containers",
		Name:        "percona-distribution-postgresql",
		RollupState: model.RollupSucceeded,
		IsContainer: boolPtr(true),
		Targets:     []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.ContainerTagsTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "18.4-1-1.7" {
		t.Errorf("expected Version %q, got %q", "18.4-1-1.7", pkg.Version)
	}
	if len(pkg.ContainerTags) != 2 {
		t.Fatalf("expected 2 ContainerTags, got %d: %v", len(pkg.ContainerTags), pkg.ContainerTags)
	}
	if pkg.ContainerTags[1] != "18.4-1" {
		t.Errorf("expected ContainerTags[1] %q, got %q", "18.4-1", pkg.ContainerTags[1])
	}
}

func TestContainerTagsTaskSkipsNonContainers(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		IsContainer: boolPtr(false),
		Targets:     []model.Target{{Repo: "UBI_9", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   time.Now().UTC(),
	}

	task := obs.ContainerTagsTask{}
	if err := task.Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("ContainerTagsTask should not call OBS for non-container packages")
	}
}

// resultXML builds a single-package resultlist with one <result> per (repo, arch, code).
func resultXML(entries [][3]string) string {
	var sb strings.Builder
	sb.WriteString(`<resultlist>`)
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf(
			`<result project="isv:percona" repository="%s" arch="%s" state="building">
				<status package="mypkg" code="%s"/>
			</result>`, e[0], e[1], e[2]))
	}
	sb.WriteString(`</resultlist>`)
	return sb.String()
}

func TestBuildStateTaskPreservationMatrix(t *testing.T) {
	fetchedAt := time.Now().UTC().Add(-time.Minute)
	enriched := func(state string) model.Target {
		return model.Target{
			Repo: "repo", Arch: "x86_64", State: state,
			BlockedBy:           "waiting on libfoo",
			BuildReason:         "meta change",
			BuildReasonPackages: []string{"libfoo"},
			BlockedByFetchedAt:  fetchedAt,
		}
	}

	cases := []struct {
		name         string
		prevTargets  []model.Target
		serverStates [][3]string // repo, arch, code
		wantPreserve bool
		wantStable   bool
	}{
		{
			name:         "state unchanged: preserved and stable",
			prevTargets:  []model.Target{enriched("blocked")},
			serverStates: [][3]string{{"repo", "x86_64", "blocked"}},
			wantPreserve: true,
			wantStable:   true,
		},
		{
			name:         "state changed: wiped and unstable",
			prevTargets:  []model.Target{enriched("blocked")},
			serverStates: [][3]string{{"repo", "x86_64", "building"}},
			wantPreserve: false,
			wantStable:   false,
		},
		{
			name:         "target added: unstable",
			prevTargets:  []model.Target{enriched("blocked")},
			serverStates: [][3]string{{"repo", "x86_64", "blocked"}, {"repo", "aarch64", "building"}},
			wantPreserve: true, // the matching target still preserves
			wantStable:   false,
		},
		{
			name:         "target removed: unstable",
			prevTargets:  []model.Target{enriched("blocked"), {Repo: "repo", Arch: "aarch64", State: "building"}},
			serverStates: [][3]string{{"repo", "x86_64", "blocked"}},
			wantPreserve: true,
			wantStable:   false,
		},
		{
			name:         "no previous targets: unstable",
			prevTargets:  nil,
			serverStates: [][3]string{{"repo", "x86_64", "blocked"}},
			wantPreserve: false, // nothing to preserve from
			wantStable:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, resultXML(tc.serverStates))
			}))
			defer ts.Close()

			c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
			pkg := &model.Package{
				Project: "isv:percona", Name: "mypkg",
				Targets: tc.prevTargets, UpdatedAt: time.Now().UTC(),
				CacheWarm: true, // simulate a pointer that completed a prior pass
			}
			if err := (obs.BuildStateTask{}).Run(context.Background(), c, pkg, nil); err != nil {
				t.Fatal(err)
			}

			if pkg.TargetsStable != tc.wantStable {
				t.Errorf("TargetsStable = %v, want %v", pkg.TargetsStable, tc.wantStable)
			}
			var got *model.Target
			for i := range pkg.Targets {
				if pkg.Targets[i].Repo == "repo" && pkg.Targets[i].Arch == "x86_64" {
					got = &pkg.Targets[i]
					break
				}
			}
			if got == nil {
				t.Fatal("repo/x86_64 target missing from result")
			}
			if tc.wantPreserve {
				if got.BlockedBy != "waiting on libfoo" || got.BuildReason != "meta change" ||
					len(got.BuildReasonPackages) != 1 || !got.BlockedByFetchedAt.Equal(fetchedAt) {
					t.Errorf("enrichment not preserved: %+v", got)
				}
			} else {
				if got.BlockedBy != "" || got.BuildReason != "" ||
					got.BuildReasonPackages != nil || !got.BlockedByFetchedAt.IsZero() {
					t.Errorf("enrichment not wiped: %+v", got)
				}
			}
		})
	}
}

// A pointer seeded from the DB (or replaced by MQ) has CacheWarm=false even when
// its targets match live OBS state — the first pass must not report stability.
func TestBuildStateTaskColdPointerIsNeverStable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, resultXML([][3]string{{"repo", "x86_64", "blocked"}}))
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona", Name: "mypkg",
		// Same state as the server reports, but CacheWarm is false (cold start).
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "blocked"}},
		UpdatedAt: time.Now().UTC(),
	}
	if err := (obs.BuildStateTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.TargetsStable {
		t.Error("cold pointer must not be stable on its first pass")
	}
	if !pkg.CacheWarm {
		t.Error("CacheWarm must be set after a completed pass")
	}
}

// The second pass over the same pointer (now warm) reports stability.
func TestBuildStateTaskSecondPassIsStable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, resultXML([][3]string{{"repo", "x86_64", "blocked"}}))
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona", Name: "mypkg",
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "blocked"}},
		UpdatedAt: time.Now().UTC(),
	}
	if err := (obs.BuildStateTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if err := (obs.BuildStateTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if !pkg.TargetsStable {
		t.Error("second pass over a warm pointer with unchanged state must be stable")
	}
}

// Release chain has no BuildStateTask, so TargetsStable is never set and release
// containers keep fetching their tags every pass (accepted in the design).
func TestContainerTagsTaskReleaseAlwaysFetches(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if strings.HasSuffix(r.URL.Path, ".containerinfo") {
			fmt.Fprint(w, `{"tags":["percona-distribution-postgresql:18.4-1-1.7","percona-distribution-postgresql:18.4-1"]}`)
		} else {
			fmt.Fprint(w, `<binarylist>
				<binary filename="percona-distribution-postgresql.x86_64-1.7.containerinfo" size="1" mtime="1"/>
			</binarylist>`)
		}
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona:ppg:releases:17", Name: "percona-distribution-postgresql",
		IsRelease:     true,
		IsContainer:   boolPtr(true),
		ContainerTags: []string{"18.4-1-1.7", "18.4-1"},
		// TargetsStable deliberately false: the release chain never sets it.
		Targets:   []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt: time.Now().UTC(),
	}
	if err := (obs.ContainerTagsTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) == 0 {
		t.Fatal("release container must keep fetching tags (TargetsStable never set in release chain)")
	}
}

func TestVersionTaskSkipsWhenStable(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `<resultlist></resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "percona-pg_tde",
		IsContainer:   boolPtr(false),
		Version:       "17.5-1",
		TargetsStable: true,
		UpdatedAt:     time.Now().UTC(),
	}
	if err := (obs.VersionTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no OBS call when version known and targets stable, got %d", calls)
	}
	if pkg.Version != "17.5-1" {
		t.Errorf("version changed: %q", pkg.Version)
	}
}

func TestVersionTaskFetchesWhenUnstable(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `<resultlist>
			<result repository="UBI_9" arch="x86_64" state="published">
				<status package="percona-pg_tde" code="succeeded" versrel="17.5-2"/>
			</result>
		</resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "percona-pg_tde",
		IsContainer:   boolPtr(false),
		Version:       "17.5-1",
		TargetsStable: false, // a target changed → version may have moved
		UpdatedAt:     time.Now().UTC(),
	}
	if err := (obs.VersionTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 OBS call when unstable, got %d", calls)
	}
	if pkg.Version != "17.5-2" {
		t.Errorf("version not refreshed: %q", pkg.Version)
	}
}

func TestContainerTagsTaskSkipsWhenStable(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17:containers", Name: "percona-distribution-postgresql",
		IsContainer:   boolPtr(true),
		ContainerTags: []string{"18.4-1-1.7", "18.4-1"},
		TargetsStable: true,
		Targets:       []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:     time.Now().UTC(),
	}
	if err := (obs.ContainerTagsTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no OBS call when tags known and targets stable, got %d", calls)
	}
}

func TestContainerTagsTaskFetchesWhenUnstable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".containerinfo") {
			fmt.Fprint(w, `{"tags":["percona-distribution-postgresql:18.4-2-1.8","percona-distribution-postgresql:18.4-2"]}`)
		} else {
			fmt.Fprint(w, `<binarylist>
				<binary filename="percona-distribution-postgresql.x86_64-1.8.containerinfo" size="1" mtime="1"/>
			</binarylist>`)
		}
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17:containers", Name: "percona-distribution-postgresql",
		IsContainer:   boolPtr(true),
		ContainerTags: []string{"18.4-1-1.7", "18.4-1"},
		TargetsStable: false, // new build landed
		Targets:       []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:     time.Now().UTC(),
	}
	if err := (obs.ContainerTagsTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if len(pkg.ContainerTags) != 2 || pkg.ContainerTags[0] != "18.4-2-1.8" {
		t.Errorf("tags not refreshed: %v", pkg.ContainerTags)
	}
	if pkg.Version != "18.4-2-1.8" {
		t.Errorf("version not updated from refreshed tags: %q", pkg.Version)
	}
}

// Negative-result caching: under stable targets, an empty reason was already
// confirmed empty in this exact state (e.g. unresolvable targets, which OBS
// has no reason for) — no refetch until a state transition.
func TestBuildReasonTaskSkipsWhenStable(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `<reason><explain>should never be fetched</explain></reason>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona", Name: "mypkg",
		TargetsStable: true,
		Targets: []model.Target{
			{Repo: "repo", Arch: "x86_64", State: "unresolvable"}, // empty reason, stable → skip
			{Repo: "repo", Arch: "aarch64", State: "building", BuildReason: "meta change"},
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := (obs.BuildReasonTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("expected no OBS calls under stable targets, got %d", got)
	}
	if pkg.Targets[0].BuildReason != "" {
		t.Errorf("unresolvable target reason should stay empty: %q", pkg.Targets[0].BuildReason)
	}
}

func TestVersionTaskSkipsEmptyVersionWhenStable(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		fmt.Fprint(w, `<resultlist></resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "percona-pg_tde",
		IsContainer:   boolPtr(false),
		Version:       "", // never built — empty versrel is now negative-cached
		TargetsStable: true,
		UpdatedAt:     time.Now().UTC(),
	}
	if err := (obs.VersionTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no OBS call for empty version under stable targets, got %d", calls)
	}
}

func TestBuildStateTaskUsesPrefetchedEnv(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		RollupState: model.RollupFailed,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "failed"}},
	}
	env := &obs.Env{BuildStates: []obs.PackageBuildState{
		{Project: "isv:percona", Repo: "repo", Arch: "x86_64", Package: "mypkg", State: "succeeded"},
	}}

	if err := (obs.BuildStateTask{}).Run(context.Background(), c, pkg, env); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Errorf("expected no HTTP requests, got %d", hits.Load())
	}
	if pkg.RollupState != model.RollupSucceeded {
		t.Errorf("expected succeeded rollup, got %s", pkg.RollupState)
	}
}

func TestPublishStateTaskUsesPrefetchedEnv(t *testing.T) {
	// Serve only the _meta publish-flags request; _result must not be hit.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_meta") {
			fmt.Fprint(w, `<project name="isv:percona"/>`)
			return
		}
		http.Error(w, "must not be called: "+r.URL.Path, http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona",
		Name:        "mypkg",
		RollupState: model.RollupSucceeded,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "succeeded"}},
	}
	env := &obs.Env{RepoStates: map[string]string{"repo/x86_64": "published"}}

	if err := (obs.PublishStateTask{}).Run(context.Background(), c, pkg, env); err != nil {
		t.Fatal(err)
	}
	if !pkg.Targets[0].Published {
		t.Error("expected target published from prefetched repo states")
	}
	if pkg.RollupState != model.RollupPublished {
		t.Errorf("expected published rollup, got %s", pkg.RollupState)
	}
}

func TestBinariesCheckTaskUsesPrefetchedEnv(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:releases:17",
		Name:        "mypkg",
		IsRelease:   true,
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "succeeded"}},
	}
	env := &obs.Env{RepoStates: map[string]string{"repo/x86_64": "published"}}

	if err := (obs.BinariesCheckTask{}).Run(context.Background(), c, pkg, env); err != nil {
		t.Fatal(err)
	}
	if pkg.RollupState != model.RollupPublished {
		t.Errorf("expected published rollup, got %s", pkg.RollupState)
	}
}

func TestBuildStateTaskCarriesForwardFailedInstance(t *testing.T) {
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pg", CacheWarm: true,
		Targets: []model.Target{
			{Repo: "RHEL_9", Arch: "x86_64", State: "succeeded", Instance: "opensuse", Published: true},
			{Repo: "Debian_12", Arch: "aarch64", State: "failed", Instance: "percona", Details: "boom", BuildReason: "source change"},
		},
	}
	env := &obs.Env{
		BuildStates:     []obs.PackageBuildState{{Project: pkg.Project, Package: "pg", Repo: "RHEL_9", Arch: "x86_64", State: "building", Instance: "opensuse"}},
		FailedInstances: []string{"percona"},
	}
	if err := (obs.BuildStateTask{}).Run(context.Background(), nil, pkg, env); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Targets) != 2 {
		t.Fatalf("carried target dropped: %+v", pkg.Targets)
	}
	var deb model.Target
	for _, tg := range pkg.Targets {
		if tg.Repo == "Debian_12" {
			deb = tg
		}
	}
	if deb.State != "failed" || deb.Instance != "percona" || deb.BuildReason != "source change" {
		t.Errorf("carried target lost state/enrichment: %+v", deb)
	}
	if pkg.RollupState != model.RollupFailed {
		t.Errorf("rollup must include carried targets, got %s", pkg.RollupState)
	}
}

func TestBuildStateTaskKeepsPublishedOnCarriedTarget(t *testing.T) {
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pg",
		Targets: []model.Target{{Repo: "Debian_12", Arch: "aarch64", State: "succeeded", Instance: "percona", Published: true}},
	}
	env := &obs.Env{BuildStates: []obs.PackageBuildState{}, FailedInstances: []string{"percona"}}
	if err := (obs.BuildStateTask{}).Run(context.Background(), nil, pkg, env); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Targets) != 1 || !pkg.Targets[0].Published {
		t.Fatalf("carried target must keep Published: %+v", pkg.Targets)
	}
}

// Released containers have their builds disabled after release and, in the
// per-base-image layout, live in ubi8/ubi9/… repos (not the legacy "images"
// repo). The release fallback must still discover those targets, fetch the
// tags and promote the package to published so it gets CVE-scanned.
func TestContainerTagsTaskReleaseDisabledPerBaseImageRepos(t *testing.T) {
	var tagRepos []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_result"):
			fmt.Fprint(w, `<resultlist>
				<result repository="ubi8" arch="x86_64" state="published"><status package="percona-pgbouncer" code="disabled"/></result>
				<result repository="ubi9" arch="x86_64" state="published"><status package="percona-pgbouncer" code="disabled"/></result>
			</resultlist>`)
		case strings.HasSuffix(r.URL.Path, ".containerinfo"):
			fmt.Fprint(w, `{"tags":["percona-pgbouncer:1.24.1-1","percona-pgbouncer:latest"]}`)
		default:
			tagRepos = append(tagRepos, strings.Split(r.URL.Path, "/")[3])
			fmt.Fprint(w, `<binarylist><binary filename="percona-pgbouncer.x86_64-1.containerinfo" size="1" mtime="1"/></binarylist>`)
		}
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "isv:percona:ppg:releases:17:containers",
		Name:        "percona-pgbouncer",
		IsRelease:   true,
		RollupState: model.RollupSucceeded,
		IsContainer: boolPtr(true),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := (obs.ContainerTagsTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Targets) != 2 {
		t.Fatalf("expected the ubi8 and ubi9 targets to be discovered, got %+v", pkg.Targets)
	}
	if len(pkg.ContainerTags) != 2 || pkg.ContainerTags[1] != "latest" {
		t.Errorf("expected tags to be fetched, got %v", pkg.ContainerTags)
	}
	if pkg.RollupState != model.RollupPublished {
		t.Errorf("expected rollup published (so the CVE scanner picks it up), got %s", pkg.RollupState)
	}
	if len(tagRepos) == 0 {
		t.Error("expected the containerinfo listing to be fetched from a discovered repo")
	}
}

// Aggregate packages report an empty versrel; VersionTask follows the
// _aggregate link and takes the source package's versrel instead.
func TestVersionTaskResolvesAggregateVersion(t *testing.T) {
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case strings.HasSuffix(r.URL.Path, "/_aggregate"):
			fmt.Fprint(w, `<aggregatelist>
				<aggregate project="isv:percona:ppg:common:deps">
					<package>etcd</package>
					<repository source="UBI_9" target="UBI_9"/>
				</aggregate>
			</aggregatelist>`)
		case strings.Contains(r.URL.Path, "/build/isv:percona:ppg:common:deps/_result"):
			fmt.Fprint(w, `<resultlist>
				<result repository="UBI_9" arch="x86_64" state="published">
					<status package="etcd" code="succeeded" versrel="3.5.1-1"/>
				</result>
			</resultlist>`)
		default:
			fmt.Fprint(w, `<resultlist>
				<result repository="UBI_9" arch="x86_64" state="published">
					<status package="etcd" code="succeeded"/>
				</result>
			</resultlist>`)
		}
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{
		Project:     "ppg:staging:18",
		Name:        "etcd",
		RollupState: model.RollupSucceeded,
		IsContainer: boolPtr(false),
		UpdatedAt:   time.Now().UTC(),
	}

	if err := (obs.VersionTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "3.5.1-1" {
		t.Errorf("expected aggregate source version %q, got %q (paths %v)", "3.5.1-1", pkg.Version, paths)
	}
}

// A package with an empty versrel that is not an aggregate (no _aggregate
// file) keeps an empty version.
func TestVersionTaskEmptyVersrelNonAggregate(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_aggregate") {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<resultlist><result repository="UBI_9" arch="x86_64" state="published">
			<status package="never-built" code="unresolvable"/></result></resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{Project: "ppg:staging:18", Name: "never-built", IsContainer: boolPtr(false), UpdatedAt: time.Now().UTC()}
	if err := (obs.VersionTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "" {
		t.Errorf("expected empty version, got %q", pkg.Version)
	}
}

// A non-empty versrel never triggers the aggregate lookup.
func TestVersionTaskSkipsAggregateLookupWhenBuilt(t *testing.T) {
	aggregateCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_aggregate") {
			aggregateCalled = true
		}
		fmt.Fprint(w, `<resultlist><result repository="UBI_9" arch="x86_64" state="published">
			<status package="p" code="succeeded" versrel="1.0-1"/></result></resultlist>`)
	}))
	defer ts.Close()

	c := obs.SingleFleet(obs.NewClient(ts.URL, "u", "p"), "isv:percona")
	pkg := &model.Package{Project: "ppg:staging:18", Name: "p", IsContainer: boolPtr(false), UpdatedAt: time.Now().UTC()}
	if err := (obs.VersionTask{}).Run(context.Background(), c, pkg, nil); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "1.0-1" || aggregateCalled {
		t.Errorf("version %q, aggregate lookup called = %v", pkg.Version, aggregateCalled)
	}
}
