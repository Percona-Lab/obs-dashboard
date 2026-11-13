package release

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

const (
	actor    = "someone@percona.com"
	extrasID = "root/ppg/releases/17/extras"
)

// withBuiltUnits makes both units tickable.
func (fx *svcFixture) withBuiltUnits() {
	fx.svc.pkgs = fakeSource{
		"ppg:staging:18":        {pkg("ppg:staging:18", "pg", "18.6", model.RollupPublished)},
		"ppg:staging:17:extras": {pkg("ppg:staging:17:extras", "ex", "17.10", model.RollupPublished)},
	}
}

// atChangelog sets up a release at the changelog step.
func (fx *svcFixture) atChangelog(labels ...string) {
	fx.withBranch()
	fx.withCutRun("completed", "success")
	fx.withPR(labels...)
}

// atQA sets up a release at the qa step; lanes are given as job conclusions
// by lane name, "" meaning no QA label yet.
func (fx *svcFixture) atQA(qaLabel bool, runStatus string, lanes ...Job) {
	labels := []string{LabelSync}
	if qaLabel {
		labels = append(labels, LabelQA)
	}
	fx.atChangelog(labels...)
	jobs := append([]Job{populateJob(100, "obs", "completed", "success")}, lanes...)
	conclusion := ""
	if runStatus == "completed" {
		conclusion = "success"
	}
	fx.withPRRun(runStatus, conclusion, jobs...)
}

func laneJob(id int64, name, conclusion string) Job {
	return Job{ID: id, Name: JobLanePrefix + name, Status: "completed", Conclusion: conclusion}
}

// atReleasing sets up a merged PR whose release run has the given conclusion.
func (fx *svcFixture) atReleasing(conclusion string) {
	merged := fx.clock.now().Add(-time.Hour)
	fx.gh.merged = append(fx.gh.merged, Pull{
		Number: testPR, HTMLURL: "https://github.com/pull/415", HeadRef: testBranch, HeadSHA: testHead,
		HeadRepo: fakeRepo, BaseRepo: fakeRepo, State: "closed", MergedAt: &merged, MergeSHA: "merge1",
	})
	fx.gh.pullFiles[testPR] = []string{testChangelogPath, testYAML, extrasID + "/release.yaml"}
	fx.gh.setFile("merge1", testYAML, cutYAML)
	fx.gh.setFile("merge1", testChangelogPath, cutChangelog)
	fx.gh.setFile("merge1", extrasID+"/release.yaml", "project: ppg:staging:17:extras\nreleases:\n  - ppg/extras-17.10-2\n")
	fx.gh.runs = append(fx.gh.runs, Run{
		ID: 20, Name: ReleaseRunName(testPR), WorkflowFile: WorkflowRelease, Status: "completed", Conclusion: conclusion,
		CreatedAt: fx.clock.now().Add(-30 * time.Minute),
	})
}

func (fx *svcFixture) auditRows(t *testing.T) []store.ReleaseAudit {
	t.Helper()
	m, err := store.ReleaseAuditByBranch(fx.db, []string{testBranch})
	if err != nil {
		t.Fatal(err)
	}
	return m[testBranch]
}

func (fx *svcFixture) wantOneAudit(t *testing.T, action string) store.ReleaseAudit {
	t.Helper()
	rows := fx.auditRows(t)
	if len(rows) != 1 || rows[0].Action != action || rows[0].Email != actor || rows[0].Branch != testBranch {
		t.Fatalf("audit rows = %+v, want one %q by %s", rows, action, actor)
	}
	return rows[0]
}

func (fx *svcFixture) wantNoWrites(t *testing.T) {
	t.Helper()
	for _, m := range []string{"AmendFile", "AddLabel", "RemoveLabel", "Comment", "Dispatch", "RerunJob", "RerunFailedJobs", "MergePull", "ClosePull", "DeleteBranch"} {
		if c := fx.gh.callsOf(m); len(c) != 0 {
			t.Errorf("unexpected GitHub write: %v", c)
		}
	}
	if rows := fx.auditRows(t); len(rows) != 0 {
		t.Errorf("unexpected audit rows: %+v", rows)
	}
}

func TestActionCutDispatches(t *testing.T) {
	fx := newFixture(t)
	fx.withBuiltUnits()
	fx.clock.t = time.Date(2026, 11, 11, 14, 32, 10, 0, time.UTC)
	id, err := fx.svc.Cut(context.Background(), actor, []string{"ppg:staging:18", "ppg:staging:17:extras"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "20261111T143210" {
		t.Errorf("id = %q", id)
	}
	got := fx.gh.callsOf("Dispatch")
	want := "Dispatch " + WorkflowCut + " branch=" + testBranch + " units=ppg:staging:18 ppg:staging:17:extras"
	if len(got) != 1 || got[0] != want {
		t.Errorf("Dispatch = %v, want %q", got, want)
	}
	a := fx.wantOneAudit(t, "cut")
	if a.Detail != "ppg:staging:18 ppg:staging:17:extras" {
		t.Errorf("detail = %q", a.Detail)
	}
}

func TestActionCutRefusesInFlightUnit(t *testing.T) {
	fx := newFixture(t)
	fx.withBuiltUnits()
	fx.atChangelog()
	_, err := fx.svc.Cut(context.Background(), actor, []string{"ppg:staging:18"})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "ppg:staging:18") {
		t.Fatalf("err = %v", err)
	}
	if n := len(fx.gh.callsOf("Dispatch")); n != 0 {
		t.Errorf("Dispatch called %d times", n)
	}
}

func TestActionCutBadInput(t *testing.T) {
	fx := newFixture(t)
	fx.withBuiltUnits()
	for _, units := range [][]string{nil, {"nope"}, {"ppg:staging:18", "ppg:staging:18"}} {
		if _, err := fx.svc.Cut(context.Background(), actor, units); !errors.Is(err, ErrBadRequest) {
			t.Errorf("units %v: err = %v", units, err)
		}
	}
	if n := len(fx.gh.callsOf("Dispatch")); n != 0 {
		t.Errorf("Dispatch called %d times", n)
	}
}

const editedSection = "## [18.6-6] - 2026-11-11\n\n- Edited by hand\n\n"

func TestActionSaveChangelogAmends(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	newHead, err := fx.svc.SaveChangelog(context.Background(), actor, "20261111T143210", testHead, testUnit, editedSection)
	if err != nil {
		t.Fatal(err)
	}
	got := fx.gh.callsOf("AmendFile")
	if len(got) != 1 || !strings.HasPrefix(got[0], "AmendFile "+testBranch+" "+testHead+" root/ppg/releases/18/CHANGELOG.md ") ||
		!strings.Contains(got[0], "- Edited by hand") || !strings.Contains(got[0], "Older entry") {
		t.Errorf("AmendFile = %v", got)
	}
	if newHead == "" || newHead == testHead {
		t.Errorf("newHead = %q", newHead)
	}
	if rel := fx.oneRelease(t); rel.HeadSHA != newHead {
		t.Errorf("head after save = %q, want %q", rel.HeadSHA, newHead)
	}
	fx.wantOneAudit(t, "save_changelog")
}

func TestActionSaveStaleHead(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	_, err := fx.svc.SaveChangelog(context.Background(), actor, "20261111T143210", "old", testUnit, editedSection)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	fx.wantNoWrites(t)
}

func TestActionSaveAfterApprovalRefused(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog(LabelSync)
	_, err := fx.svc.SaveChangelog(context.Background(), actor, "20261111T143210", testHead, testUnit, editedSection)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	fx.wantNoWrites(t)
}

func TestActionSaveBadSection(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	bad := editedSection + "## [9.9-9] - 2026-01-01\n"
	_, err := fx.svc.SaveChangelog(context.Background(), actor, "20261111T143210", testHead, testUnit, bad)
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v", err)
	}
	fx.wantNoWrites(t)
	_, err = fx.svc.SaveChangelog(context.Background(), actor, "20261111T143210", testHead, "no:such:unit", editedSection)
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("unknown unit: err = %v", err)
	}
}

