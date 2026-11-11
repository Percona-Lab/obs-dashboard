package release

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

const (
	testBranch        = "release/20261111T143210"
	testDir           = "root/ppg/releases/18"
	testYAML          = testDir + "/release.yaml"
	testChangelogPath = testDir + "/CHANGELOG.md"
	testUnit          = "ppg:staging:18"
	testHead          = "head1"
	testPR            = 415

	unitsJSON = `[
 {"unit":"ppg:staging:18","members":["ppg:staging:18"],"release_project":"ppg:releases:18","release_dir":"root/ppg/releases/18"},
 {"unit":"ppg:staging:17:extras","members":["ppg:staging:17:extras"],"release_project":"ppg:releases:17:extras","release_dir":"root/ppg/releases/17/extras"}
]`
	mainYAML = "project: ppg:staging:18\nreleases:\n  - ppg/18.6-4\n  - tag: ppg/18.6-5\n    source_commit: aaa\n"
	cutYAML  = mainYAML + "  - tag: ppg/18.6-6\n    source_commit: bbb\n"

	section6     = "## [18.6-6] - 2026-11-11\n\n- Update PostgreSQL to 18.6\n\n"
	section5     = "## [18.6-5] - 2026-10-01\n\n- Older entry\n"
	cutChangelog = "# Changelog\n\n" + section6 + section5
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type svcFixture struct {
	svc   *Service
	gh    *fakeGitHub
	clock *testClock
	db    *sql.DB
}

// newFixture returns a service over a fake GitHub that holds only main: the
// units file and unit 18's release.yaml with releases up to 18.6-5.
func newFixture(t *testing.T) *svcFixture {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := &testClock{t: time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)}
	gh := newFakeGitHub()
	gh.now = clock.now
	gh.setFile("main", UnitsPath, unitsJSON)
	gh.setFile("main", testYAML, mainYAML)
	svc := NewService(Options{GitHub: gh, DB: db, Packages: fakeSource{}, Now: clock.now})
	return &svcFixture{svc: svc, gh: gh, clock: clock, db: db}
}

// withBranch adds the release branch holding the cut of 18.6-6.
func (fx *svcFixture) withBranch() {
	fx.gh.branches = append(fx.gh.branches, Ref{Name: testBranch, SHA: testHead})
	fx.gh.setFile(testBranch, testYAML, cutYAML)
	fx.gh.setFile(testBranch, testChangelogPath, cutChangelog)
}

// withCutRun adds the cut run for the release branch.
func (fx *svcFixture) withCutRun(status, conclusion string) {
	fx.gh.runs = append(fx.gh.runs, Run{
		ID: 1, Name: CutRunName(testBranch), WorkflowFile: WorkflowCut, Status: status, Conclusion: conclusion,
		HTMLURL: "https://github.com/run/1", CreatedAt: fx.clock.now().Add(-time.Hour),
	})
}

// withPR adds the open release PR of the release branch.
func (fx *svcFixture) withPR(labels ...string) {
	fx.gh.open = append(fx.gh.open, Pull{
		Number: testPR, HTMLURL: "https://github.com/pull/415", HeadRef: testBranch, HeadSHA: testHead,
		HeadRepo: fakeRepo, BaseRepo: fakeRepo, Labels: labels, State: "open",
	})
	fx.gh.pullFiles[testPR] = []string{testChangelogPath, testYAML}
}

// withPRRun adds a PR-workflow run (id 10) for the head with the given jobs.
func (fx *svcFixture) withPRRun(status, conclusion string, jobs ...Job) {
	fx.gh.runs = append(fx.gh.runs, Run{
		ID: 10, Name: "Release PR", WorkflowFile: WorkflowPR, HeadSHA: testHead, Status: status, Conclusion: conclusion,
		CreatedAt: fx.clock.now().Add(-30 * time.Minute),
	})
	fx.gh.jobs[10] = jobs
}

func populateJob(id int64, instance, status, conclusion string) Job {
	return Job{ID: id, Name: JobPopulatePrefix + instance + ")", Status: status, Conclusion: conclusion,
		HTMLURL: fmt.Sprintf("https://github.com/job/%d", id)}
}

func (fx *svcFixture) snapshot(t *testing.T) Snapshot {
	t.Helper()
	snap, err := fx.svc.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return snap
}

func (fx *svcFixture) oneRelease(t *testing.T) Release {
	t.Helper()
	snap := fx.snapshot(t)
	if len(snap.Releases) != 1 {
		t.Fatalf("got %d releases, want 1: %+v", len(snap.Releases), snap.Releases)
	}
	return snap.Releases[0]
}

