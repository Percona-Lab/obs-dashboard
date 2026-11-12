package release

import (
	"context"
	"database/sql"
	"encoding/json"
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
	if !strings.HasPrefix(created[0], "CreatePull "+testBranch+" Release 18.6-6 ") || !strings.Contains(created[0], "- `"+testUnit+"`: `18.6-6`") ||
		strings.Contains(created[0], "From the cut") {
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

// The PR is opened without the generated-changelogs artifact too, and its
// description never carries changelog text.
func TestSnapshotOpensPRWithoutArtifact(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.oneRelease(t)
	created := fx.gh.callsOf("CreatePull")
	if len(created) != 1 || !strings.Contains(created[0], "Units in this release:") || !strings.Contains(created[0], "- `"+testUnit+"`: `18.6-6`") ||
		strings.Contains(created[0], "Update PostgreSQL") || strings.Contains(created[0], "Older entry") {
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
	wantObs := []CardUnit{{Project: "ppg:releases:18", Outcome: "failure"}, {Project: "ppg:releases:18:containers", Outcome: "success"}}
	if !reflect.DeepEqual(obs.Units, wantObs) || !reflect.DeepEqual(obs.Errors, []string{"staging not green"}) {
		t.Errorf("obs card = %+v", obs)
	}
	if !reflect.DeepEqual(labs.Units, []CardUnit{{Project: "ppg:releases:18", Outcome: "success"}}) || len(labs.Errors) != 0 {
		t.Errorf("labs card = %+v", labs)
	}
	if none.Units == nil || len(none.Units) != 0 || none.Errors == nil || len(none.Errors) != 0 {
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
	rows, _, err := fx.svc.History(context.Background(), 1)
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
	if _, _, err := fx.svc.History(context.Background(), 0); err != nil {
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
	rows, _, err := fx.svc.History(context.Background(), 1)
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
	if _, _, err := fx.svc.History(context.Background(), 1); err == nil {
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

func TestHistoryMoreFollowsTheGitHubPage(t *testing.T) {
	fx := historyFixture(t)
	// Page 1 holds 20 GitHub releases of which only two belong to a unit;
	// page 2 holds one more.
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 17; i++ {
		fx.gh.releases = append(fx.gh.releases, GHRelease{Tag: fmt.Sprintf("other-%d", i), PublishedAt: base.Add(time.Duration(i) * time.Hour)})
	}
	fx.gh.releases = append(fx.gh.releases, GHRelease{Tag: "ppg/18.6-4", Body: "notes 4", PublishedAt: base.Add(-time.Hour)})
	ctx := context.Background()

	rows, more, err := fx.svc.History(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || !more {
		t.Fatalf("page 1: %d rows, more = %v; want 2 rows and more", len(rows), more)
	}
	rows, more, err = fx.svc.History(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ReleaseID != "18.6-4" || more {
		t.Fatalf("page 2: rows = %+v, more = %v", rows, more)
	}
	if rows, more, err = fx.svc.History(ctx, 3); err != nil || len(rows) != 0 || rows == nil || more {
		t.Fatalf("page 3: rows = %#v, more = %v, err = %v", rows, more, err)
	}
}

// TestReleaseJSONFields pins the field names the frontend types rely on.
func TestReleaseJSONFields(t *testing.T) {
	fx := newFixture(t)
	fx.qaFailedToStart()
	b, err := json.Marshal(fx.oneRelease(t))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["sync_url"] != "" { // the fixture's sync run has no URL
		t.Errorf("sync_url = %#v", m["sync_url"])
	}
	qa, _ := m["qa"].(map[string]any)
	if qa["state"] != "failure" || qa["url"] != "https://github.com/run/11" || qa["error"] != "QA could not start." {
		t.Errorf("qa = %#v", qa)
	}
	if lanes, ok := qa["lanes"].([]any); !ok || len(lanes) != 0 {
		t.Errorf("qa.lanes = %#v, want an empty list", qa["lanes"])
	}
	for _, k := range []string{"pending", "pendingUnits", "heldUnits", "syncRunDone", "finalRunDone"} {
		if _, ok := m[k]; ok {
			t.Errorf("internal field %q is sent to the browser", k)
		}
	}
}

const heldURL = "https://github.com/run/7"

// withHeldRun adds a cut run GitHub holds for approval, created at the
// fixture's current time: no jobs and no branch in its name.
func (fx *svcFixture) withHeldRun() {
	fx.gh.mu.Lock()
	defer fx.gh.mu.Unlock()
	fx.gh.runs = append(fx.gh.runs, Run{
		ID: 7, Name: "OBS Release Cut", WorkflowFile: WorkflowCut, Status: "completed", Conclusion: "action_required",
		HTMLURL: heldURL, CreatedAt: fx.clock.now(),
	})
}

// heldAfter records a cut or recut of the release branch now and adds the
// held run five seconds later; the clock ends ten seconds after the dispatch.
func (fx *svcFixture) heldAfter(t *testing.T, action, units string) {
	t.Helper()
	fx.recordCut(t, action, units)
	fx.clock.advance(5 * time.Second)
	fx.withHeldRun()
	fx.clock.advance(5 * time.Second)
}

func TestSnapshotHeldFirstCut(t *testing.T) {
	fx := newFixture(t)
	fx.withBuiltUnits()
	fx.heldAfter(t, "cut", "ppg:staging:18")
	rel := fx.oneRelease(t)
	if rel.ID != relID || rel.Branch != testBranch || rel.Step != StepCutting || rel.PR != 0 || len(rel.Units) != 0 ||
		rel.Cut == nil || rel.Cut.State != StateApproval || rel.Cut.URL != heldURL {
		t.Fatalf("release = %+v cut = %+v", rel, rel.Cut)
	}
	if rel.StartedBy != "earlier@percona.com" {
		t.Errorf("StartedBy = %q", rel.StartedBy)
	}
	b, err := json.Marshal(rel)
	if err != nil || !strings.Contains(string(b), `"cut":{"state":"approval","url":"`+heldURL+`"}`) {
		t.Errorf("json = %s err = %v", b, err)
	}
	rows, err := fx.svc.Units(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b := rows[0].Blocked; rows[0].Tickable || b == nil || b.Reason != "in_flight" || b.ReleaseID != relID ||
		b.Text != "Release "+relID+" is waiting for approval on GitHub." {
		t.Errorf("held unit = %+v blocked = %+v", rows[0], b)
	}
	if !rows[1].Tickable || rows[1].Blocked != nil {
		t.Errorf("other unit = %+v blocked = %+v", rows[1], rows[1].Blocked)
	}
	ctx := context.Background()
	if _, err := fx.svc.Cut(ctx, actor, []string{"ppg:staging:18"}); !errors.Is(err, ErrConflict) {
		t.Errorf("cut of a held unit: err = %v", err)
	}
	// No branch and no PR: there is nothing to give up, and nothing to repeat.
	if err := fx.svc.Abandon(ctx, actor, relID, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("abandon: err = %v", err)
	}
	if err := fx.svc.Recut(ctx, actor, relID, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("recut: err = %v", err)
	}
	for _, m := range []string{"Dispatch", "DeleteBranch", "ClosePull", "CreatePull"} {
		if c := fx.gh.callsOf(m); len(c) != 0 {
			t.Errorf("unexpected GitHub write: %v", c)
		}
	}
}

func TestSnapshotHeldRecut(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog(LabelSync)
	fx.heldAfter(t, "recut", "ppg:staging:18")
	ctx := context.Background()
	rel := fx.oneRelease(t)
	if rel.Step != StepCutting || rel.PR != testPR || rel.Cut == nil || rel.Cut.State != StateApproval || rel.Cut.URL != heldURL {
		t.Fatalf("step=%q pr=%d cut=%+v", rel.Step, rel.PR, rel.Cut)
	}
	_, saveErr := fx.svc.SaveChangelog(ctx, actor, relID, testHead, testUnit, editedSection)
	_, resetErr := fx.svc.ResetChangelog(ctx, actor, relID, testHead, testUnit)
	for name, err := range map[string]error{
		"save":       saveErr,
		"reset":      resetErr,
		"approve":    fx.svc.ApproveChangelog(ctx, actor, relID, testHead),
		"retry sync": fx.svc.RetrySync(ctx, actor, relID, testHead),
		"approve qa": fx.svc.ApproveQA(ctx, actor, relID, testHead),
		"release":    fx.svc.Release(ctx, actor, relID, testHead, true),
		"recut":      fx.svc.Recut(ctx, actor, relID, testHead),
	} {
		if !errors.Is(err, ErrConflict) {
			t.Errorf("%s while the cut waits for approval: err = %v", name, err)
		}
	}
	for _, m := range []string{"AmendFile", "AddLabel", "RemoveLabel", "Dispatch", "MergePull", "ClosePull", "DeleteBranch"} {
		if c := fx.gh.callsOf(m); len(c) != 0 {
			t.Errorf("unexpected GitHub write: %v", c)
		}
	}
	if err := fx.svc.Abandon(ctx, actor, relID, testHead); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if c := fx.gh.callsOf("DeleteBranch"); len(c) != 1 || c[0] != "DeleteBranch "+testBranch {
		t.Errorf("DeleteBranch = %v", c)
	}
}

// A held run does not open the PR of a branch that has none yet.
func TestSnapshotHeldRecutWithoutPR(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.heldAfter(t, "recut", "ppg:staging:18")
	rel := fx.oneRelease(t)
	if rel.Step != StepCutting || rel.PR != 0 || rel.Cut == nil || rel.Cut.State != StateApproval {
		t.Fatalf("step=%q pr=%d cut=%+v", rel.Step, rel.PR, rel.Cut)
	}
	if n := len(fx.gh.callsOf("CreatePull")); n != 0 {
		t.Errorf("CreatePull called %d times", n)
	}
	// The branch exists: it can be given up.
	if err := fx.svc.Abandon(context.Background(), actor, relID, testHead); err != nil {
		t.Fatalf("abandon: %v", err)
	}
}

func TestSnapshotHeldRunWithoutAuditRowIgnored(t *testing.T) {
	// No row at all.
	fx := newFixture(t)
	fx.withHeldRun()
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("releases = %+v, want none", snap.Releases)
	}

	// A row too long before the run, and one too long after it.
	for _, d := range []time.Duration{181 * time.Second, -31 * time.Second} {
		fx = newFixture(t)
		fx.atChangelog()
		fx.recordCut(t, "recut", "ppg:staging:18")
		fx.clock.advance(d)
		fx.withHeldRun()
		fx.clock.advance(-d + 10*time.Minute)
		if rel := fx.oneRelease(t); rel.Step != StepChangelog || rel.Cut == nil || rel.Cut.State != StateSuccess {
			t.Fatalf("%v: step=%q cut=%+v", d, rel.Step, rel.Cut)
		}
	}

	// A row that is not a cut.
	fx = newFixture(t)
	fx.atChangelog()
	fx.recordCut(t, "save_changelog", testUnit)
	fx.withHeldRun()
	if rel := fx.oneRelease(t); rel.Step != StepChangelog {
		t.Fatalf("step = %q", rel.Step)
	}
}

// The run is matched to the closest dispatch, not to the first in the window.
func TestSnapshotHeldRunClosestAuditRow(t *testing.T) {
	fx := newFixture(t)
	other := "release/20261111T150000"
	err := store.AddReleaseAudit(fx.db, store.ReleaseAudit{At: fx.clock.now(), Email: actor, Action: "cut", Branch: other, Detail: "ppg:staging:17:extras"})
	if err != nil {
		t.Fatal(err)
	}
	fx.clock.advance(60 * time.Second)
	fx.heldAfter(t, "cut", "ppg:staging:18")
	if rel := fx.oneRelease(t); rel.Branch != testBranch || rel.Cut == nil || rel.Cut.State != StateApproval {
		t.Fatalf("release = %+v", rel)
	}
}

func TestSnapshotHeldRunApprovedLater(t *testing.T) {
	fx := newFixture(t)
	fx.heldAfter(t, "cut", "ppg:staging:18")
	if rel := fx.oneRelease(t); rel.Cut == nil || rel.Cut.State != StateApproval {
		t.Fatalf("held: cut = %+v", rel.Cut)
	}
	// Someone approves it on GitHub: the same run starts and gets its name.
	fx.gh.mu.Lock()
	fx.gh.runs[0].Name, fx.gh.runs[0].Status, fx.gh.runs[0].Conclusion = CutRunName(testBranch), "in_progress", ""
	fx.gh.mu.Unlock()
	fx.svc.Invalidate()
	rel := fx.oneRelease(t)
	if rel.ID != relID || rel.Step != StepCutting || rel.Cut == nil || rel.Cut.State != StateRunning || rel.Cut.URL != heldURL {
		t.Fatalf("approved: step=%q cut=%+v", rel.Step, rel.Cut)
	}
}

// A named run newer than the held one speaks for the branch.
func TestSnapshotHeldRunSupersededByNamedRun(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	fx.heldAfter(t, "recut", "ppg:staging:18")
	fx.gh.runs = append(fx.gh.runs, Run{ID: 8, Name: CutRunName(testBranch), WorkflowFile: WorkflowCut,
		Status: "completed", Conclusion: "success", HTMLURL: "https://github.com/run/8", CreatedAt: fx.clock.now()})
	if rel := fx.oneRelease(t); rel.Step != StepChangelog || rel.Cut == nil || rel.Cut.URL != "https://github.com/run/8" {
		t.Fatalf("step=%q cut=%+v", rel.Step, rel.Cut)
	}
}

// The held run wins over the marker of the dispatch that created it.
func TestSnapshotHeldRunWinsOverPendingMarker(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	ctx := context.Background()
	if err := fx.svc.Recut(ctx, actor, relID, testHead); err != nil {
		t.Fatal(err)
	}
	if rel := fx.oneRelease(t); rel.Cut == nil || rel.Cut.State != StateQueued {
		t.Fatalf("pending: cut = %+v", rel.Cut)
	}
	fx.clock.advance(5 * time.Second)
	fx.withHeldRun()
	fx.svc.Invalidate()
	rel := fx.oneRelease(t)
	if rel.Step != StepCutting || rel.Cut == nil || rel.Cut.State != StateApproval || rel.Cut.URL != heldURL {
		t.Fatalf("held: step=%q cut=%+v", rel.Step, rel.Cut)
	}
	// Not "wait for the cut": the release can be given up.
	if err := fx.svc.Abandon(ctx, actor, relID, testHead); err != nil {
		t.Fatalf("abandon: %v", err)
	}
}

func TestSnapshotHeldFirstCutExpires(t *testing.T) {
	fx := newFixture(t)
	fx.heldAfter(t, "cut", "ppg:staging:18")
	fx.clock.advance(23 * time.Hour)
	if rel := fx.oneRelease(t); rel.Cut == nil || rel.Cut.State != StateApproval {
		t.Fatalf("at 23h: cut = %+v", rel.Cut)
	}
	fx.clock.advance(2 * time.Hour)
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("at 25h: releases = %+v, want none", snap.Releases)
	}
	rows, err := fx.svc.Units(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b := rows[0].Blocked; b != nil && b.Reason == "in_flight" {
		t.Errorf("unit still in flight: %+v", b)
	}
}

// A held re-cut older than a day is ignored: the release shows as it is.
func TestSnapshotHeldRecutExpires(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	fx.heldAfter(t, "recut", "ppg:staging:18")
	fx.clock.advance(25 * time.Hour)
	if rel := fx.oneRelease(t); rel.Step != StepChangelog || rel.Cut == nil || rel.Cut.State != StateSuccess {
		t.Fatalf("step=%q cut=%+v", rel.Step, rel.Cut)
	}
}

// Abandoning a held re-cut deletes the branch and closes the PR: the held run
// stays listed on GitHub but the release does not come back.
func TestSnapshotHeldRecutAbandonedStaysGone(t *testing.T) {
	fx := newFixture(t)
	fx.withBuiltUnits()
	fx.atChangelog(LabelSync)
	fx.heldAfter(t, "recut", "ppg:staging:18")
	ctx := context.Background()
	if rel := fx.oneRelease(t); rel.Cut == nil || rel.Cut.State != StateApproval {
		t.Fatalf("before: cut = %+v", rel.Cut)
	}
	if err := fx.svc.Abandon(ctx, actor, relID, testHead); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if c := fx.gh.callsOf("ClosePull"); len(c) != 1 {
		t.Errorf("ClosePull = %v", c)
	}
	if c := fx.gh.callsOf("DeleteBranch"); len(c) != 1 {
		t.Errorf("DeleteBranch = %v", c)
	}
	fx.clock.advance(time.Second)
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("releases = %+v, want none", snap.Releases)
	}
	rows, err := fx.svc.Units(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b := rows[0].Blocked; b != nil && b.Reason == "in_flight" {
		t.Errorf("unit still in flight: %+v", b)
	}
}

// Two cuts dispatched close together each get their own held run.
func TestSnapshotHeldRunsMatchedOneToOne(t *testing.T) {
	fx := newFixture(t)
	other := "release/20261111T150000"
	t0 := fx.clock.now()
	for i, b := range []string{testBranch, other} {
		err := store.AddReleaseAudit(fx.db, store.ReleaseAudit{At: t0.Add(time.Duration(i) * 10 * time.Second), Email: actor, Action: "cut", Branch: b, Detail: "ppg:staging:18"})
		if err != nil {
			t.Fatal(err)
		}
	}
	fx.gh.mu.Lock()
	fx.gh.runs = append(fx.gh.runs,
		Run{ID: 7, Name: "OBS Release Cut", WorkflowFile: WorkflowCut, Status: "completed", Conclusion: "action_required", HTMLURL: "https://github.com/run/7", CreatedAt: t0.Add(5 * time.Second)},
		Run{ID: 8, Name: "OBS Release Cut", WorkflowFile: WorkflowCut, Status: "completed", Conclusion: "action_required", HTMLURL: "https://github.com/run/8", CreatedAt: t0.Add(15 * time.Second)},
	)
	fx.gh.mu.Unlock()
	fx.clock.advance(20 * time.Second)
	snap := fx.snapshot(t)
	if len(snap.Releases) != 2 {
		t.Fatalf("releases = %+v", snap.Releases)
	}
	want := map[string]string{testBranch: "https://github.com/run/7", other: "https://github.com/run/8"}
	for _, r := range snap.Releases {
		if r.Cut == nil || r.Cut.State != StateApproval || r.Cut.URL != want[r.Branch] {
			t.Errorf("%s: cut = %+v, want url %s", r.Branch, r.Cut, want[r.Branch])
		}
	}
}

// Two cuts in the window but one held run: only the closest branch is held.
func TestSnapshotOneHeldRunTwoCutsClosestWins(t *testing.T) {
	fx := newFixture(t)
	other := "release/20261111T150000"
	t0 := fx.clock.now()
	for i, b := range []string{testBranch, other} {
		err := store.AddReleaseAudit(fx.db, store.ReleaseAudit{At: t0.Add(time.Duration(i) * 10 * time.Second), Email: actor, Action: "cut", Branch: b, Detail: "ppg:staging:18"})
		if err != nil {
			t.Fatal(err)
		}
	}
	fx.gh.mu.Lock()
	fx.gh.runs = append(fx.gh.runs,
		Run{ID: 8, Name: "OBS Release Cut", WorkflowFile: WorkflowCut, Status: "completed", Conclusion: "action_required", HTMLURL: "https://github.com/run/8", CreatedAt: t0.Add(15 * time.Second)},
	)
	fx.gh.mu.Unlock()
	fx.clock.advance(20 * time.Second)
	rel := fx.oneRelease(t)
	if rel.Branch != other || rel.Cut == nil || rel.Cut.State != StateApproval || rel.Cut.URL != "https://github.com/run/8" {
		t.Fatalf("release = %+v cut = %+v", rel, rel.Cut)
	}
}

// Independent closest-row matching would give both runs to the second cut.
func TestSnapshotHeldRunsOneToOneNotIndependent(t *testing.T) {
	fx := newFixture(t)
	other := "release/20261111T150000"
	t0 := fx.clock.now()
	for i, b := range []string{testBranch, other} {
		err := store.AddReleaseAudit(fx.db, store.ReleaseAudit{At: t0.Add(time.Duration(i) * 10 * time.Second), Email: actor, Action: "cut", Branch: b, Detail: "ppg:staging:18"})
		if err != nil {
			t.Fatal(err)
		}
	}
	fx.gh.mu.Lock()
	fx.gh.runs = append(fx.gh.runs,
		Run{ID: 7, Name: "OBS Release Cut", WorkflowFile: WorkflowCut, Status: "completed", Conclusion: "action_required", HTMLURL: "https://github.com/run/7", CreatedAt: t0.Add(9 * time.Second)},
		Run{ID: 8, Name: "OBS Release Cut", WorkflowFile: WorkflowCut, Status: "completed", Conclusion: "action_required", HTMLURL: "https://github.com/run/8", CreatedAt: t0.Add(12 * time.Second)},
	)
	fx.gh.mu.Unlock()
	fx.clock.advance(20 * time.Second)
	snap := fx.snapshot(t)
	if len(snap.Releases) != 2 {
		t.Fatalf("releases = %+v", snap.Releases)
	}
	want := map[string]string{other: "https://github.com/run/7", testBranch: "https://github.com/run/8"}
	for _, r := range snap.Releases {
		if r.Cut == nil || r.Cut.State != StateApproval || r.Cut.URL != want[r.Branch] {
			t.Errorf("%s: cut = %+v, want url %s", r.Branch, r.Cut, want[r.Branch])
		}
	}
}

const (
	testYAML17 = "root/ppg/releases/17/extras/release.yaml"
	yaml17     = "project: ppg:staging:17:extras\nreleases:\n  - tag: ppg/17.11-5\n    source_commit: ccc\n"
)

// withMergedTwoUnits adds a merged release PR of two units (tags ppg/17.11-5
// and ppg/18.6-5) and release runs that all carry the pre-contract title.
func (fx *svcFixture) withMergedTwoUnits(number int) {
	merged := fx.clock.now().Add(-time.Hour)
	sha := fmt.Sprintf("merge%d", number)
	fx.gh.merged = append(fx.gh.merged, Pull{
		Number: number, HeadRef: fmt.Sprintf("x%d", number), HeadSHA: fmt.Sprintf("h%d", number), HeadRepo: fakeRepo, BaseRepo: fakeRepo,
		State: "closed", MergedAt: &merged, MergeSHA: sha,
	})
	fx.gh.pullFiles[number] = []string{testYAML, testYAML17}
	fx.gh.setFile(sha, testYAML, mainYAML)
	fx.gh.setFile(sha, testYAML17, yaml17)
	fx.gh.runs = append(fx.gh.runs, Run{ID: int64(900 + number), Name: "OBS Release", WorkflowFile: WorkflowRelease,
		Status: "completed", Conclusion: "success", CreatedAt: fx.clock.now()})
}

func (fx *svcFixture) publish(tags ...string) {
	for _, tag := range tags {
		fx.gh.releases = append(fx.gh.releases, GHRelease{Tag: tag, PublishedAt: fx.clock.now()})
	}
}

func TestSnapshotMergedWithoutNamedRunAndReleasesIsFinished(t *testing.T) {
	fx := newFixture(t)
	fx.withMergedTwoUnits(234)
	fx.publish("ppg/17.11-5", "ppg/18.6-5")
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("releases = %+v, want none", snap.Releases)
	}
	rows, err := fx.svc.Units(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Blocked != nil && r.Blocked.Reason == "in_flight" {
			t.Errorf("unit %s is in flight: %+v", r.Unit, r.Blocked)
		}
	}
}

func TestSnapshotMergedWithoutNamedRunMissingOneReleaseStaysListed(t *testing.T) {
	fx := newFixture(t)
	fx.withMergedTwoUnits(234)
	fx.publish("ppg/18.6-5")
	if rel := fx.oneRelease(t); rel.Step != StepReleasing || rel.PR != 234 {
		t.Fatalf("release = %+v", rel)
	}
}

func TestSnapshotMergedNamedRunStillDecides(t *testing.T) {
	fx := newFixture(t)
	fx.withMergedTwoUnits(234)
	fx.publish("ppg/17.11-5", "ppg/18.6-5")
	fx.gh.runs = append(fx.gh.runs, Run{ID: 20, Name: ReleaseRunName(234), WorkflowFile: WorkflowRelease,
		Status: "in_progress", CreatedAt: fx.clock.now()})
	fx.gh.jobs[20] = []Job{{ID: 300, Name: JobCopyPrefix + "obs)", Status: "in_progress"}}
	if rel := fx.oneRelease(t); rel.Step != StepReleasing {
		t.Fatalf("release = %+v", rel)
	}
	if n := len(fx.gh.callsOf("Releases")); n != 0 {
		t.Errorf("Releases called %d times with a named run", n)
	}
}

func TestSnapshotMergedUnresolvableWithoutNamedRunNotListed(t *testing.T) {
	fx := newFixture(t)
	fx.withMergedTwoUnits(234)
	fx.gh.mu.Lock()
	delete(fx.gh.files, "merge234:"+testYAML)
	delete(fx.gh.files, "merge234:"+testYAML17)
	fx.gh.mu.Unlock()
	fx.gh.fileErrs["merge234:"+testYAML] = fakeNotFound("f")
	fx.gh.fileErrs["merge234:"+testYAML17] = fakeNotFound("f")
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("releases = %+v, want none", snap.Releases)
	}
}

func TestSnapshotReleaseTagCacheNotRefetched(t *testing.T) {
	fx := newFixture(t)
	fx.withMergedTwoUnits(234)
	fx.publish("ppg/17.11-5", "ppg/18.6-5")
	fx.snapshot(t)
	if n := len(fx.gh.callsOf("Releases")); n != 1 {
		t.Fatalf("Releases calls = %d, want 1", n)
	}
	fx.withMergedTwoUnits(235)
	fx.svc.Invalidate()
	if snap := fx.snapshot(t); len(snap.Releases) != 0 {
		t.Fatalf("releases = %+v, want none", snap.Releases)
	}
	if n := len(fx.gh.callsOf("Releases")); n != 1 {
		t.Errorf("Releases calls = %d after second refresh, want 1", n)
	}
}

// While a cut has not produced its units yet, the release names the units
// that were asked for, from the dashboard's own audit row.
func TestSnapshotCutUnitsWhileCutting(t *testing.T) {
	fx := newFixture(t)
	fx.recordCut(t, "cut", "ppg:staging:16:tde ppg:staging:18")
	fx.withCutRun("in_progress", "")
	rel := fx.oneRelease(t)
	if rel.Step != StepCutting || len(rel.Units) != 0 {
		t.Fatalf("release = %+v", rel)
	}
	if got := strings.Join(rel.CutUnits, " "); got != "ppg:staging:16:tde ppg:staging:18" {
		t.Errorf("CutUnits = %q", got)
	}
	b, err := json.Marshal(rel)
	if err != nil || !strings.Contains(string(b), `"cut_units":["ppg:staging:16:tde","ppg:staging:18"]`) {
		t.Errorf("json = %s err = %v", b, err)
	}
}

// Once the units are resolved, or when the cut was not made from the
// dashboard, cut_units is an empty list, never null.
func TestSnapshotCutUnitsEmptyOtherwise(t *testing.T) {
	fx := newFixture(t)
	fx.withCutRun("in_progress", "")
	b, err := json.Marshal(fx.oneRelease(t))
	if err != nil || !strings.Contains(string(b), `"cut_units":[]`) {
		t.Errorf("no audit row: json = %s err = %v", b, err)
	}

	fx = newFixture(t)
	fx.recordCut(t, "cut", "ppg:staging:18")
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.withPR()
	rel := fx.oneRelease(t)
	if len(rel.Units) == 0 {
		t.Fatalf("fixture has no resolved units: %+v", rel)
	}
	if len(rel.CutUnits) != 0 {
		t.Errorf("CutUnits = %v, want none once units are resolved", rel.CutUnits)
	}
}

// An instance that holds none of a unit's projects reports outcome "skipped"
// for it: that is not a failure. Every card lists each unit with its outcome,
// and a card with nothing but skipped entries had nothing to sync.
func TestSnapshotPopulateSkippedOutcome(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withPR(LabelSync)
	fx.withPRRun("completed", "success",
		populateJob(100, "obs", "completed", "success"),
		populateJob(101, "labs", "completed", "success"),
		populateJob(102, "mixed", "completed", "success"),
	)
	fx.gh.artifacts["10:"+ArtifactReportPrefix+"obs"] = []byte(
		`[{"project":"ppg:releases:16:tde","outcome":"success","removals":[]},
		  {"project":"ppg:releases:18","outcome":"success","removals":[]}]`)
	fx.gh.artifacts["10:"+ArtifactReportPrefix+"labs"] = []byte(
		`[{"project":"ppg:releases:16:tde","outcome":"skipped","removals":[]},
		  {"project":"ppg:releases:18","outcome":"skipped","removals":[]}]`)
	fx.gh.artifacts["10:"+ArtifactReportPrefix+"mixed"] = []byte(
		`[{"project":"ppg:releases:16:tde","outcome":"skipped","removals":[]},
		  {"project":"ppg:releases:18","outcome":"success","removals":[]}]`)

	rel := fx.oneRelease(t)
	if rel.SyncState != string(StateSuccess) || rel.SyncError != "" || len(rel.Sync) != 3 {
		t.Fatalf("SyncState=%q SyncError=%q Sync=%+v", rel.SyncState, rel.SyncError, rel.Sync)
	}
	obs, labs, mixed := rel.Sync[0], rel.Sync[1], rel.Sync[2]
	if obs.NothingToSync || !reflect.DeepEqual(obs.Units, []CardUnit{{"ppg:releases:16:tde", "success"}, {"ppg:releases:18", "success"}}) {
		t.Errorf("obs card = %+v", obs)
	}
	if !labs.NothingToSync || labs.State != StateSuccess ||
		!reflect.DeepEqual(labs.Units, []CardUnit{{"ppg:releases:16:tde", "skipped"}, {"ppg:releases:18", "skipped"}}) {
		t.Errorf("labs card = %+v", labs)
	}
	if mixed.NothingToSync || !reflect.DeepEqual(mixed.Units, []CardUnit{{"ppg:releases:16:tde", "skipped"}, {"ppg:releases:18", "success"}}) {
		t.Errorf("mixed card = %+v", mixed)
	}
	b, err := json.Marshal(labs)
	if err != nil || !strings.Contains(string(b), `"units":[{"project":"ppg:releases:16:tde","outcome":"skipped"},`) || !strings.Contains(string(b), `"nothing_to_sync":true`) {
		t.Errorf("json = %s err = %v", b, err)
	}
	if strings.Contains(string(b), "failed_units") || strings.Contains(string(b), "skipped_units") {
		t.Errorf("old fields still sent: %s", b)
	}
}

// A skipped entry next to a failed one keeps its own outcome; an unknown
// outcome is reported as a failure.
func TestSnapshotPopulateSkippedIsNotFailed(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withPR(LabelSync)
	fx.withPRRun("completed", "failure", populateJob(100, "obs", "completed", "failure"))
	fx.gh.artifacts["10:"+ArtifactReportPrefix+"obs"] = []byte(
		`[{"project":"ppg:releases:18","outcome":"failure","removals":[]},
		  {"project":"ppg:releases:16:tde","outcome":"skipped","removals":[]},
		  {"project":"ppg:releases:17","outcome":"weird","removals":[]}]`)
	card := fx.oneRelease(t).Sync[0]
	want := []CardUnit{{"ppg:releases:18", "failure"}, {"ppg:releases:16:tde", "skipped"}, {"ppg:releases:17", "failure"}}
	if !reflect.DeepEqual(card.Units, want) || card.NothingToSync {
		t.Errorf("card = %+v", card)
	}
}

// A unit is populated when every instance reports success or skipped and at
// least one reports success. A unit skipped everywhere was populated nowhere:
// the sync failed, even though every job succeeded.
func TestSnapshotUnitPopulatedNowhereFailsTheSync(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withPR(LabelSync)
	fx.withPRRun("completed", "success",
		populateJob(100, "obs", "completed", "success"),
		populateJob(101, "labs", "completed", "success"),
	)
	fx.gh.artifacts["10:"+ArtifactReportPrefix+"obs"] = []byte(
		`[{"project":"ppg:releases:18","outcome":"success","removals":[]},
		  {"project":"ppg:releases:16:tde","outcome":"skipped","removals":[]}]`)
	fx.gh.artifacts["10:"+ArtifactReportPrefix+"labs"] = []byte(
		`[{"project":"ppg:releases:18","outcome":"skipped","removals":[]},
		  {"project":"ppg:releases:16:tde","outcome":"skipped","removals":[]}]`)
	rel := fx.oneRelease(t)
	if rel.Step != StepSync || rel.SyncState != string(StateFailure) {
		t.Fatalf("Step=%q SyncState=%q, want sync/failure", rel.Step, rel.SyncState)
	}
	if rel.SyncError != "ppg:releases:16:tde was not populated on any instance." {
		t.Errorf("SyncError = %q", rel.SyncError)
	}
	// QA cannot be approved, and the sync can be retried.
	ctx := context.Background()
	if err := fx.svc.ApproveQA(ctx, actor, rel.ID, rel.HeadSHA); !errors.Is(err, ErrConflict) {
		t.Errorf("ApproveQA: err = %v, want a conflict", err)
	}
	if err := fx.svc.RetrySync(ctx, actor, rel.ID, rel.HeadSHA); err != nil {
		t.Errorf("RetrySync: err = %v", err)
	}
}

// The rule is only applied once every instance has reported: a missing
// report must not make a unit look populated nowhere.
func TestSnapshotUnitRuleNeedsEveryReport(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withPR(LabelSync)
	fx.withPRRun("completed", "success",
		populateJob(100, "obs", "completed", "success"),
		populateJob(101, "labs", "completed", "success"),
	)
	fx.gh.artifacts["10:"+ArtifactReportPrefix+"labs"] = []byte(
		`[{"project":"ppg:releases:16:tde","outcome":"skipped","removals":[]}]`)
	rel := fx.oneRelease(t)
	if rel.SyncState != string(StateSuccess) || rel.SyncError != "" {
		t.Errorf("SyncState=%q SyncError=%q, want success with no error", rel.SyncState, rel.SyncError)
	}
}

// The description of the release PR lists the units being released with
// their release ids, and nothing else: no changelog text, whatever its size.
func TestPullBodyListsUnits(t *testing.T) {
	units := []UnitState{
		{Unit: "ppg:staging:18", ReleaseDir: "root/ppg/releases/18", ReleaseID: "18.7-1", Changelog: "## [18.7-1]\n\n- from the branch\n"},
		{Unit: "ppg:staging:16:tde", ReleaseDir: "root/ppg/releases/16/tde", ReleaseID: "tde-16.16-2", Changelog: "## [tde-16.16-2]\n\n- x\n"},
	}
	gen := "## [18.7-1]\n\n- generated by the cut\n"
	units[0].Generated = &gen
	want := "Units in this release:\n\n- `ppg:staging:18`: `18.7-1`\n- `ppg:staging:16:tde`: `tde-16.16-2`\n"
	if got := pullBody(units); got != want {
		t.Errorf("body =\n%s\nwant\n%s", got, want)
	}
	if got := pullBody(nil); got != "" {
		t.Errorf("body without units = %q", got)
	}
}

// A title is limited too.
func TestPullTitleIsBounded(t *testing.T) {
	var units []UnitState
	for i := 0; i < 40; i++ {
		units = append(units, UnitState{ReleaseID: fmt.Sprintf("common-upgrade-extras-%d", i)})
	}
	title := pullTitle(units)
	if len(title) > 200 || !strings.HasPrefix(title, "Release common-upgrade-extras-0, ") || !strings.HasSuffix(title, " more") {
		t.Errorf("title (%d bytes) = %q", len(title), title)
	}
	if got := pullTitle(units[:2]); got != "Release common-upgrade-extras-0, common-upgrade-extras-1" {
		t.Errorf("short title = %q", got)
	}
}

// When GitHub refuses to open the PR the release says so, with nothing but
// the method, path and status, and the next refresh tries again.
func TestSnapshotReportsPRThatCannotBeOpened(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.gh.createErr = &APIError{Method: "POST", Path: "/repos/x/pulls", Status: 422}

	rel := fx.oneRelease(t)
	if rel.Step != StepCutting || rel.PR != 0 {
		t.Fatalf("Step=%q PR=%d, want cutting with no PR", rel.Step, rel.PR)
	}
	if rel.PRError != "github POST /repos/x/pulls: HTTP 422" {
		t.Errorf("PRError = %q", rel.PRError)
	}
	b, _ := json.Marshal(rel)
	if !strings.Contains(string(b), `"pr_error":"github POST /repos/x/pulls: HTTP 422"`) {
		t.Errorf("json = %s", b)
	}

	fx.gh.createErr = nil
	fx.svc.Invalidate()
	rel = fx.oneRelease(t)
	if rel.PR == 0 || rel.PRError != "" || rel.Step != StepChangelog {
		t.Errorf("after GitHub accepts: PR=%d PRError=%q Step=%q", rel.PR, rel.PRError, rel.Step)
	}
	if n := len(fx.gh.callsOf("CreatePull")); n != 2 {
		t.Errorf("CreatePull calls = %d, want 2", n)
	}
}

// heldRun is a run of the release PR's head that GitHub holds for approval.
func heldRun(id int64, workflow, actor string) Run {
	return Run{ID: id, WorkflowFile: workflow, Event: "pull_request", Actor: actor, HeadSHA: testHead,
		Status: "completed", Conclusion: "action_required"}
}

func TestSnapshotApprovesRunsHeldAfterTheCutPush(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.withPR()
	fx.gh.runs = append(fx.gh.runs,
		heldRun(20, WorkflowPR, ActorActions),
		heldRun(21, "project-config-check.yml", ActorActions),
		heldRun(22, WorkflowPR, "someone"), // not the cut's push: left to a person
	)

	fx.oneRelease(t)
	if got := fx.gh.callsOf("ApproveRun"); len(got) != 2 || got[0] != "ApproveRun 20" || got[1] != "ApproveRun 21" {
		t.Fatalf("approved %v, want runs 20 and 21", got)
	}
	fx.svc.Invalidate()
	fx.oneRelease(t)
	if got := fx.gh.callsOf("ApproveRun"); len(got) != 2 {
		t.Fatalf("approved again: %v", got)
	}
}

func TestSnapshotApprovalRefusedIsAskedOnce(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.withPR()
	fx.gh.runs = append(fx.gh.runs, heldRun(20, WorkflowPR, ActorActions))
	fx.gh.approveErr = &APIError{Method: "POST", Path: "/x", Status: 403}

	// The refusal does not fail the refresh; the run stays held on GitHub.
	if rel := fx.oneRelease(t); rel.Step != StepChangelog {
		t.Fatalf("step %q, want %q", rel.Step, StepChangelog)
	}
	fx.svc.Invalidate()
	fx.oneRelease(t)
	if got := fx.gh.callsOf("ApproveRun"); len(got) != 1 {
		t.Fatalf("a refused approval was asked %d times, want 1", len(got))
	}

	// An error that is not GitHub's answer is tried again.
	fx2 := newFixture(t)
	fx2.withBranch()
	fx2.withCutRun("completed", "success")
	fx2.withPR()
	fx2.gh.runs = append(fx2.gh.runs, heldRun(20, WorkflowPR, ActorActions))
	fx2.gh.approveErr = errors.New("connection reset")
	fx2.oneRelease(t)
	fx2.svc.Invalidate()
	fx2.oneRelease(t)
	if got := fx2.gh.callsOf("ApproveRun"); len(got) != 2 {
		t.Fatalf("a failed approval was asked %d times, want 2", len(got))
	}
}

func TestSnapshotNeverApprovesForkRuns(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.gh.open = []Pull{{Number: 47, HeadRef: testBranch, HeadSHA: "forkhead", HeadRepo: "someone/obs-packaging", BaseRepo: fakeRepo}}
	fx.gh.pullFiles[47] = []string{testYAML}
	fx.gh.setFile("forkhead", testYAML, strings.Replace(cutYAML, "18.6-6", "18.6-7", 1))
	held := heldRun(20, WorkflowPR, ActorActions)
	held.HeadSHA = "forkhead"
	fx.gh.runs = append(fx.gh.runs, held)

	fx.snapshot(t)
	if got := fx.gh.callsOf("ApproveRun"); len(got) != 0 {
		t.Fatalf("approved a fork's run: %v", got)
	}
}