func TestActionResetChangelog(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	_, err := fx.svc.ResetChangelog(context.Background(), actor, "20261111T143210", testHead, testUnit)
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("without a generated section: err = %v", err)
	}
	fx.wantNoWrites(t)

	// A finished run's missing artifact is looked for again after a while.
	fx.clock.advance(missingArtifactTTL)
	fx.gh.artifacts["1:"+ArtifactChangelogs] = []byte(`{"ppg:staging:18":"## [18.6-6] generated\n\n- From the cut\n"}`)
	newHead, err := fx.svc.ResetChangelog(context.Background(), actor, "20261111T143210", testHead, testUnit)
	if err != nil {
		t.Fatal(err)
	}
	got := fx.gh.callsOf("AmendFile")
	if len(got) != 1 || !strings.Contains(got[0], "- From the cut") || newHead == "" {
		t.Errorf("AmendFile = %v, newHead = %q", got, newHead)
	}
	fx.wantOneAudit(t, "save_changelog")
}

func TestActionApproveChangelog(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	if err := fx.svc.ApproveChangelog(context.Background(), actor, "20261111T143210", testHead); err != nil {
		t.Fatal(err)
	}
	if got := fx.gh.callsOf("AddLabel"); len(got) != 1 || got[0] != "AddLabel 415 obs-sync" {
		t.Errorf("AddLabel = %v", got)
	}
	fx.wantOneAudit(t, "approve_changelog")
	// The cache was dropped: the next read sees the label.
	if rel := fx.oneRelease(t); rel.Step != StepSync {
		t.Errorf("step after approval = %q", rel.Step)
	}
}

func TestActionRetrySyncRelabels(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog(LabelSync)
	fx.withPRRun("completed", "failure", populateJob(100, "obs", "completed", "failure"))
	if err := fx.svc.RetrySync(context.Background(), actor, "20261111T143210", testHead); err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, c := range fx.gh.allCalls() {
		if strings.HasPrefix(c, "RemoveLabel") || strings.HasPrefix(c, "AddLabel") {
			labels = append(labels, c)
		}
	}
	if len(labels) != 2 || labels[0] != "RemoveLabel 415 obs-sync" || labels[1] != "AddLabel 415 obs-sync" {
		t.Errorf("label calls = %v", labels)
	}
	fx.wantOneAudit(t, "retry_sync")
}

func TestActionRetrySyncOnlyWhenFailed(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog(LabelSync)
	fx.withPRRun("in_progress", "", populateJob(100, "obs", "in_progress", ""))
	if err := fx.svc.RetrySync(context.Background(), actor, "20261111T143210", testHead); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	fx.wantNoWrites(t)
}

func TestActionApproveQA(t *testing.T) {
	fx := newFixture(t)
	fx.atQA(false, "completed")
	if err := fx.svc.ApproveQA(context.Background(), actor, "20261111T143210", testHead); err != nil {
		t.Fatal(err)
	}
	if got := fx.gh.callsOf("AddLabel"); len(got) != 1 || got[0] != "AddLabel 415 qa-release" {
		t.Errorf("AddLabel = %v", got)
	}
	fx.wantOneAudit(t, "approve_qa")

	fx = newFixture(t)
	fx.atQA(true, "in_progress", Job{ID: 200, Name: JobLanePrefix + "packages / pg_tde", Status: "in_progress"})
	if err := fx.svc.ApproveQA(context.Background(), actor, "20261111T143210", testHead); !errors.Is(err, ErrConflict) {
		t.Fatalf("after QA started: err = %v", err)
	}
}

func TestActionRecutRemovesLabelsThenDispatches(t *testing.T) {
	fx := newFixture(t)
	fx.atQA(true, "in_progress", Job{ID: 200, Name: JobLanePrefix + "packages / pg_tde", Status: "in_progress"})
	if err := fx.svc.Recut(context.Background(), actor, "20261111T143210", testHead); err != nil {
		t.Fatal(err)
	}
	var writes []string
	for _, c := range fx.gh.allCalls() {
		if strings.HasPrefix(c, "RemoveLabel") || strings.HasPrefix(c, "Dispatch") || strings.HasPrefix(c, "AddLabel") {
			writes = append(writes, c)
		}
	}
	want := []string{
		"RemoveLabel 415 obs-sync",
		"RemoveLabel 415 qa-release",
		"Dispatch " + WorkflowCut + " branch=" + testBranch + " units=" + testUnit,
	}
	if strings.Join(writes, "|") != strings.Join(want, "|") {
		t.Errorf("writes = %v\nwant     %v", writes, want)
	}
	if a := fx.wantOneAudit(t, "recut"); a.Detail != testUnit {
		t.Errorf("detail = %q", a.Detail)
	}
}