func TestSnapshotStepsEndToEnd(t *testing.T) {
	cases := []struct {
		name  string
		setup func(fx *svcFixture)
		want  Step
	}{
		{"cutting", func(fx *svcFixture) {
			fx.withBranch()
			fx.withCutRun("in_progress", "")
		}, StepCutting},
		{"changelog", func(fx *svcFixture) {
			fx.withBranch()
			fx.withCutRun("completed", "success")
			fx.withPR()
		}, StepChangelog},
		{"sync", func(fx *svcFixture) {
			fx.withBranch()
			fx.withCutRun("completed", "success")
			fx.withPR(LabelSync)
			fx.withPRRun("in_progress", "", populateJob(100, "obs", "in_progress", ""))
		}, StepSync},
		{"qa", func(fx *svcFixture) {
			fx.withBranch()
			fx.withCutRun("completed", "success")
			fx.withPR(LabelSync)
			fx.withPRRun("completed", "success", populateJob(100, "obs", "completed", "success"))
		}, StepQA},
		{"releasing", func(fx *svcFixture) {
			merged := fx.clock.now().Add(-time.Hour)
			fx.gh.merged = append(fx.gh.merged, Pull{
				Number: testPR, HeadRef: testBranch, HeadSHA: testHead, HeadRepo: fakeRepo, BaseRepo: fakeRepo,
				State: "closed", MergedAt: &merged, MergeSHA: "merge1",
			})
			fx.gh.pullFiles[testPR] = []string{testChangelogPath, testYAML}
			fx.gh.setFile("merge1", testYAML, cutYAML)
			fx.gh.setFile("merge1", testChangelogPath, cutChangelog)
		}, StepReleasing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t)
			tc.setup(fx)
			rel := fx.oneRelease(t)
			if rel.Step != tc.want {
				t.Errorf("Step = %q, want %q", rel.Step, tc.want)
			}
			if rel.ID != "20261111T143210" || rel.Branch != testBranch || !rel.Dashboard {
				t.Errorf("ID=%q Branch=%q Dashboard=%v", rel.ID, rel.Branch, rel.Dashboard)
			}
			if len(rel.Units) != 1 {
				t.Fatalf("Units = %+v, want 1", rel.Units)
			}
			u := rel.Units[0]
			want := UnitState{Unit: testUnit, ReleaseProject: "ppg:releases:18", ReleaseDir: testDir,
				ReleaseID: "18.6-6", Tag: "ppg/18.6-6", Changelog: section6}
			if !reflect.DeepEqual(u, want) {
				t.Errorf("unit = %+v\nwant   %+v", u, want)
			}
			if rel.HeadSHA != testHead {
				t.Errorf("HeadSHA = %q", rel.HeadSHA)
			}
		})
	}
}

func TestSnapshotDetailPerStep(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.withPR(LabelSync, LabelQA)
	fx.gh.artifacts["1:"+ArtifactChangelogs] = []byte(`{"ppg:staging:18":"## [18.6-6] generated\n"}`)
	fx.withPRRun("in_progress", "",
		populateJob(100, "obs", "completed", "success"),
		Job{ID: 200, Name: JobLanePrefix + "packages / pg_tde", Status: "in_progress", HTMLURL: "https://github.com/job/200"},
	)
	fx.gh.annotations[200] = []Annotation{{Level: "notice", Title: AnnotationJenkins, Message: "ppg:releases:18 https://jenkins/job/9"}}

	rel := fx.oneRelease(t)
	if rel.Step != StepQA || rel.PR != testPR || rel.PRURL == "" {
		t.Fatalf("release = %+v", rel)
	}
	if rel.Cut == nil || rel.Cut.State != StateSuccess || rel.Cut.URL == "" {
		t.Errorf("Cut = %+v", rel.Cut)
	}
	if g := rel.Units[0].Generated; g == nil || *g != "## [18.6-6] generated\n" {
		t.Errorf("Generated = %v", g)
	}
	if rel.SyncState != string(StateSuccess) || len(rel.Sync) != 1 || rel.Sync[0].Instance != "obs" {
		t.Errorf("SyncState=%q Sync=%+v", rel.SyncState, rel.Sync)
	}
	if rel.QA == nil || rel.QA.State != "running" || len(rel.QA.Lanes) != 1 {
		t.Fatalf("QA = %+v", rel.QA)
	}
	if l := rel.QA.Lanes[0]; l.Unit != testUnit || l.JenkinsURL != "https://jenkins/job/9" {
		t.Errorf("lane = %+v", l)
	}
	if rel.Removals == nil || rel.Actions == nil {
		t.Errorf("Removals and Actions must be empty lists, not nil")
	}
}

func TestSnapshotOpensPRAfterCut(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.gh.artifacts["1:"+ArtifactChangelogs] = []byte(`{"ppg:staging:18":"## [18.6-6] generated\n\n- From the cut\n"}`)
	if err := store.AddReleaseAudit(fx.db, store.ReleaseAudit{At: fx.clock.now(), Email: "someone@percona.com", Action: "cut", Branch: testBranch}); err != nil {
		t.Fatal(err)
	}

	rel := fx.oneRelease(t)
	created := fx.gh.callsOf("CreatePull")
	if len(created) != 1 {
		t.Fatalf("CreatePull calls = %v, want 1", created)
	}
	if strings.Contains(created[0], "@") || strings.Contains(created[0], "someone") {
		t.Errorf("CreatePull carries a user identity: %q", created[0])
	}
	if !strings.HasPrefix(created[0], "CreatePull "+testBranch+" Release 18.6-6 ") || !strings.Contains(created[0], "- From the cut") {
		t.Errorf("CreatePull = %q", created[0])
	}
	if rel.PR == 0 || rel.Step != StepChangelog {
		t.Errorf("after opening: PR=%d Step=%q, want a PR at step changelog", rel.PR, rel.Step)
	}

	fx.svc.Invalidate()
	rel = fx.oneRelease(t)
	if n := len(fx.gh.callsOf("CreatePull")); n != 1 {
		t.Errorf("CreatePull calls after second refresh = %d, want 1", n)
	}
	if rel.PR == 0 {
		t.Errorf("second refresh lost the PR")
	}
}

func TestSnapshotPRBodyFromBranchWithoutArtifact(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.oneRelease(t)
	created := fx.gh.callsOf("CreatePull")
	if len(created) != 1 || !strings.Contains(created[0], "- Update PostgreSQL to 18.6") || strings.Contains(created[0], "Older entry") {
		t.Fatalf("CreatePull = %v", created)
	}
}

func TestSnapshotNoPRWhileCutRunning(t *testing.T) {
	for _, tc := range []struct{ status, conclusion string }{
		{"in_progress", ""}, {"queued", ""}, {"completed", "failure"},
	} {
		fx := newFixture(t)
		fx.withBranch()
		fx.withCutRun(tc.status, tc.conclusion)
		rel := fx.oneRelease(t)
		if n := len(fx.gh.callsOf("CreatePull")); n != 0 {
			t.Errorf("%s/%s: CreatePull called %d times", tc.status, tc.conclusion, n)
		}
		if rel.PR != 0 || rel.Step != StepCutting || rel.Cut == nil {
			t.Errorf("%s/%s: release = %+v", tc.status, tc.conclusion, rel)
		}
	}
}