func TestActionRecutHandMadeRefused(t *testing.T) {
	fx := newFixture(t)
	fx.gh.open = []Pull{{Number: 47, HeadRef: "release-pg-18", HeadSHA: "h47", HeadRepo: fakeRepo, BaseRepo: fakeRepo, State: "open"}}
	fx.gh.pullFiles[47] = []string{testYAML, testChangelogPath}
	fx.gh.setFile("release-pg-18", testYAML, cutYAML)
	fx.gh.setFile("release-pg-18", testChangelogPath, cutChangelog)
	if err := fx.svc.Recut(context.Background(), actor, "pr-47", "h47"); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	fx.wantNoWrites(t)
}

func TestActionRecutAfterFailedFirstCut(t *testing.T) {
	fx := newFixture(t)
	fx.withCutRun("completed", "failure")
	id := "20261111T143210"
	if err := fx.svc.Recut(context.Background(), actor, id, ""); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("no recorded units: err = %v", err)
	}
	fx.wantNoWrites(t)

	fx.recordCut(t, "cut", "ppg:staging:18 ppg:staging:17:extras")
	fx.recordCut(t, "recut", "ppg:staging:18")
	if err := fx.svc.Recut(context.Background(), actor, id, ""); err != nil {
		t.Fatal(err)
	}
	got := fx.gh.callsOf("Dispatch")
	want := "Dispatch " + WorkflowCut + " branch=" + testBranch + " units=ppg:staging:18"
	if len(got) != 1 || got[0] != want {
		t.Errorf("Dispatch = %v, want %q", got, want)
	}
	if n := len(fx.gh.callsOf("RemoveLabel")); n != 0 {
		t.Errorf("RemoveLabel called %d times without a PR", n)
	}
}

func (fx *svcFixture) recordCut(t *testing.T, action, units string) {
	t.Helper()
	err := store.AddReleaseAudit(fx.db, store.ReleaseAudit{At: fx.clock.now(), Email: "earlier@percona.com", Action: action, Branch: testBranch, Detail: units})
	if err != nil {
		t.Fatal(err)
	}
}

func TestActionRecutRefusedWhileCutRuns(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("in_progress", "")
	if err := fx.svc.Recut(context.Background(), actor, "20261111T143210", testHead); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	fx.wantNoWrites(t)
}

func TestActionReleaseNeedsConfirm(t *testing.T) {
	fx := newFixture(t)
	fx.atQA(true, "completed", laneJob(200, "packages / pg_tde", "failure"), laneJob(201, "packages / pg_stat", "success"))
	err := fx.svc.Release(context.Background(), actor, "20261111T143210", testHead, false)
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v", err)
	}
	if n := len(fx.gh.callsOf("MergePull")); n != 0 {
		t.Fatalf("MergePull called %d times", n)
	}
	if err := fx.svc.Release(context.Background(), actor, "20261111T143210", testHead, true); err != nil {
		t.Fatal(err)
	}
	if got := fx.gh.callsOf("MergePull"); len(got) != 1 || got[0] != "MergePull 415 "+testHead {
		t.Errorf("MergePull = %v", got)
	}
	a := fx.wantOneAudit(t, "release")
	if a.Detail != "Release / OBS QA / packages / pg_tde" || a.PR != testPR {
		t.Errorf("audit = %+v", a)
	}
	// The release branch is cleaned up once merged.
	if got := fx.gh.callsOf("DeleteBranch"); len(got) != 1 || got[0] != "DeleteBranch "+testBranch {
		t.Errorf("DeleteBranch = %v", got)
	}
}

func TestActionReleaseSurvivesBranchDeleteFailure(t *testing.T) {
	fx := newFixture(t)
	fx.atQA(true, "completed", laneJob(200, "packages / pg_tde", "success"))
	fx.gh.deleteErr = errors.New("boom")
	if err := fx.svc.Release(context.Background(), actor, "20261111T143210", testHead, false); err != nil {
		t.Fatal(err)
	}
	fx.wantOneAudit(t, "release")
}

func TestActionReleaseBeforeQAFinished(t *testing.T) {
	fx := newFixture(t)
	fx.atQA(true, "in_progress", Job{ID: 200, Name: JobLanePrefix + "packages / pg_tde", Status: "in_progress"})
	err := fx.svc.Release(context.Background(), actor, "20261111T143210", testHead, true)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	fx.wantNoWrites(t)
}

func TestActionRetryReleaseInputs(t *testing.T) {
	fx := newFixture(t)
	fx.atReleasing("failure")
	if err := fx.svc.RetryRelease(context.Background(), actor, "20261111T143210"); err != nil {
		t.Fatal(err)
	}
	got := fx.gh.callsOf("Dispatch")
	want := "Dispatch " + WorkflowRelease + " pr_number=415 tags=ppg/18.6-6 ppg/extras-17.10-2"
	if len(got) != 1 || got[0] != want {
		t.Errorf("Dispatch = %v, want %q", got, want)
	}
	fx.wantOneAudit(t, "retry_release")

	fx = newFixture(t)
	fx.atReleasing("success")
	if err := fx.svc.RetryRelease(context.Background(), actor, "20261111T143210"); err == nil {
		t.Fatal("a finished release was retried")
	}
}

func TestActionAbandon(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	if err := fx.svc.Abandon(context.Background(), actor, "20261111T143210", testHead); err != nil {
		t.Fatal(err)
	}
	var writes []string
	for _, c := range fx.gh.allCalls() {
		if strings.HasPrefix(c, "DeleteBranch") || strings.HasPrefix(c, "ClosePull") {
			writes = append(writes, c)
		}
	}
	if len(writes) != 2 || writes[0] != "DeleteBranch "+testBranch || writes[1] != "ClosePull 415" {
		t.Errorf("writes = %v", writes)
	}
	fx.wantOneAudit(t, "abandon")

	fx = newFixture(t)
	fx.atReleasing("failure")
	if err := fx.svc.Abandon(context.Background(), actor, "20261111T143210", testHead); !errors.Is(err, ErrConflict) {
		t.Fatalf("after merge: err = %v", err)
	}
	fx.wantNoWrites(t)
}

func TestActionAbandonBranchDeleteFails(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	fx.gh.deleteErr = errors.New("boom")
	if err := fx.svc.Abandon(context.Background(), actor, "20261111T143210", testHead); err == nil {
		t.Fatal("want the delete error")
	}
	if n := len(fx.gh.callsOf("ClosePull")); n != 0 {
		t.Errorf("ClosePull called %d times", n)
	}
	if rows := fx.auditRows(t); len(rows) != 0 {
		t.Errorf("audit rows = %+v", rows)
	}
}

func TestActionAbandonIgnoresClosePullError(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	fx.gh.closeErr = errors.New("already closed")
	if err := fx.svc.Abandon(context.Background(), actor, "20261111T143210", testHead); err != nil {
		t.Fatal(err)
	}
	fx.wantOneAudit(t, "abandon")
}

func TestActionAbandonHandMadeKeepsBranch(t *testing.T) {
	fx := newFixture(t)
	fx.gh.open = []Pull{{Number: 47, HeadRef: "release-pg-18", HeadSHA: "h47", HeadRepo: fakeRepo, BaseRepo: fakeRepo, State: "open"}}
	fx.gh.pullFiles[47] = []string{testYAML, testChangelogPath}
	fx.gh.setFile("release-pg-18", testYAML, cutYAML)
	fx.gh.setFile("release-pg-18", testChangelogPath, cutChangelog)
	if err := fx.svc.Abandon(context.Background(), actor, "pr-47", "h47"); err != nil {
		t.Fatal(err)
	}
	if got := fx.gh.callsOf("ClosePull"); len(got) != 1 || got[0] != "ClosePull 47" {
		t.Errorf("ClosePull = %v", got)
	}
	if n := len(fx.gh.callsOf("DeleteBranch")); n != 0 {
		t.Errorf("DeleteBranch called %d times for a hand-made PR", n)
	}
}