func TestSnapshotNoPRForBranchIdenticalToMain(t *testing.T) {
	fx := newFixture(t)
	fx.gh.branches = []Ref{{Name: testBranch, SHA: "mainsha"}}
	fx.gh.setFile(testBranch, testYAML, mainYAML)
	fx.withCutRun("completed", "success")
	rel := fx.oneRelease(t)
	if len(rel.Units) != 0 || len(fx.gh.callsOf("CreatePull")) != 0 {
		t.Fatalf("units=%+v CreatePull=%v", rel.Units, fx.gh.callsOf("CreatePull"))
	}
}

func TestSnapshotCutRunningBeforeBranchExists(t *testing.T) {
	fx := newFixture(t)
	fx.withCutRun("queued", "")
	rel := fx.oneRelease(t)
	if rel.ID != "20261111T143210" || rel.Step != StepCutting || len(rel.Units) != 0 || rel.HeadSHA != "" {
		t.Fatalf("release = %+v", rel)
	}

	// A finished cut that left no branch and is older than a day is not a release.
	fx = newFixture(t)
	fx.withCutRun("completed", "failure")
	fx.gh.runs[0].CreatedAt = fx.clock.now().Add(-25 * time.Hour)
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("releases = %+v, want none", snap.Releases)
	}
}

func TestSnapshotHandMadePR(t *testing.T) {
	fx := newFixture(t)
	fx.gh.open = []Pull{
		{Number: 47, HeadRef: "release-pg-18", HeadSHA: "h47", HeadRepo: fakeRepo, BaseRepo: fakeRepo, State: "open"},
		{Number: 48, HeadRef: "fix-typo", HeadSHA: "h48", HeadRepo: fakeRepo, BaseRepo: fakeRepo, State: "open"},
	}
	fx.gh.pullFiles[47] = []string{testYAML, testChangelogPath}
	fx.gh.pullFiles[48] = []string{"README.md", "root/ppg/releases/18/notes/release.yaml.txt"}
	fx.gh.setFile("release-pg-18", testYAML, cutYAML)
	fx.gh.setFile("release-pg-18", testChangelogPath, cutChangelog)

	rel := fx.oneRelease(t)
	if rel.ID != "pr-47" || !rel.Dashboard || rel.StartedBy != "" || rel.StartedAt != nil {
		t.Fatalf("release = %+v", rel)
	}
	if rel.Step != StepChangelog || rel.Cut != nil || len(rel.Units) != 1 || rel.Units[0].ReleaseID != "18.6-6" {
		t.Errorf("release = %+v", rel)
	}
}

func TestSnapshotNestedUnitPath(t *testing.T) {
	fx := newFixture(t)
	const dir = "root/ppg/releases/17/extras"
	fx.gh.open = []Pull{{Number: 47, HeadRef: "extras", HeadSHA: "h47", HeadRepo: fakeRepo, BaseRepo: fakeRepo}}
	fx.gh.pullFiles[47] = []string{dir + "/release.yaml"}
	fx.gh.setFile("extras", dir+"/release.yaml", "project: ppg:staging:17:extras\nreleases:\n  - ppg/extras-17.10-2\n")
	rel := fx.oneRelease(t)
	want := UnitState{Unit: "ppg:staging:17:extras", ReleaseProject: "ppg:releases:17:extras", ReleaseDir: dir,
		ReleaseID: "extras-17.10-2", Tag: "ppg/extras-17.10-2"}
	if len(rel.Units) != 1 || !reflect.DeepEqual(rel.Units[0], want) {
		t.Fatalf("units = %+v", rel.Units)
	}
}

func TestSnapshotForkPR(t *testing.T) {
	fx := newFixture(t)
	// The fork's branch is called like a dashboard branch and like a branch of
	// the repository itself; neither may be trusted.
	fx.withBranch()
	fx.gh.open = []Pull{{Number: 47, HeadRef: testBranch, HeadSHA: "forkhead", HeadRepo: "someone/obs-packaging", BaseRepo: fakeRepo}}
	fx.gh.pullFiles[47] = []string{testYAML}
	fx.gh.setFile("forkhead", testYAML, strings.Replace(cutYAML, "18.6-6", "18.6-7", 1))

	snap := fx.snapshot(t)
	var fork *Release
	for i := range snap.Releases {
		if snap.Releases[i].PR == 47 {
			fork = &snap.Releases[i]
		}
	}
	if fork == nil {
		t.Fatalf("fork PR not listed: %+v", snap.Releases)
	}
	if fork.Dashboard || fork.ID != "pr-47" {
		t.Errorf("fork = %+v", fork)
	}
	if len(fork.Units) != 1 || fork.Units[0].ReleaseID != "18.6-7" {
		t.Errorf("fork units = %+v, want the fork head's content", fork.Units)
	}
	if len(snap.Releases) != 2 {
		t.Errorf("the repository's own branch must stay a separate release: %+v", snap.Releases)
	}
}