func TestActionUnknownRelease(t *testing.T) {
	fx := newFixture(t)
	if err := fx.svc.ApproveChangelog(context.Background(), actor, "nope", testHead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestActionForkRefused(t *testing.T) {
	fx := newFixture(t)
	fx.gh.open = []Pull{{Number: 47, HeadRef: "feature", HeadSHA: "forkhead", HeadRepo: "someone/obs-packaging", BaseRepo: fakeRepo, State: "open"}}
	fx.gh.pullFiles[47] = []string{testYAML}
	fx.gh.setFile("forkhead", testYAML, cutYAML)
	ctx := context.Background()
	calls := map[string]error{
		"ApproveChangelog": fx.svc.ApproveChangelog(ctx, actor, "pr-47", "forkhead"),
		"RetrySync":        fx.svc.RetrySync(ctx, actor, "pr-47", "forkhead"),
		"ApproveQA":        fx.svc.ApproveQA(ctx, actor, "pr-47", "forkhead"),
		"Recut":            fx.svc.Recut(ctx, actor, "pr-47", "forkhead"),
		"Release":          fx.svc.Release(ctx, actor, "pr-47", "forkhead", true),
		"RetryRelease":     fx.svc.RetryRelease(ctx, actor, "pr-47"),
		"Abandon":          fx.svc.Abandon(ctx, actor, "pr-47", "forkhead"),
	}
	_, calls["SaveChangelog"] = fx.svc.SaveChangelog(ctx, actor, "pr-47", "forkhead", testUnit, editedSection)
	_, calls["ResetChangelog"] = fx.svc.ResetChangelog(ctx, actor, "pr-47", "forkhead", testUnit)
	for name, err := range calls {
		if !errors.Is(err, ErrConflict) {
			t.Errorf("%s: err = %v, want ErrConflict", name, err)
		}
	}
	for _, m := range []string{"AmendFile", "AddLabel", "RemoveLabel", "Comment", "Dispatch", "MergePull", "ClosePull", "DeleteBranch"} {
		if c := fx.gh.callsOf(m); len(c) != 0 {
			t.Errorf("fork: %v", c)
		}
	}
}

// TestActionNeverSendsEmail runs every action with an email and checks that
// no argument of any GitHub call carries it.
func TestActionNeverSendsEmail(t *testing.T) {
	ctx := context.Background()
	check := func(fx *svcFixture) {
		t.Helper()
		for _, c := range fx.gh.allCalls() {
			if strings.Contains(c, "someone") || strings.Contains(c, "@percona.com") {
				t.Errorf("GitHub call carries the email: %q", c)
			}
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	const id = "20261111T143210"

	fx := newFixture(t)
	fx.withBuiltUnits()
	_, err := fx.svc.Cut(ctx, actor, []string{"ppg:staging:18"})
	must(err)
	check(fx)

	fx = newFixture(t)
	fx.atChangelog()
	fx.gh.artifacts["1:"+ArtifactChangelogs] = []byte(`{"ppg:staging:18":"## [18.6-6] generated\n"}`)
	head, err := fx.svc.SaveChangelog(ctx, actor, id, testHead, testUnit, editedSection)
	must(err)
	head, err = fx.svc.ResetChangelog(ctx, actor, id, head, testUnit)
	must(err)
	must(fx.svc.ApproveChangelog(ctx, actor, id, head))
	check(fx)

	fx = newFixture(t)
	fx.atChangelog(LabelSync)
	fx.withPRRun("completed", "failure", populateJob(100, "obs", "completed", "failure"))
	must(fx.svc.RetrySync(ctx, actor, id, testHead))
	check(fx)

	fx = newFixture(t)
	fx.atQA(false, "completed")
	must(fx.svc.ApproveQA(ctx, actor, id, testHead))
	must(fx.svc.Recut(ctx, actor, id, testHead))
	check(fx)

	fx = newFixture(t)
	fx.atQA(true, "completed", laneJob(200, "packages / pg_tde", "failure"))
	must(fx.svc.Release(ctx, actor, id, testHead, true))
	check(fx)

	fx = newFixture(t)
	fx.atReleasing("failure")
	must(fx.svc.RetryRelease(ctx, actor, id))
	check(fx)

	fx = newFixture(t)
	fx.atChangelog()
	must(fx.svc.Abandon(ctx, actor, id, testHead))
	check(fx)
}

func TestActionCutRefusedOnStaleSnapshot(t *testing.T) {
	fx := newFixture(t)
	fx.withBuiltUnits()
	if _, err := fx.svc.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	fx.gh.setErr(errors.New("github unreachable"))
	_, err := fx.svc.Cut(context.Background(), actor, []string{"ppg:staging:18"})
	if err == nil {
		t.Fatal("Cut succeeded on stale data")
	}
	if n := len(fx.gh.callsOf("Dispatch")); n != 0 {
		t.Errorf("Dispatch called %d times", n)
	}
	m, aerr := store.ReleaseAuditByBranch(fx.db, []string{BranchPrefix + "20261111T150000"})
	if aerr != nil {
		t.Fatal(aerr)
	}
	for b, rows := range m {
		if len(rows) != 0 {
			t.Errorf("audit rows for %s: %+v", b, rows)
		}
	}
}

func TestActionCutTwiceQuickly(t *testing.T) {
	fx := newFixture(t)
	fx.withBuiltUnits()
	if _, err := fx.svc.Cut(context.Background(), actor, []string{"ppg:staging:18"}); err != nil {
		t.Fatal(err)
	}
	_, err := fx.svc.Cut(context.Background(), actor, []string{"ppg:staging:18"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second Cut err = %v, want ErrConflict", err)
	}
	if n := len(fx.gh.callsOf("Dispatch")); n != 1 {
		t.Errorf("Dispatch called %d times, want 1", n)
	}
}

func TestActionCutBranchNamesUnique(t *testing.T) {
	fx := newFixture(t)
	fx.withBuiltUnits()
	a, err := fx.svc.Cut(context.Background(), actor, []string{"ppg:staging:18"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := fx.svc.Cut(context.Background(), actor, []string{"ppg:staging:17:extras"})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("both cuts got branch id %q", a)
	}
	if n := len(fx.gh.callsOf("Dispatch")); n != 2 {
		t.Errorf("Dispatch called %d times, want 2", n)
	}
}

func TestActionAbandonRefusedWhileCutRunning(t *testing.T) {
	for _, status := range []string{"queued", "in_progress"} {
		fx := newFixture(t)
		fx.withBranch()
		fx.withCutRun(status, "")
		if err := fx.svc.Abandon(context.Background(), actor, "20261111T143210", testHead); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s: err = %v", status, err)
		}
		fx.wantNoWrites(t)
	}
}

const relID = "20261111T143210"

func TestActionChangelogLockedAfterFailedRecut(t *testing.T) {
	fx := newFixture(t)
	fx.withBranch()
	fx.withCutRun("completed", "failure")
	fx.withPR()
	fx.gh.artifacts["1:"+ArtifactChangelogs] = []byte(`{"ppg:staging:18":"## [18.6-6] generated\n"}`)
	ctx := context.Background()
	if rel := fx.oneRelease(t); rel.Step != StepChangelog || rel.Cut == nil || rel.Cut.State != StateFailure {
		t.Fatalf("release = %+v", rel)
	}
	_, err := fx.svc.SaveChangelog(ctx, actor, relID, testHead, testUnit, editedSection)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("save: err = %v", err)
	}
	if _, err = fx.svc.ResetChangelog(ctx, actor, relID, testHead, testUnit); !errors.Is(err, ErrConflict) {
		t.Errorf("reset: err = %v", err)
	}
	if err = fx.svc.ApproveChangelog(ctx, actor, relID, testHead); !errors.Is(err, ErrConflict) {
		t.Errorf("approve: err = %v", err)
	}
	fx.wantNoWrites(t)
	if err = fx.svc.Recut(ctx, actor, relID, testHead); err != nil {
		t.Fatalf("recut after a failed cut: %v", err)
	}
}

func TestActionRecutPendingUntilRunListed(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	ctx := context.Background()
	if err := fx.svc.Recut(ctx, actor, relID, testHead); err != nil {
		t.Fatal(err)
	}
	rel := fx.oneRelease(t)
	if rel.Step != StepCutting || rel.Cut == nil || rel.Cut.State != StateQueued || rel.Cut.URL != "" {
		t.Fatalf("after recut: step=%q cut=%+v", rel.Step, rel.Cut)
	}
	_, saveErr := fx.svc.SaveChangelog(ctx, actor, relID, testHead, testUnit, editedSection)
	for name, err := range map[string]error{
		"save":    saveErr,
		"approve": fx.svc.ApproveChangelog(ctx, actor, relID, testHead),
		"recut":   fx.svc.Recut(ctx, actor, relID, testHead),
		"abandon": fx.svc.Abandon(ctx, actor, relID, testHead),
	} {
		if !errors.Is(err, ErrConflict) {
			t.Errorf("%s while the dispatch is pending: err = %v", name, err)
		}
	}
	if n := len(fx.gh.callsOf("Dispatch")); n != 1 {
		t.Errorf("Dispatch called %d times, want 1", n)
	}
	if n := len(fx.gh.callsOf("AddLabel")) + len(fx.gh.callsOf("AmendFile")) + len(fx.gh.callsOf("DeleteBranch")); n != 0 {
		t.Errorf("%d writes while pending", n)
	}

	// GitHub lists the new run: the marker goes and the run speaks for itself.
	fx.clock.advance(5 * time.Second)
	fx.gh.mu.Lock()
	fx.gh.runs = append(fx.gh.runs, Run{ID: 2, Name: CutRunName(testBranch), WorkflowFile: WorkflowCut,
		Status: "completed", Conclusion: "success", HTMLURL: "https://github.com/run/2", CreatedAt: fx.clock.now()})
	fx.gh.mu.Unlock()
	fx.svc.Invalidate()
	rel = fx.oneRelease(t)
	if rel.Step != StepChangelog || rel.Cut == nil || rel.Cut.URL != "https://github.com/run/2" {
		t.Fatalf("after the run appeared: step=%q cut=%+v", rel.Step, rel.Cut)
	}
	if err := fx.svc.ApproveChangelog(ctx, actor, relID, testHead); err != nil {
		t.Fatalf("approve after the cut: %v", err)
	}
}

func TestActionPendingCutExpires(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog()
	ctx := context.Background()
	if err := fx.svc.Recut(ctx, actor, relID, testHead); err != nil {
		t.Fatal(err)
	}
	fx.clock.advance(recentCutTTL - time.Second)
	fx.svc.Invalidate()
	if rel := fx.oneRelease(t); rel.Step != StepCutting {
		t.Fatalf("just before expiry: step = %q", rel.Step)
	}
	fx.clock.advance(time.Second)
	fx.svc.Invalidate()
	if rel := fx.oneRelease(t); rel.Step != StepChangelog || rel.Cut == nil || rel.Cut.State != StateSuccess {
		t.Fatalf("after expiry: step=%q cut=%+v", rel.Step, rel.Cut)
	}
	if err := fx.svc.ApproveChangelog(ctx, actor, relID, testHead); err != nil {
		t.Fatalf("approve after expiry: %v", err)
	}
}

func TestActionCutListedWhilePending(t *testing.T) {
	fx := newFixture(t)
	fx.withBuiltUnits()
	ctx := context.Background()
	id, err := fx.svc.Cut(ctx, actor, []string{"ppg:staging:18"})
	if err != nil {
		t.Fatal(err)
	}
	rel := fx.oneRelease(t)
	if rel.ID != id || rel.Step != StepCutting || rel.Cut == nil || rel.Cut.State != StateQueued || rel.Cut.URL != "" || len(rel.Units) != 0 {
		t.Fatalf("release = %+v", rel)
	}
	if rel.StartedBy != actor {
		t.Errorf("StartedBy = %q", rel.StartedBy)
	}
	rows, err := fx.svc.Units(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Tickable || rows[0].Blocked == nil || rows[0].Blocked.Reason != "in_flight" || rows[0].Blocked.ReleaseID != id {
		t.Errorf("cut unit = %+v blocked = %+v", rows[0], rows[0].Blocked)
	}
	if !rows[1].Tickable {
		t.Errorf("other unit = %+v blocked = %+v", rows[1], rows[1].Blocked)
	}
	if err := fx.svc.Abandon(ctx, actor, id, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("abandon while pending: err = %v", err)
	}
}

// qaFailedToStart sets up a release whose QA run ended before any lane.
func (fx *svcFixture) qaFailedToStart() {
	fx.atChangelog(LabelSync, LabelQA)
	fx.withPRRun("completed", "success", populateJob(100, "obs", "completed", "success"))
	fx.gh.runs = append(fx.gh.runs, Run{
		ID: 11, Name: "Release PR", WorkflowFile: WorkflowPR, HeadSHA: testHead, Status: "completed", Conclusion: "failure",
		HTMLURL: "https://github.com/run/11", CreatedAt: fx.clock.now().Add(-10 * time.Minute),
	})
	fx.gh.jobs[11] = []Job{{ID: 300, Name: "Release / Detect QA matrix (obs)", Status: "completed", Conclusion: "failure"}}
}

func labelCalls(fx *svcFixture) []string {
	var out []string
	for _, c := range fx.gh.allCalls() {
		if strings.HasPrefix(c, "RemoveLabel") || strings.HasPrefix(c, "AddLabel") {
			out = append(out, c)
		}
	}
	return out
}

func TestActionRetryQA(t *testing.T) {
	fx := newFixture(t)
	fx.qaFailedToStart()
	ctx := context.Background()
	rel := fx.oneRelease(t)
	if rel.Step != StepQA || rel.QA == nil || rel.QA.State != "failure" || rel.QA.Error != "QA could not start." || rel.QA.URL != "https://github.com/run/11" {
		t.Fatalf("release: step=%q qa=%+v", rel.Step, rel.QA)
	}
	if err := fx.svc.Release(ctx, actor, relID, testHead, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("release with failed QA start: err = %v", err)
	}
	if err := fx.svc.RetryQA(ctx, actor, relID, testHead); err != nil {
		t.Fatal(err)
	}
	if got := labelCalls(fx); len(got) != 2 || got[0] != "RemoveLabel 415 qa-release" || got[1] != "AddLabel 415 qa-release" {
		t.Errorf("label calls = %v", got)
	}
	fx.wantOneAudit(t, "retry_qa")

	// The same retry again within a minute is refused.
	if err := fx.svc.RetryQA(ctx, actor, relID, testHead); !errors.Is(err, ErrConflict) {
		t.Fatalf("second retry: err = %v", err)
	}
	if n := len(labelCalls(fx)); n != 2 {
		t.Errorf("label calls after the refused retry = %d", n)
	}
	fx.clock.advance(61 * time.Second)
	if err := fx.svc.RetryQA(ctx, actor, relID, testHead); err != nil {
		t.Fatalf("retry after a minute: %v", err)
	}
}

func TestActionRetryQAOnlyWhenFailed(t *testing.T) {
	fx := newFixture(t)
	fx.atQA(true, "in_progress", Job{ID: 200, Name: JobLanePrefix + "packages / pg_tde", Status: "in_progress"})
	if err := fx.svc.RetryQA(context.Background(), actor, relID, testHead); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	fx.wantNoWrites(t)
}

func TestActionRetrySyncCooldown(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog(LabelSync)
	fx.withPRRun("completed", "failure", populateJob(100, "obs", "completed", "failure"))
	ctx := context.Background()
	if err := fx.svc.RetrySync(ctx, actor, relID, testHead); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.RetrySync(ctx, actor, relID, testHead); !errors.Is(err, ErrConflict) {
		t.Fatalf("second retry: err = %v", err)
	}
	if n := len(labelCalls(fx)); n != 2 {
		t.Errorf("label calls = %d, want 2", n)
	}
	fx.clock.advance(61 * time.Second)
	if err := fx.svc.RetrySync(ctx, actor, relID, testHead); err != nil {
		t.Fatalf("retry after a minute: %v", err)
	}
}

func TestActionRetryReleaseCooldown(t *testing.T) {
	fx := newFixture(t)
	fx.atReleasing("failure")
	ctx := context.Background()
	if err := fx.svc.RetryRelease(ctx, actor, relID); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.RetryRelease(ctx, actor, relID); !errors.Is(err, ErrConflict) {
		t.Fatalf("second retry: err = %v", err)
	}
	if n := len(fx.gh.callsOf("Dispatch")); n != 1 {
		t.Errorf("Dispatch called %d times, want 1", n)
	}
}

func TestActionRetrySyncWaitsForTheRun(t *testing.T) {
	fx := newFixture(t)
	fx.atChangelog(LabelSync)
	fx.withPRRun("in_progress", "", populateJob(100, "obs", "completed", "failure"), populateJob(101, "labs", "in_progress", ""))
	if rel := fx.oneRelease(t); rel.SyncState != string(StateFailure) {
		t.Fatalf("SyncState = %q", rel.SyncState)
	}
	if err := fx.svc.RetrySync(context.Background(), actor, relID, testHead); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	fx.wantNoWrites(t)
}

func TestActionRetryReleaseWaitsForTheRun(t *testing.T) {
	fx := newFixture(t)
	fx.atReleasing("failure")
	last := len(fx.gh.runs) - 1
	fx.gh.runs[last].Status, fx.gh.runs[last].Conclusion = "in_progress", ""
	fx.gh.jobs[20] = []Job{
		{ID: 400, Name: JobCopyPrefix + "obs)", Status: "completed", Conclusion: "failure"},
		{ID: 401, Name: JobCopyPrefix + "labs)", Status: "in_progress"},
	}
	if rel := fx.oneRelease(t); rel.Final == nil || rel.Final.State != StateFailure {
		t.Fatalf("Final = %+v", rel.Final)
	}
	if err := fx.svc.RetryRelease(context.Background(), actor, relID); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	fx.wantNoWrites(t)
}

func TestActionAbandonFailedFirstCutRefused(t *testing.T) {
	fx := newFixture(t)
	fx.withCutRun("completed", "failure")
	if rel := fx.oneRelease(t); rel.PR != 0 || rel.HeadSHA != "" || rel.Cut == nil || rel.Cut.State != StateFailure {
		t.Fatalf("release = %+v", rel)
	}
	if err := fx.svc.Abandon(context.Background(), actor, relID, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	fx.wantNoWrites(t)
}

func TestActionSkipQA(t *testing.T) {
	ctx, id := context.Background(), "20261111T143210"
	fx := newFixture(t)
	fx.atQA(false, "completed")
	// Not released before the skip.
	if err := fx.svc.Release(ctx, actor, id, testHead, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("release before the skip: err = %v", err)
	}
	if err := fx.svc.SkipQA(ctx, actor, id, testHead); err != nil {
		t.Fatal(err)
	}
	got := fx.gh.callsOf("Comment")
	if len(got) != 1 || !strings.HasPrefix(got[0], "Comment 415 ") || !strings.Contains(got[0], "QA skipped") || !strings.Contains(got[0], testHead) {
		t.Errorf("Comment = %v", got)
	}
	if strings.Contains(got[0], actor) {
		t.Errorf("the comment names the user: %q", got[0])
	}
	if a := fx.wantOneAudit(t, "skip_qa"); a.Detail != testHead {
		t.Errorf("audit = %+v", a)
	}
	rel := fx.snapshot(t).Releases[0]
	if rel.QA == nil || !rel.QA.Skipped || rel.QA.SkippedBy != actor || rel.QA.State != "waiting" {
		t.Fatalf("qa = %+v", rel.QA)
	}
	// A second skip is refused, and leaves no second comment.
	if err := fx.svc.SkipQA(ctx, actor, id, testHead); !errors.Is(err, ErrConflict) {
		t.Fatalf("second skip: err = %v", err)
	}
	if n := len(fx.gh.callsOf("Comment")); n != 1 {
		t.Fatalf("Comment called %d times", n)
	}
	if err := fx.svc.Release(ctx, actor, id, testHead, false); err != nil {
		t.Fatalf("release after the skip: %v", err)
	}
	if got := fx.gh.callsOf("MergePull"); len(got) != 1 || got[0] != "MergePull 415 "+testHead {
		t.Errorf("MergePull = %v", got)
	}
}

func TestActionSkipQAWhileRunningStillNeedsConfirmForFailedLanes(t *testing.T) {
	ctx, id := context.Background(), "20261111T143210"
	fx := newFixture(t)
	fx.atQA(true, "in_progress", laneJob(200, "packages / pg_tde", "failure"), Job{ID: 201, Name: JobLanePrefix + "packages / pg_stat", Status: "in_progress"})
	if err := fx.svc.SkipQA(ctx, actor, id, testHead); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Release(ctx, actor, id, testHead, false); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v", err)
	}
	if err := fx.svc.Release(ctx, actor, id, testHead, true); err != nil {
		t.Fatal(err)
	}
}

func TestActionSkipQARefused(t *testing.T) {
	ctx, id := context.Background(), "20261111T143210"
	// QA finished: Release is available already.
	fx := newFixture(t)
	fx.atQA(true, "completed", laneJob(200, "packages / pg_tde", "success"))
	if err := fx.svc.SkipQA(ctx, actor, id, testHead); !errors.Is(err, ErrConflict) {
		t.Fatalf("finished QA: err = %v", err)
	}
	fx.wantNoWrites(t)
	// Not at the QA step.
	fx = newFixture(t)
	fx.atChangelog()
	if err := fx.svc.SkipQA(ctx, actor, id, testHead); !errors.Is(err, ErrConflict) {
		t.Fatalf("changelog step: err = %v", err)
	}
	fx.wantNoWrites(t)
	// A head the user did not see.
	fx = newFixture(t)
	fx.atQA(false, "completed")
	if err := fx.svc.SkipQA(ctx, actor, id, "otherhead"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale head: err = %v", err)
	}
	fx.wantNoWrites(t)
	// The comment could not be left: nothing is skipped.
	fx.gh.commentErr = errors.New("boom")
	if err := fx.svc.SkipQA(ctx, actor, id, testHead); err == nil {
		t.Fatal("skipped although the comment failed")
	}
	if rel := fx.snapshot(t).Releases[0]; rel.QA.Skipped {
		t.Fatal("skipped although the comment failed")
	}
}

func TestSkipQADoesNotCoverANewHead(t *testing.T) {
	fx := newFixture(t)
	fx.atQA(false, "completed")
	// The skip of an earlier iteration of the PR.
	fx.recordCut(t, "skip_qa", "oldhead")
	if rel := fx.snapshot(t).Releases[0]; rel.QA.Skipped {
		t.Fatal("a skip of another head counts")
	}
	if err := fx.svc.Release(context.Background(), actor, "20261111T143210", testHead, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
}

func TestActionRerunQAOneLane(t *testing.T) {
	ctx, id := context.Background(), "20261111T143210"
	fx := newFixture(t)
	fx.atQA(true, "completed", laneJob(200, "packages / pg_tde", "failure"), laneJob(201, "packages / pg_stat", "failure"), laneJob(202, "packages / pg_ok", "success"))
	if err := fx.svc.RerunQA(ctx, actor, id, testHead, "packages / pg_tde"); err != nil {
		t.Fatal(err)
	}
	if got := fx.gh.callsOf("RerunJob"); len(got) != 1 || got[0] != "RerunJob 200" {
		t.Errorf("RerunJob = %v", got)
	}
	if n := len(fx.gh.callsOf("RerunFailedJobs")); n != 0 {
		t.Errorf("RerunFailedJobs called %d times", n)
	}
	if a := fx.wantOneAudit(t, "rerun_qa"); a.Detail != JobLanePrefix+"packages / pg_tde" {
		t.Errorf("audit = %+v", a)
	}
	// The run is going again: the lane is not failed any more, the others keep
	// their result, and nothing can be run again until it finishes.
	qa := fx.snapshot(t).Releases[0].QA
	if qa.State != "running" || qa.Lanes[0].State != StateQueued || qa.Lanes[1].State != StateFailure || qa.Lanes[2].State != StateSuccess {
		t.Fatalf("qa = %+v", qa)
	}
	fx.clock.advance(61 * time.Second)
	if err := fx.svc.RerunQA(ctx, actor, id, testHead, "packages / pg_stat"); !errors.Is(err, ErrConflict) {
		t.Fatalf("rerun while running: err = %v", err)
	}
}

func TestActionRerunQAAllFailed(t *testing.T) {
	ctx, id := context.Background(), "20261111T143210"
	fx := newFixture(t)
	fx.atQA(true, "completed", laneJob(200, "packages / pg_tde", "failure"), laneJob(201, "packages / pg_stat", "failure"), laneJob(202, "packages / pg_ok", "success"))
	if err := fx.svc.RerunQA(ctx, actor, id, testHead, ""); err != nil {
		t.Fatal(err)
	}
	if got := fx.gh.callsOf("RerunFailedJobs"); len(got) != 1 || !strings.HasPrefix(got[0], "RerunFailedJobs ") {
		t.Errorf("RerunFailedJobs = %v", got)
	}
	if n := len(fx.gh.callsOf("RerunJob")); n != 0 {
		t.Errorf("RerunJob called %d times", n)
	}
	want := JobLanePrefix + "packages / pg_tde\n" + JobLanePrefix + "packages / pg_stat"
	if a := fx.wantOneAudit(t, "rerun_qa"); a.Detail != want {
		t.Errorf("audit = %+v", a)
	}
	qa := fx.snapshot(t).Releases[0].QA
	if qa.State != "running" || qa.Lanes[0].State != StateQueued || qa.Lanes[1].State != StateQueued || qa.Lanes[2].State != StateSuccess {
		t.Fatalf("qa = %+v", qa)
	}
}

func TestActionRerunQARefused(t *testing.T) {
	ctx, id := context.Background(), "20261111T143210"
	failedLane, passedLane := laneJob(200, "packages / pg_tde", "failure"), laneJob(201, "packages / pg_stat", "success")
	cases := []struct {
		name  string
		setup func(fx *svcFixture)
		head  string
		lane  string
		want  error
	}{
		{"QA still running", func(fx *svcFixture) {
			fx.atQA(true, "in_progress", failedLane, Job{ID: 201, Name: JobLanePrefix + "packages / pg_stat", Status: "in_progress"})
		}, testHead, "packages / pg_tde", ErrConflict},
		{"QA not started", func(fx *svcFixture) { fx.atQA(false, "completed") }, testHead, "", ErrConflict},
		{"stale head", func(fx *svcFixture) { fx.atQA(true, "completed", failedLane, passedLane) }, "oldhead", "", ErrConflict},
		{"lane passed", func(fx *svcFixture) { fx.atQA(true, "completed", failedLane, passedLane) }, testHead, "packages / pg_stat", ErrBadRequest},
		{"unknown lane", func(fx *svcFixture) { fx.atQA(true, "completed", failedLane, passedLane) }, testHead, "nope", ErrBadRequest},
		{"nothing failed", func(fx *svcFixture) { fx.atQA(true, "completed", passedLane) }, testHead, "", ErrBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t)
			c.setup(fx)
			if err := fx.svc.RerunQA(ctx, actor, id, c.head, c.lane); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			fx.wantNoWrites(t)
		})
	}
}

func TestActionRerunQAGitHubRefuses(t *testing.T) {
	ctx, id := context.Background(), "20261111T143210"
	fx := newFixture(t)
	fx.atQA(true, "completed", laneJob(200, "packages / pg_tde", "failure"))
	fx.gh.rerunErr = errors.New("boom")
	if err := fx.svc.RerunQA(ctx, actor, id, testHead, ""); err == nil {
		t.Fatal("no error")
	}
	if rows := fx.auditRows(t); len(rows) != 0 {
		t.Errorf("audit rows: %+v", rows)
	}
	// A refused re-run starts no cooldown.
	fx.gh.rerunErr = nil
	if err := fx.svc.RerunQA(ctx, actor, id, testHead, ""); err != nil {
		t.Fatal(err)
	}
	// An accepted one does, even if GitHub still showed the run as finished.
	if err := fx.svc.RerunQA(ctx, actor, id, testHead, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("second rerun: err = %v", err)
	}
}