func TestSnapshotMergedLeavesWhenReleaseRunSucceeded(t *testing.T) {
	fx := newFixture(t)
	merged := fx.clock.now().Add(-time.Hour)
	fx.gh.merged = []Pull{{
		Number: testPR, HeadRef: testBranch, HeadSHA: testHead, HeadRepo: fakeRepo, BaseRepo: fakeRepo,
		MergedAt: &merged, MergeSHA: "merge1",
	}}
	fx.gh.pullFiles[testPR] = []string{testYAML}
	fx.gh.setFile("merge1", testYAML, cutYAML)
	// The branch outlives the merge for a moment; it must not become a new release.
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.gh.runs = append(fx.gh.runs, Run{ID: 20, Name: ReleaseRunName(testPR), WorkflowFile: WorkflowRelease,
		Status: "in_progress", HTMLURL: "https://github.com/run/20", CreatedAt: fx.clock.now()})
	fx.gh.jobs[20] = []Job{{ID: 300, Name: JobCopyPrefix + "obs)", Status: "in_progress"}}

	rel := fx.oneRelease(t)
	if rel.Step != StepReleasing || rel.Final == nil || rel.Final.State != StateRunning {
		t.Fatalf("release = %+v final = %+v", rel, rel.Final)
	}
	if !reflect.DeepEqual(rel.Final.Tags, []string{"ppg/18.6-6"}) || len(rel.Final.Instances) != 1 {
		t.Errorf("final = %+v", rel.Final)
	}

	fx.gh.mu.Lock()
	fx.gh.runs[len(fx.gh.runs)-1].Status = "completed"
	fx.gh.runs[len(fx.gh.runs)-1].Conclusion = "success"
	fx.gh.mu.Unlock()
	fx.svc.Invalidate()
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("releases = %+v, want none", snap.Releases)
	}
	if n := len(fx.gh.callsOf("CreatePull")); n != 0 {
		t.Errorf("CreatePull called %d times for a merged release", n)
	}
	if n := len(fx.gh.callsOf("PullFiles")); n != 1 {
		t.Errorf("PullFiles called %d times, want 1 (cached)", n)
	}
}

func TestSnapshotMergedOutsideWindowNotListed(t *testing.T) {
	fx := newFixture(t)
	old := fx.clock.now().Add(-8 * 24 * time.Hour)
	fx.gh.merged = []Pull{{Number: testPR, HeadRef: "x", HeadRepo: fakeRepo, BaseRepo: fakeRepo, MergedAt: &old, MergeSHA: "m"}}
	fx.gh.pullFiles[testPR] = []string{testYAML}
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("releases = %+v", snap.Releases)
	}
}

func TestSnapshotCached(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withPR()
	fx.snapshot(t)
	fx.clock.advance(14 * time.Second)
	fx.snapshot(t)
	if n := len(fx.gh.callsOf("OpenPulls")); n != 1 {
		t.Fatalf("OpenPulls calls within TTL = %d, want 1", n)
	}
	fx.svc.Invalidate()
	fx.snapshot(t)
	if n := len(fx.gh.callsOf("OpenPulls")); n != 2 {
		t.Fatalf("OpenPulls calls after Invalidate = %d, want 2", n)
	}
	fx.clock.advance(15 * time.Second)
	fx.snapshot(t)
	if n := len(fx.gh.callsOf("OpenPulls")); n != 3 {
		t.Fatalf("OpenPulls calls after TTL = %d, want 3", n)
	}
}

func TestSnapshotConcurrentCallersShareRefresh(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withPR()
	fx.gh.block = make(chan struct{})
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap, err := fx.svc.Snapshot(context.Background())
			if err == nil && len(snap.Releases) != 1 {
				err = fmt.Errorf("releases = %d", len(snap.Releases))
			}
			errs <- err
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(fx.gh.block)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if c := len(fx.gh.callsOf("Branches")); c != 1 {
		t.Fatalf("Branches calls = %d, want 1", c)
	}
}

func TestSnapshotCallerCancelDoesNotBreakRefresh(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withPR()
	fx.gh.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := fx.svc.Snapshot(ctx)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	close(fx.gh.block)
	if rel := fx.oneRelease(t); rel.PR != testPR {
		t.Fatalf("release = %+v", rel)
	}
}

func TestSnapshotStaleOnGitHubError(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withPR()
	first := fx.snapshot(t)
	if first.Stale || first.Error != "" || !first.RefreshedAt.Equal(fx.clock.now()) {
		t.Fatalf("first = %+v", first)
	}

	fx.gh.setErr(&APIError{Method: "GET", Path: "/repos/x/pulls", Status: 403})
	fx.clock.advance(20 * time.Second)
	snap, err := fx.svc.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want the stale snapshot", err)
	}
	if !snap.Stale || snap.Error == "" || len(snap.Releases) != 1 {
		t.Fatalf("snap = %+v", snap)
	}
	if !snap.RefreshedAt.Equal(first.RefreshedAt) {
		t.Errorf("RefreshedAt = %v, want the time of the last good refresh %v", snap.RefreshedAt, first.RefreshedAt)
	}

	fx.gh.setErr(nil)
	fx.clock.advance(20 * time.Second)
	snap = fx.snapshot(t)
	if snap.Stale || snap.Error != "" {
		t.Fatalf("after recovery: %+v", snap)
	}
}

func TestSnapshotErrorWithoutPrevious(t *testing.T) {
	fx := newFixture(t)
	fx.gh.setErr(&APIError{Method: "GET", Path: "/repos/x/pulls", Status: 403})
	if _, err := fx.svc.Snapshot(context.Background()); err == nil {
		t.Fatal("err = nil, want the GitHub error")
	}
}

func TestSnapshotStartedByFromAudit(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withPR()
	at := time.Date(2026, 11, 11, 14, 32, 10, 0, time.UTC)
	for _, a := range []store.ReleaseAudit{
		{At: at, Email: "starter@percona.com", Action: "cut", Branch: testBranch},
		{At: at.Add(time.Minute), Email: "editor@percona.com", Action: "save_changelog", Branch: testBranch, PR: testPR},
		{At: at, Email: "other@percona.com", Action: "cut", Branch: "release/other"},
	} {
		if err := store.AddReleaseAudit(fx.db, a); err != nil {
			t.Fatal(err)
		}
	}
	rel := fx.oneRelease(t)
	if rel.StartedBy != "starter@percona.com" || rel.StartedAt == nil || !rel.StartedAt.Equal(at) {
		t.Fatalf("StartedBy=%q StartedAt=%v", rel.StartedBy, rel.StartedAt)
	}
	want := []AuditEntry{
		{At: at, Email: "starter@percona.com", Action: "cut"},
		{At: at.Add(time.Minute), Email: "editor@percona.com", Action: "save_changelog"},
	}
	if !reflect.DeepEqual(rel.Actions, want) {
		t.Errorf("Actions = %+v", rel.Actions)
	}
}

func TestSnapshotRemovalsAndFailedUnits(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withPR(LabelSync)
	fx.withPRRun("completed", "failure",
		populateJob(100, "obs", "completed", "failure"),
		populateJob(101, "labs", "completed", "success"),
		populateJob(102, "noreport", "completed", "success"),
	)
	fx.gh.artifacts["10:"+ArtifactReportPrefix+"obs"] = []byte(
		`[{"project":"ppg:releases:18","outcome":"failure","removals":["package python3-six"]},
		  {"project":"ppg:releases:18:containers","outcome":"success","removals":[]}]`)
	fx.gh.artifacts["10:"+ArtifactReportPrefix+"labs"] = []byte(
		`[{"project":"ppg:releases:18","outcome":"success","removals":["package python3-six","project ppg:releases:18:old"]}]`)
	fx.gh.annotations[100] = []Annotation{
		{Level: "warning", Message: "deprecated"},
		{Level: "failure", Message: "staging not green"},
	}
	fx.gh.annotations[101] = []Annotation{{Level: "failure", Message: "must not be read"}}

	rel := fx.oneRelease(t)
	if rel.Step != StepSync || rel.SyncState != string(StateFailure) || len(rel.Sync) != 3 {
		t.Fatalf("Step=%q SyncState=%q Sync=%+v", rel.Step, rel.SyncState, rel.Sync)
	}
	wantRemovals := []Removal{
		{Project: "ppg:releases:18", Item: "package python3-six"},
		{Project: "ppg:releases:18", Item: "project ppg:releases:18:old"},
	}
	if !reflect.DeepEqual(rel.Removals, wantRemovals) {
		t.Errorf("Removals = %+v", rel.Removals)
	}
	obs, labs, none := rel.Sync[0], rel.Sync[1], rel.Sync[2]
	if !reflect.DeepEqual(obs.FailedUnits, []string{"ppg:releases:18"}) || !reflect.DeepEqual(obs.Errors, []string{"staging not green"}) {
		t.Errorf("obs card = %+v", obs)
	}
	if len(labs.FailedUnits) != 0 || len(labs.Errors) != 0 {
		t.Errorf("labs card = %+v", labs)
	}
	if none.FailedUnits == nil || len(none.FailedUnits) != 0 || none.Errors == nil || len(none.Errors) != 0 {
		t.Errorf("card without report = %+v", none)
	}

	// Reports of finished jobs do not change: the next refresh reuses them.
	before := len(fx.gh.callsOf("Artifact 10 " + ArtifactReportPrefix + "obs"))
	fx.svc.Invalidate()
	fx.oneRelease(t)
	if after := len(fx.gh.callsOf("Artifact 10 " + ArtifactReportPrefix + "obs")); after != before {
		t.Errorf("report artifact fetched again: %d -> %d", before, after)
	}
}

func TestSnapshotDegradesOnUnreadableFiles(t *testing.T) {
	fx := newFixture(t)
	fx.gh.open = []Pull{{Number: 47, HeadRef: "gone", HeadSHA: "h47", HeadRepo: fakeRepo, BaseRepo: fakeRepo}}
	fx.gh.pullFiles[47] = []string{testYAML}
	rel := fx.oneRelease(t)
	want := UnitState{ReleaseProject: "ppg:releases:18", ReleaseDir: testDir}
	if rel.ID != "pr-47" || len(rel.Units) != 1 || !reflect.DeepEqual(rel.Units[0], want) {
		t.Fatalf("release = %+v", rel)
	}
}

func TestUnitsLastReleaseAndInFlight(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withPR()
	rows, err := fx.svc.Units(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Unit != testUnit || rows[0].LastRelease != "18.6-5" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if rows[0].Blocked == nil || rows[0].Blocked.Reason != "in_flight" || rows[0].Blocked.ReleaseID != "20261111T143210" {
		t.Errorf("row 0 blocked = %+v", rows[0].Blocked)
	}
	// Unit 17:extras has no release.yaml on main yet.
	if rows[1].LastRelease != "" || (rows[1].Blocked != nil && rows[1].Blocked.Reason == "in_flight") {
		t.Errorf("row 1 = %+v blocked = %+v", rows[1], rows[1].Blocked)
	}
}

func TestUnitsUsesPackages(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gh := newFakeGitHub()
	gh.setFile("main", UnitsPath, unitsJSON)
	src := fakeSource{"ppg:staging:18": {pkg("ppg:staging:18", "pg", "18.2", model.RollupPublished)}}
	svc := NewService(Options{GitHub: gh, DB: db, Packages: src})
	rows, err := svc.Units(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !rows[0].Tickable || rows[0].Total != 1 || rows[1].Tickable {
		t.Fatalf("rows = %+v", rows)
	}
}

func historyFixture(t *testing.T) *svcFixture {
	fx := newFixture(t)
	fx.gh.setFile("main", testYAML, cutYAML)
	fx.gh.releases = []GHRelease{
		{Tag: "ppg/18.6-5", Body: "notes 5", HTMLURL: "https://github.com/rel/5", PublishedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)},
		{Tag: "nightly", Body: "not a release tag", PublishedAt: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)},
		{Tag: "ppg/18.6-6", Body: "notes 6", HTMLURL: "https://github.com/rel/6", PublishedAt: time.Date(2026, 11, 11, 16, 0, 0, 0, time.UTC)},
	}
	fx.gh.tagSHAs["ppg/18.6-6"] = "sha6"
	fx.gh.tagSHAs["ppg/18.6-5"] = "sha5"
	fx.gh.commitPulls["sha6"] = Pull{Number: testPR, HTMLURL: "https://github.com/pull/415"}
	fx.gh.commitPulls["sha5"] = Pull{Number: 300, HTMLURL: "https://github.com/pull/300"}
	return fx
}

func TestHistoryRows(t *testing.T) {
	fx := historyFixture(t)
	if err := store.AddReleaseAudit(fx.db, store.ReleaseAudit{
		At: fx.clock.now(), Email: "releaser@percona.com", Action: "release", Branch: testBranch, PR: testPR,
		Detail: "Release / OBS QA / packages / pg_tde\nRelease / OBS QA / tarballs / ol9",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := fx.svc.History(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []HistoryRow{
		{Tag: "ppg/18.6-6", ReleaseID: "18.6-6", Unit: testUnit, ReleasedAt: time.Date(2026, 11, 11, 16, 0, 0, 0, time.UTC),
			ReleaseURL: "https://github.com/rel/6", PRURL: "https://github.com/pull/415", Notes: "notes 6",
			ReleasedBy:  "releaser@percona.com",
			FailedLanes: []string{"Release / OBS QA / packages / pg_tde", "Release / OBS QA / tarballs / ol9"}},
		{Tag: "ppg/18.6-5", ReleaseID: "18.6-5", Unit: testUnit, ReleasedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
			ReleaseURL: "https://github.com/rel/5", PRURL: "https://github.com/pull/300", Notes: "notes 5",
			FailedLanes: []string{}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %+v\nwant   %+v", rows, want)
	}
	if c := fx.gh.callsOf("Releases"); len(c) != 1 || c[0] != "Releases 1 20" {
		t.Errorf("Releases calls = %v", c)
	}

	// Tag lookups are immutable and cached.
	if _, err := fx.svc.History(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if n := len(fx.gh.callsOf("TagSHA")); n != 2 {
		t.Errorf("TagSHA calls = %d, want 2", n)
	}
	if n := len(fx.gh.callsOf("PullForCommit")); n != 2 {
		t.Errorf("PullForCommit calls = %d, want 2", n)
	}
}

func TestHistoryTagWithoutPR(t *testing.T) {
	fx := historyFixture(t)
	delete(fx.gh.commitPulls, "sha6") // PullForCommit returns 0
	delete(fx.gh.tagSHAs, "ppg/18.6-5")
	rows, err := fx.svc.History(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].PRURL != "" || rows[1].PRURL != "" || rows[0].ReleasedBy != "" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestHistoryGitHubError(t *testing.T) {
	fx := historyFixture(t)
	fx.gh.setErr(&APIError{Method: "GET", Path: "/repos/x/releases", Status: 500})
	if _, err := fx.svc.History(context.Background(), 1); err == nil {
		t.Fatal("err = nil")
	}
}

func TestUnitsInFlightByReleaseProject(t *testing.T) {
	fx := newFixture(t)
	fx.gh.open = []Pull{{Number: testPR, HeadRef: testBranch, HeadSHA: testHead, HeadRepo: fakeRepo, BaseRepo: fakeRepo, State: "open"}}
	fx.gh.pullFiles[testPR] = []string{testYAML} // release.yaml unreadable: the unit has only its release project
	rel := fx.oneRelease(t)
	if len(rel.Units) != 1 || rel.Units[0].Unit != "" || rel.Units[0].ReleaseProject != "ppg:releases:18" {
		t.Fatalf("units = %+v", rel.Units)
	}
	rows, err := fx.svc.Units(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Tickable || rows[0].Blocked == nil || rows[0].Blocked.Reason != "in_flight" {
		t.Errorf("row 0 = %+v blocked = %+v", rows[0], rows[0].Blocked)
	}
	if rows[1].Blocked != nil && rows[1].Blocked.Reason == "in_flight" {
		t.Errorf("row 1 blocked = %+v", rows[1].Blocked)
	}
}

func TestUnitsFailClosedWhileCutRunning(t *testing.T) {
	for _, status := range []string{"queued", "in_progress"} {
		fx := newFixture(t)
		fx.withCutRun(status, "")
		rows, err := fx.svc.Units(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %+v", rows)
		}
		for _, r := range rows {
			if r.Tickable || r.Blocked == nil || r.Blocked.Reason != "in_flight" ||
				r.Blocked.Text != "A release is being cut. Try again in a minute." {
				t.Errorf("%s: row %s = %+v blocked = %+v", status, r.Unit, r, r.Blocked)
			}
		}
	}
}

func TestUnitsNotBlockedByFailedCut(t *testing.T) {
	fx := newFixture(t)
	fx.withCutRun("completed", "failure")
	src := fakeSource{"ppg:staging:18": {pkg("ppg:staging:18", "pg", "18.2", model.RollupPublished)}}
	svc := NewService(Options{GitHub: fx.gh, DB: fx.db, Packages: src, Now: fx.clock.now})
	rel, err := svc.Snapshot(context.Background())
	if err != nil || len(rel.Releases) != 1 || rel.Releases[0].Cut == nil || rel.Releases[0].Cut.State != StateFailure {
		t.Fatalf("snapshot = %+v err = %v", rel, err)
	}
	rows, err := svc.Units(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !rows[0].Tickable || rows[0].Blocked != nil {
		t.Errorf("row 0 = %+v blocked = %+v", rows[0], rows[0].Blocked)
	}
}

func TestSnapshotNoPRWhenClosedPullExists(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.gh.closedHeads[testBranch] = true
	snap := fx.snapshot(t)
	if len(snap.Releases) != 0 {
		t.Fatalf("releases = %+v, want none", snap.Releases)
	}
	if n := len(fx.gh.callsOf("CreatePull")); n != 0 {
		t.Fatalf("CreatePull called %d times", n)
	}
}

func TestSnapshotHasClosedPullErrorFailsRefresh(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.gh.hasClosedErr = errors.New("boom")
	if _, err := fx.svc.Snapshot(context.Background()); err == nil {
		t.Fatal("Snapshot succeeded, want an error")
	}
	if n := len(fx.gh.callsOf("CreatePull")); n != 0 {
		t.Fatalf("CreatePull called %d times", n)
	}
}

func TestSnapshotNoPRForDegradedUnits(t *testing.T) {
	// release.yaml of the branch is unreadable: the unit has no release id.
	fx := newFixture(t)
	fx.gh.branches = []Ref{{Name: testBranch, SHA: testHead}}
	fx.gh.setFile(testBranch, testYAML, "not: [valid")
	fx.withCutRun("completed", "success")
	rel := fx.oneRelease(t)
	if n := len(fx.gh.callsOf("CreatePull")); n != 0 || rel.PR != 0 {
		t.Fatalf("CreatePull calls = %d, PR = %d, units = %+v", n, rel.PR, rel.Units)
	}

	// A readable release.yaml with no changelog section and no generated text.
	fx = newFixture(t)
	fx.withBranch()
	fx.gh.setFile(testBranch, testChangelogPath, "# Changelog\n")
	fx.withCutRun("completed", "success")
	fx.oneRelease(t)
	if n := len(fx.gh.callsOf("CreatePull")); n != 0 {
		t.Fatalf("empty changelog: CreatePull called %d times", n)
	}
}

func TestSnapshotFailedCutListedFor24h(t *testing.T) {
	fx := newFixture(t)
	fx.withCutRun("completed", "failure")
	fx.gh.runs[0].CreatedAt = fx.clock.now().Add(-23 * time.Hour)
	rel := fx.oneRelease(t)
	if rel.ID != "20261111T143210" || rel.Step != StepCutting || len(rel.Units) != 0 || !rel.Dashboard ||
		rel.Cut == nil || rel.Cut.State != StateFailure || rel.Cut.URL != "https://github.com/run/1" {
		t.Fatalf("release = %+v", rel)
	}
	fx.clock.advance(2 * time.Hour)
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("after 25h: releases = %+v, want none", snap.Releases)
	}
}

func TestSnapshotUnusableFileDegrades(t *testing.T) {
	unusable := fmt.Errorf("github contents x: %w", ErrUnusable)

	// A changelog that is a directory: the unit keeps its identity, without text.
	fx := newFixture(t)
	fx.atChangelog()
	fx.gh.fileErrs[testBranch+":"+testChangelogPath] = unusable
	rel := fx.oneRelease(t)
	if len(rel.Units) != 1 || rel.Units[0].ReleaseID != "18.6-6" || rel.Units[0].Changelog != "" {
		t.Fatalf("units = %+v", rel.Units)
	}

	// A release.yaml that cannot be read: only the path is known.
	fx = newFixture(t)
	fx.atChangelog()
	fx.gh.fileErrs[testBranch+":"+testYAML] = unusable
	rel = fx.oneRelease(t)
	want := UnitState{ReleaseProject: "ppg:releases:18", ReleaseDir: testDir}
	if len(rel.Units) != 1 || !reflect.DeepEqual(rel.Units[0], want) {
		t.Fatalf("units = %+v", rel.Units)
	}

	// The same on main, for the selection list.
	fx = newFixture(t)
	fx.gh.fileErrs["main:"+testYAML] = unusable
	rows, err := fx.svc.Units(context.Background())
	if err != nil || len(rows) != 2 || rows[0].LastRelease != "" {
		t.Fatalf("rows = %+v, err = %v", rows, err)
	}
}

func TestSnapshotForkFileErrorNeverFailsRefresh(t *testing.T) {
	for _, bad := range []string{testYAML, testChangelogPath} {
		fx := newFixture(t)
		fx.atChangelog()
		fx.gh.open = append(fx.gh.open, Pull{Number: 47, HeadRef: "feature", HeadSHA: "forkhead", HeadRepo: "someone/obs-packaging", BaseRepo: fakeRepo})
		fx.gh.pullFiles[47] = []string{testYAML}
		fx.gh.setFile("forkhead", testYAML, strings.Replace(cutYAML, "18.6-6", "18.6-7", 1))
		fx.gh.setFile("forkhead", testChangelogPath, cutChangelog)
		fx.gh.fileErrs["forkhead:"+bad] = errors.New("github GET /contents: HTTP 500")

		snap := fx.snapshot(t)
		if snap.Stale || len(snap.Releases) != 2 {
			t.Fatalf("%s: snapshot = %+v", bad, snap)
		}
		for _, r := range snap.Releases {
			if r.PR == 47 && (r.Dashboard || len(r.Units) != 1 || r.Units[0].ReleaseProject != "ppg:releases:18") {
				t.Errorf("%s: fork = %+v", bad, r)
			}
		}
		// The release of the repository itself still takes actions.
		if err := fx.svc.ApproveChangelog(context.Background(), actor, relID, testHead); err != nil {
			t.Errorf("%s: approve: %v", bad, err)
		}
	}

	// The same error in a PR of the repository itself does fail the refresh.
	fx := newFixture(t)
	fx.atChangelog()
	fx.gh.fileErrs[testBranch+":"+testChangelogPath] = errors.New("github GET /contents: HTTP 500")
	if _, err := fx.svc.Snapshot(context.Background()); err == nil {
		t.Fatal("a failed read of our own PR must not be hidden")
	}
}

func TestSnapshotErrorTextHidesInternals(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	fx.snapshot(t)

	fx.gh.setErr(errors.New("release audit: no such table: secret_detail"))
	fx.svc.Invalidate()
	snap := fx.snapshot(t)
	if !snap.Stale || snap.Error != "internal error" {
		t.Fatalf("non-API error: %+v", snap)
	}

	fx.gh.setErr(fmt.Errorf("wrapped: %w", &APIError{Method: "GET", Path: "/repos/x/pulls", Status: 403}))
	fx.svc.Invalidate()
	snap = fx.snapshot(t)
	if snap.Error != "github GET /repos/x/pulls: HTTP 403" {
		t.Fatalf("API error: %q", snap.Error)
	}
}

// countCalls returns how many logged calls use one of the methods.
func countCalls(fx *svcFixture, methods ...string) int {
	n := 0
	for _, m := range methods {
		n += len(fx.gh.callsOf(m))
	}
	return n
}

func TestSnapshotSecondRefreshReusesCompletedRuns(t *testing.T) {
	fx := newFixture(t)
	fx.atQA(true, "completed", laneJob(200, "packages / pg_tde", "failure"), laneJob(201, "packages / pg_stat", "success"))
	fx.gh.artifacts["1:"+ArtifactChangelogs] = []byte(`{"ppg:staging:18":"## [18.6-6] generated\n"}`)
	fx.gh.artifacts["10:"+ArtifactReportPrefix+"obs"] = []byte(`[{"project":"ppg:releases:18","outcome":"success","removals":["package x"]}]`)
	fx.gh.annotations[200] = []Annotation{{Level: "notice", Title: AnnotationJenkins, Message: "ppg:releases:18 https://jenkins/job/9"}}

	first := fx.oneRelease(t)
	if first.QA == nil || first.QA.State != "finished" || len(first.QA.Lanes) != 2 || first.QA.Lanes[0].JenkinsURL == "" || len(first.Removals) != 1 {
		t.Fatalf("first = %+v qa = %+v", first, first.QA)
	}
	before := countCalls(fx, "Jobs", "Annotations", "Artifact")
	if before == 0 {
		t.Fatal("the first refresh read nothing")
	}
	fx.svc.Invalidate()
	second := fx.oneRelease(t)
	if after := countCalls(fx, "Jobs", "Annotations", "Artifact"); after != before {
		t.Errorf("second refresh made %d more Jobs/Annotations/Artifact calls: %v", after-before, fx.gh.allCalls())
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("second snapshot differs:\n%+v\n%+v", first, second)
	}
}

func TestSnapshotMissingArtifactNotAskedEveryRefresh(t *testing.T) {
	fx := newFixture(t)
	fx.atQA(false, "completed") // no generated-changelogs artifact, no populate report
	fx.oneRelease(t)
	before := countCalls(fx, "Artifact")
	fx.svc.Invalidate()
	fx.oneRelease(t)
	if after := countCalls(fx, "Artifact"); after != before {
		t.Errorf("missing artifacts asked again: %d -> %d", before, after)
	}
	// It is looked for again later: an artifact can turn up late.
	fx.clock.advance(missingArtifactTTL + time.Second)
	fx.oneRelease(t)
	if after := countCalls(fx, "Artifact"); after == before {
		t.Error("missing artifacts are never asked again")
	}
}

func TestSnapshotRunningRunIsReadAgain(t *testing.T) {
	fx := newFixture(t)
	fx.atQA(true, "in_progress", Job{ID: 200, Name: JobLanePrefix + "packages / pg_tde", Status: "in_progress"})
	fx.oneRelease(t)
	jobs, anns := countCalls(fx, "Jobs"), countCalls(fx, "Annotations")
	fx.svc.Invalidate()
	fx.oneRelease(t)
	if countCalls(fx, "Jobs") == jobs || countCalls(fx, "Annotations") == anns {
		t.Error("a run in progress was served from the cache")
	}
}

func TestSnapshotFilesByCommitAreKept(t *testing.T) {
	fx := newFixture(t)
	fx.atReleasing("failure")
	fx.oneRelease(t)
	byCommit := func() int {
		n := 0
		for _, c := range fx.gh.callsOf("File") {
			if strings.HasSuffix(c, " merge1") {
				n++
			}
		}
		return n
	}
	before := byCommit()
	if before == 0 {
		t.Fatal("nothing was read by commit")
	}
	fx.svc.Invalidate()
	fx.oneRelease(t)
	if after := byCommit(); after != before {
		t.Errorf("files of a commit read again: %d -> %d", before, after)
	}
}

func TestSnapshotClosedPullAnswerIsKept(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.gh.closedHeads[testBranch] = true
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("releases = %+v", snap.Releases)
	}
	fx.svc.Invalidate()
	fx.snapshot(t)
	if n := countCalls(fx, "HasClosedPull"); n != 1 {
		t.Errorf("HasClosedPull called %d times, want 1", n)
	}
}

func TestSnapshotCachesDropFinishedReleases(t *testing.T) {
	fx := newFixture(t)
	fx.atQA(true, "completed", laneJob(200, "packages / pg_tde", "success"))
	fx.gh.artifacts["1:"+ArtifactChangelogs] = []byte(`{"ppg:staging:18":"## [18.6-6] generated\n"}`)
	fx.oneRelease(t)
	if fx.svc.cacheSize() == 0 {
		t.Fatal("nothing was cached")
	}
	// The release is gone: abandoned and its runs aged out.
	fx.gh.mu.Lock()
	fx.gh.open, fx.gh.branches, fx.gh.runs = nil, nil, nil
	fx.gh.mu.Unlock()
	fx.svc.Invalidate()
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("releases = %+v", snap.Releases)
	}
	if n := fx.svc.cacheSize(); n != 0 {
		t.Errorf("%d cache entries left for a release that is gone", n)
	}
}
