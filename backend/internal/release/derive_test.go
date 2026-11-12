package release

import (
	"testing"
	"time"
)

func mustTime() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

func runOK(name, sha string) Run {
	return Run{Name: name, HeadSHA: sha, Status: "completed", Conclusion: "success"}
}

func TestDeriveStep(t *testing.T) {
	merged := func(p Pull) *Pull { tm := p; t0 := mustTime(); tm.MergedAt = &t0; return &tm }
	open := func(labels ...string) *Pull {
		return &Pull{Number: 4, HeadRef: "release/a", HeadSHA: "new", Labels: labels, State: "open"}
	}
	syncJobs := func(concl string) (Run, map[int64][]Job) {
		r := Run{ID: 1, Name: "x", WorkflowFile: WorkflowPR, HeadSHA: "new", Status: "completed", Conclusion: concl}
		return r, map[int64][]Job{1: {{Name: "Populate pre-release (isv)", Status: "completed", Conclusion: concl}}}
	}
	okRun, okJobs := syncJobs("success")
	rel := func(concl string) *Run { return &Run{Status: "completed", Conclusion: concl} }
	cutRunning := &Run{Name: "Cut release/a", Status: "in_progress"}
	cutQueued := &Run{Name: "Cut release/a", Status: "queued"}
	cutDone := &Run{Name: "Cut release/a", Status: "completed", Conclusion: "success"}
	tests := []struct {
		name string
		f    Facts
		want Step
	}{
		{"no pr", Facts{}, StepCutting},
		{"merged success", Facts{Pull: merged(Pull{}), ReleaseRun: rel("success")}, StepDone},
		{"merged failed", Facts{Pull: merged(Pull{}), ReleaseRun: rel("failure")}, StepReleasing},
		{"merged no run", Facts{Pull: merged(Pull{})}, StepReleasing},
		{"cut running", Facts{Pull: open(), CutRun: cutRunning}, StepCutting},
		{"cut queued", Facts{Pull: open(LabelSync), CutRun: cutQueued}, StepCutting},
		{"no label", Facts{Pull: open(), CutRun: cutDone}, StepChangelog},
		{"label sync not done", Facts{Pull: open(LabelSync)}, StepSync},
		{"label sync ok", Facts{Pull: open(LabelSync), HeadRuns: []Run{okRun}, Jobs: okJobs}, StepQA},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveStep(tc.f); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestDeriveIgnoresRunsForOldHead(t *testing.T) {
	f := Facts{
		Pull:     &Pull{HeadSHA: "new", Labels: []string{LabelSync}},
		HeadRuns: []Run{{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "old", Status: "completed", Conclusion: "success"}},
		Jobs:     map[int64][]Job{1: {{Name: "Populate pre-release (isv)", Status: "completed", Conclusion: "success"}}},
	}
	if got := DeriveStep(f); got != StepSync {
		t.Fatalf("step %s", got)
	}
	st, cards, run := SyncCards(f)
	if st != "queued" || run != nil || len(cards) != 0 {
		t.Fatalf("%s %v %v", st, cards, run)
	}
}

func TestFindRunExact(t *testing.T) {
	runs := []Run{{Name: "Cut release/a2"}, {Name: "Release PR #41"}}
	if FindRun(runs, CutRunName("release/a")) != nil || FindRun(runs, ReleaseRunName(4)) != nil {
		t.Fatal("prefix match")
	}
	if FindRun(runs, "Release PR #41") == nil {
		t.Fatal("exact match missing")
	}
}

func TestSyncCardsPerInstance(t *testing.T) {
	f := Facts{
		Pull:     &Pull{HeadSHA: "h"},
		HeadRuns: []Run{{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "completed", Conclusion: "failure"}},
		Jobs: map[int64][]Job{1: {
			{Name: "Populate pre-release (isv)", Status: "completed", Conclusion: "success", HTMLURL: "u1"},
			{Name: "Populate pre-release (labs)", Status: "completed", Conclusion: "failure"},
			{Name: "Wait for download CDN (isv)", Status: "completed", Conclusion: "skipped"},
		}},
	}
	st, cards, _ := SyncCards(f)
	if st != "failure" || len(cards) != 2 {
		t.Fatalf("%s %v", st, cards)
	}
	if cards[0].Instance != "isv" || cards[0].State != "success" || cards[0].CDN != "skipped" || cards[0].URL != "u1" {
		t.Fatalf("%+v", cards[0])
	}
	if cards[1].Instance != "labs" || cards[1].State != "failure" || cards[1].CDN != "" {
		t.Fatalf("%+v", cards[1])
	}
	if cards[0].Units == nil || cards[0].Errors == nil {
		t.Fatal("nil slices")
	}
}

func TestSyncRunSkippedPopulateDoesNotCount(t *testing.T) {
	f := Facts{
		Pull: &Pull{HeadSHA: "h"},
		HeadRuns: []Run{
			{ID: 2, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "completed", Conclusion: "success", CreatedAt: mustTime().Add(time.Hour)},
			{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "completed", Conclusion: "success", CreatedAt: mustTime()},
		},
		Jobs: map[int64][]Job{
			2: {{Name: "Populate pre-release (isv)", Status: "completed", Conclusion: "skipped"}, {Name: "Release / OBS QA / packages / x", Status: "completed", Conclusion: "success"}},
			1: {{Name: "Populate pre-release (isv)", Status: "completed", Conclusion: "success"}},
		},
	}
	_, _, run := SyncCards(f)
	if run == nil || run.ID != 1 {
		t.Fatalf("%+v", run)
	}
	q := QALanes(f, nil, true)
	if len(q.Lanes) != 1 || q.State != "finished" {
		t.Fatalf("%+v", q)
	}
}

func qaFacts() Facts {
	return Facts{
		Pull:     &Pull{HeadSHA: "h"},
		HeadRuns: []Run{{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "completed", Conclusion: "failure", HTMLURL: "run"}},
		Jobs: map[int64][]Job{1: {
			{ID: 10, Name: "Release / OBS QA / packages / pg_tde", Status: "completed", Conclusion: "failure", HTMLURL: "j10"},
			{ID: 11, Name: "Release / OBS QA / containers / ubi9", Status: "completed", Conclusion: "success", HTMLURL: "j11"},
			{ID: 12, Name: "Release / Detect QA matrix (isv)", Status: "completed", Conclusion: "success"},
		}},
		Annotations: map[int64][]Annotation{10: {{Level: "notice", Title: "Jenkins build", Message: "ppg:releases:18 https://j/job/1/"}}},
	}
}

func TestQALanes(t *testing.T) {
	units := []UnitState{{Unit: "ppg:staging:18", ReleaseProject: "ppg:releases:18"}}
	q := QALanes(qaFacts(), units, true)
	if len(q.Lanes) != 2 {
		t.Fatalf("%+v", q)
	}
	a, b := q.Lanes[0], q.Lanes[1]
	if a.Unit != "ppg:staging:18" || a.JenkinsURL != "https://j/job/1/" || a.State != StateFailure || a.URL != "j10" {
		t.Fatalf("%+v", a)
	}
	if b.Unit != "" || b.JenkinsURL != "" || b.State != StateSuccess {
		t.Fatalf("%+v", b)
	}
	if w := QALanes(qaFacts(), units, false); w.State != "waiting" {
		t.Fatalf("%+v", w)
	}
}

func TestQALaneUnitPrefixBoundary(t *testing.T) {
	units := []UnitState{
		{Unit: "a", ReleaseProject: "ppg:releases:18"},
		{Unit: "b", ReleaseProject: "ppg:releases:18:extras"},
	}
	f := qaFacts()
	f.Annotations[10][0].Message = "ppg:releases:18:extras:containers https://j/2"
	if l := QALanes(f, units, true).Lanes[0]; l.Unit != "b" {
		t.Fatalf("%+v", l)
	}
	f.Annotations[10][0].Message = "ppg:releases:180 https://j/3"
	if l := QALanes(f, units, true).Lanes[0]; l.Unit != "" {
		t.Fatalf("%+v", l)
	}
}

func TestFinalStatus(t *testing.T) {
	f := Facts{
		Pull:       &Pull{},
		ReleaseRun: &Run{Status: "in_progress", HTMLURL: "r"},
		ReleaseJobs: []Job{
			{Name: "Release to OBS (isv)", Status: "in_progress"},
			{Name: JobFinalize, Status: "queued"},
		},
	}
	fin := FinalStatus(f)
	if fin.State != StateRunning || len(fin.Instances) != 1 || fin.Instances[0].State != StateRunning || fin.GitHubReleases != "queued" || fin.URL != "r" {
		t.Fatalf("%+v", fin)
	}
	f.ReleaseRun = &Run{Status: "completed", Conclusion: "success"}
	f.ReleaseJobs = []Job{
		{Name: "Release to OBS (isv)", Status: "completed", Conclusion: "success"},
		{Name: JobFinalize, Status: "completed", Conclusion: "success"},
		{Name: "Delete the pre-release (isv)", Status: "completed", Conclusion: "success"},
	}
	fin = FinalStatus(f)
	if fin.State != StateSuccess || fin.Cleanup != "success" {
		t.Fatalf("%+v", fin)
	}
	if FinalStatus(Facts{}) != nil {
		t.Fatal("expected nil without release run")
	}
}

func syncFacts(jobs ...Job) Facts {
	return Facts{
		Pull:     &Pull{HeadSHA: "h", Labels: []string{LabelSync}},
		HeadRuns: []Run{{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "in_progress"}},
		Jobs:     map[int64][]Job{1: jobs},
	}
}

func pj(inst, status, concl string) Job {
	return Job{Name: "Populate pre-release (" + inst + ")", Status: status, Conclusion: concl}
}

func cj(inst, status, concl string) Job {
	return Job{Name: "Wait for download CDN (" + inst + ")", Status: status, Conclusion: concl}
}

func TestSyncCardsGating(t *testing.T) {
	tests := []struct {
		name string
		jobs []Job
		want []string // acceptable states
	}{
		{"populate running", []Job{pj("isv", "in_progress", "")}, []string{"running"}},
		{"populate queued", []Job{pj("isv", "queued", "")}, []string{"running", "queued"}},
		{"populate cancelled", []Job{pj("isv", "completed", "cancelled")}, []string{"failure"}},
		{"one populate running", []Job{pj("isv", "completed", "success"), pj("labs", "in_progress", "")}, []string{"running"}},
		{"cdn queued", []Job{pj("isv", "completed", "success"), cj("isv", "queued", "")}, []string{"running", "queued"}},
		{"cdn running", []Job{pj("isv", "completed", "success"), cj("isv", "in_progress", "")}, []string{"running"}},
		{"cdn failure", []Job{pj("isv", "completed", "success"), cj("isv", "completed", "failure")}, []string{"failure"}},
		{"cdn success", []Job{pj("isv", "completed", "success"), cj("isv", "completed", "success")}, []string{"success"}},
		{"cdn skipped", []Job{pj("isv", "completed", "success"), cj("isv", "completed", "skipped")}, []string{"success"}},
		{"mixed cdn", []Job{pj("isv", "completed", "success"), pj("labs", "completed", "success"), cj("isv", "completed", "success"), cj("labs", "completed", "skipped")}, []string{"success"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, _, _ := SyncCards(syncFacts(tc.jobs...))
			ok := false
			for _, w := range tc.want {
				ok = ok || st == w
			}
			if !ok {
				t.Fatalf("got %s want one of %v", st, tc.want)
			}
		})
	}
}

func TestDeriveStepSyncWithCDN(t *testing.T) {
	f := syncFacts(pj("isv", "completed", "success"), cj("isv", "in_progress", ""))
	if got := DeriveStep(f); got != StepSync {
		t.Fatalf("cdn running: %s", got)
	}
	f = syncFacts(pj("isv", "completed", "success"), cj("isv", "completed", "success"))
	if got := DeriveStep(f); got != StepQA {
		t.Fatalf("cdn success: %s", got)
	}
}

func TestQALanesSkippedAndCancelled(t *testing.T) {
	f := qaFacts()
	f.Jobs[1] = []Job{
		{ID: 10, Name: "Release / OBS QA / packages / a", Status: "completed", Conclusion: "skipped"},
		{ID: 11, Name: "Release / OBS QA / packages / b", Status: "completed", Conclusion: "cancelled"},
	}
	q := QALanes(f, nil, true)
	if len(q.Lanes) != 1 || q.Lanes[0].Name != "packages / b" || q.Lanes[0].State != StateFailure {
		t.Fatalf("%+v", q)
	}
}

func TestFinalStatusSkippedJobs(t *testing.T) {
	f := Facts{
		Pull:       &Pull{},
		ReleaseRun: &Run{Status: "completed", Conclusion: "success"},
		ReleaseJobs: []Job{
			{Name: "Release to OBS (isv)", Status: "completed", Conclusion: "success"},
			{Name: "Release to OBS (labs)", Status: "completed", Conclusion: "skipped"},
			{Name: JobFinalize, Status: "completed", Conclusion: "skipped"},
			{Name: "Delete the pre-release (isv)", Status: "completed", Conclusion: "skipped"},
			{Name: "Delete the pre-release (labs)", Status: "completed", Conclusion: "skipped"},
		},
	}
	fin := FinalStatus(f)
	if len(fin.Instances) != 1 || fin.Instances[0].Instance != "isv" {
		t.Fatalf("instances %+v", fin.Instances)
	}
	if fin.GitHubReleases != "skipped" || fin.Cleanup != "skipped" || fin.State != StateSuccess {
		t.Fatalf("%+v", fin)
	}
	f.ReleaseRun = &Run{Status: "in_progress"}
	if s := FinalStatus(f).State; s != StateRunning {
		t.Fatalf("in progress: %s", s)
	}
	f.ReleaseRun = &Run{Status: "queued"}
	if s := FinalStatus(f).State; s != StateQueued {
		t.Fatalf("queued: %s", s)
	}
	f.ReleaseRun = &Run{Status: "completed", Conclusion: "cancelled"}
	if s := FinalStatus(f).State; s != StateFailure {
		t.Fatalf("cancelled: %s", s)
	}
	f.ReleaseRun = &Run{Status: "in_progress"}
	f.ReleaseJobs = []Job{{Name: "Release to OBS (isv)", Status: "completed", Conclusion: "failure"}}
	if s := FinalStatus(f).State; s != StateFailure {
		t.Fatalf("copy failed: %s", s)
	}
}

func TestDeriveEmptyHeadSHA(t *testing.T) {
	f := Facts{
		Pull:     &Pull{HeadSHA: "", Labels: []string{LabelSync}},
		HeadRuns: []Run{{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "", Status: "completed", Conclusion: "success"}},
		Jobs:     map[int64][]Job{1: {pj("isv", "completed", "success")}},
	}
	if _, _, run := SyncCards(f); run != nil {
		t.Fatalf("sync run found %+v", run)
	}
	if got := DeriveStep(f); got != StepSync {
		t.Fatalf("step %s", got)
	}
}

func TestQALaneAnnotationTrimmed(t *testing.T) {
	f := qaFacts()
	f.Annotations[10][0].Message = "ppg:releases:18 https://j/job/1/\n"
	units := []UnitState{{Unit: "u", ReleaseProject: "ppg:releases:18"}}
	l := QALanes(f, units, true).Lanes[0]
	if l.JenkinsURL != "https://j/job/1/" || l.Unit != "u" {
		t.Fatalf("%+v", l)
	}
}

func TestSyncFailsBeforeAnyPopulateJob(t *testing.T) {
	mk := func(status, concl string) Facts {
		return Facts{
			Pull:     &Pull{HeadSHA: "h", Labels: []string{LabelSync}},
			HeadRuns: []Run{{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: status, Conclusion: concl, HTMLURL: "run1"}},
			Jobs:     map[int64][]Job{1: {{Name: "Resolve the release", Status: status, Conclusion: concl}}},
		}
	}
	st, cards, run := SyncCards(mk("completed", "failure"))
	if st != string(StateFailure) || len(cards) != 0 || run == nil || run.HTMLURL != "run1" {
		t.Fatalf("failed run: state=%q cards=%v run=%+v", st, cards, run)
	}
	if got := DeriveStep(mk("completed", "failure")); got != StepSync {
		t.Fatalf("step = %s", got)
	}
	for _, c := range [][2]string{{"in_progress", ""}, {"queued", ""}, {"completed", "success"}, {"completed", "skipped"}} {
		if st, _, run := SyncCards(mk(c[0], c[1])); st != string(StateQueued) || run != nil {
			t.Errorf("%v: state=%q run=%+v, want queued", c, st, run)
		}
	}
}

func TestSyncFailureIgnoresQAOnlyRun(t *testing.T) {
	f := Facts{
		Pull: &Pull{HeadSHA: "h", Labels: []string{LabelSync}},
		HeadRuns: []Run{
			{ID: 2, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "completed", Conclusion: "failure", CreatedAt: mustTime().Add(time.Hour)},
			{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "in_progress", CreatedAt: mustTime()},
		},
		Jobs: map[int64][]Job{
			2: {cj("isv", "completed", "failure")},
			1: {{Name: "Resolve the release", Status: "in_progress"}},
		},
	}
	if st, _, _ := SyncCards(f); st != string(StateQueued) {
		t.Fatalf("state = %q, want queued: the failed run is a QA-only run", st)
	}
}

// qaStartFacts is a finished sync run (1) followed by the run the qa-release
// label started (2), with the given state and jobs.
func qaStartFacts(status, concl string, jobs ...Job) Facts {
	return Facts{
		Pull: &Pull{HeadSHA: "h", Labels: []string{LabelSync, LabelQA}},
		HeadRuns: []Run{
			{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "completed", Conclusion: "success", HTMLURL: "run1", CreatedAt: mustTime()},
			{ID: 2, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: status, Conclusion: concl, HTMLURL: "run2", CreatedAt: mustTime().Add(time.Hour)},
		},
		Jobs: map[int64][]Job{
			1: {pj("isv", "completed", "success")},
			2: jobs,
		},
	}
}

func TestQAFailsToStart(t *testing.T) {
	// Matrix detection failed: no CDN job, no lane.
	f := qaStartFacts("completed", "failure", Job{Name: "Release / Detect QA matrix (isv)", Status: "completed", Conclusion: "failure"})
	q := QALanes(f, nil, true)
	if q.State != "failure" || q.Error != "QA could not start." || q.URL != "run2" || len(q.Lanes) != 0 {
		t.Fatalf("no lanes: %+v", q)
	}
	if st, _, run := SyncCards(f); st != string(StateSuccess) || run == nil || run.ID != 1 {
		t.Fatalf("sync = %q %+v", st, run)
	}
	if got := DeriveStep(f); got != StepQA {
		t.Fatalf("step = %s", got)
	}

	// The CDN wait failed.
	f = qaStartFacts("completed", "failure", cj("isv", "completed", "failure"))
	q = QALanes(f, nil, true)
	if q.State != "failure" || q.Error != "The download servers did not serve the pre-release in time." || q.URL != "run2" {
		t.Fatalf("cdn failed: %+v", q)
	}
	// The CDN jobs of the QA run do not hold the sync back.
	if st, _, _ := SyncCards(f); st != string(StateSuccess) {
		t.Fatalf("sync with a failed QA-run CDN job = %q", st)
	}

	// Still waiting for the CDN: running, with the run to link to.
	f = qaStartFacts("in_progress", "", cj("isv", "in_progress", ""))
	q = QALanes(f, nil, true)
	if q.State != "running" || q.Error != "" || q.URL != "run2" {
		t.Fatalf("cdn running: %+v", q)
	}

	// The run has not created a job yet.
	f = qaStartFacts("queued", "")
	if q = QALanes(f, nil, true); q.State != "running" || q.URL != "run2" {
		t.Fatalf("queued: %+v", q)
	}

	// A later run that did nothing (another label event) is not a failure.
	f = qaStartFacts("completed", "success")
	if q = QALanes(f, nil, true); q.State != "running" {
		t.Fatalf("no-op run: %+v", q)
	}

	// Lanes ran: finished, whatever the conclusion.
	f = qaStartFacts("completed", "failure", cj("isv", "completed", "success"),
		Job{ID: 10, Name: JobLanePrefix + "packages / a", Status: "completed", Conclusion: "failure"})
	if q = QALanes(f, nil, true); q.State != "finished" || len(q.Lanes) != 1 || q.Error != "" {
		t.Fatalf("lanes: %+v", q)
	}
}

const unexpanded = "${{ matrix.instance.name }}"

func TestSyncCardsIgnoresUnexpandedSkippedJobs(t *testing.T) {
	f := syncFacts(pj(unexpanded, "completed", "skipped"), cj(unexpanded, "completed", "skipped"))
	f.HeadRuns[0].Status, f.HeadRuns[0].Conclusion = "completed", "success"
	st, cards, run := SyncCards(f)
	if st != string(StateQueued) || len(cards) != 0 || run != nil {
		t.Fatalf("skipped: state=%q cards=%+v run=%+v", st, cards, run)
	}
	if q := QARun(f); q != nil {
		t.Fatalf("QA run = %+v, want none", q)
	}

	// Whatever its state, a job with an unexpanded name is no instance.
	f = syncFacts(pj(unexpanded, "completed", "cancelled"), cj(unexpanded, "completed", "cancelled"))
	if st, cards, run := SyncCards(f); st != string(StateQueued) || len(cards) != 0 || run != nil {
		t.Fatalf("cancelled: state=%q cards=%+v run=%+v", st, cards, run)
	}

	// Next to a real instance the unexpanded jobs change nothing.
	f = syncFacts(pj("isv", "completed", "success"), pj(unexpanded, "completed", "skipped"), cj(unexpanded, "completed", "skipped"))
	if st, cards, _ := SyncCards(f); st != string(StateSuccess) || len(cards) != 1 || cards[0].Instance != "isv" {
		t.Fatalf("with a real instance: state=%q cards=%+v", st, cards)
	}

	fin := FinalStatus(Facts{
		ReleaseRun: &Run{Status: "in_progress"},
		ReleaseJobs: []Job{
			{Name: JobCopyPrefix + unexpanded + ")", Status: "completed", Conclusion: "cancelled"},
			{Name: JobDeletePrefix + unexpanded + ")", Status: "completed", Conclusion: "cancelled"},
		},
	})
	if len(fin.Instances) != 0 || fin.Cleanup != "" || fin.State != StateRunning {
		t.Fatalf("final = %+v", fin)
	}
}

func TestDeriveIgnoresActionRequiredRuns(t *testing.T) {
	// What a re-cut's force-push leaves on the new head: a run with no jobs.
	f := Facts{
		Pull:     &Pull{HeadSHA: "h", Labels: []string{LabelSync}},
		HeadRuns: []Run{{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "completed", Conclusion: "action_required", HTMLURL: "run1"}},
		Jobs:     map[int64][]Job{},
	}
	if st, cards, run := SyncCards(f); st != string(StateQueued) || len(cards) != 0 || run != nil {
		t.Fatalf("sync: state=%q cards=%+v run=%+v, want queued", st, cards, run)
	}
	if got := DeriveStep(f); got != StepSync {
		t.Fatalf("step = %s", got)
	}

	f = qaStartFacts("completed", "action_required")
	q := QALanes(f, nil, true)
	if q.State != "running" || q.Error != "" || q.URL != "" {
		t.Fatalf("qa = %+v, want running with no run", q)
	}
	if st, _, run := SyncCards(f); st != string(StateSuccess) || run == nil || run.ID != 1 {
		t.Fatalf("sync next to an action_required run = %q %+v", st, run)
	}
}

// The jobs of the real QA run of a unit that declares no QA lane: the wait
// for the download servers and the matrix detection succeed, the matrix is
// empty, and the only lane job is skipped under its unexpanded name.
func noLaneQAJobs() []Job {
	ok := func(id int64, name string) Job {
		return Job{ID: id, Name: name, Status: "completed", Conclusion: "success"}
	}
	skip := func(id int64, name string) Job {
		return Job{ID: id, Name: name, Status: "completed", Conclusion: "skipped"}
	}
	return []Job{
		ok(1, "Resolve release PR"),
		ok(2, "Wait for download CDN (boo)"),
		ok(3, "Wait for download CDN (labs)"),
		skip(4, "Populate pre-release (${{ matrix.instance.name }})"),
		ok(5, "Release / Detect QA matrix (boo)"),
		ok(6, "Release / Detect QA matrix (labs)"),
		ok(7, "Release / Merge QA matrix"),
		skip(8, "Release / matrix.status_context"),
	}
}

// QA that ran and found nothing to run has finished, with no lanes.
func TestQALanesFinishedWithNoLanes(t *testing.T) {
	f := Facts{
		Pull:     &Pull{HeadSHA: "h"},
		HeadRuns: []Run{{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "completed", Conclusion: "success", HTMLURL: "run"}},
		Jobs:     map[int64][]Job{1: noLaneQAJobs()},
	}
	q := QALanes(f, nil, true)
	if q.State != "finished" || len(q.Lanes) != 0 || q.Lanes == nil || q.Error != "" || q.URL != "run" {
		t.Fatalf("QA = %+v, want finished with no lanes", q)
	}
}

// While that run is still going, QA is running: the lanes may yet appear.
func TestQALanesNoLanesYetIsRunning(t *testing.T) {
	jobs := noLaneQAJobs()[:3]
	f := Facts{
		Pull:     &Pull{HeadSHA: "h"},
		HeadRuns: []Run{{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "in_progress", HTMLURL: "run"}},
		Jobs:     map[int64][]Job{1: jobs},
	}
	if q := QALanes(f, nil, true); q.State != "running" {
		t.Fatalf("QA = %+v, want running", q)
	}
}

// A successful run in which the QA fan-out never ran (only the wait for the
// download servers did) is not a finished QA.
func TestQALanesSuccessWithoutFanOutIsNotFinished(t *testing.T) {
	jobs := noLaneQAJobs()[:3]
	jobs = append(jobs, Job{ID: 9, Name: "Release", Status: "completed", Conclusion: "skipped"})
	f := Facts{
		Pull:     &Pull{HeadSHA: "h"},
		HeadRuns: []Run{{ID: 1, WorkflowFile: WorkflowPR, HeadSHA: "h", Status: "completed", Conclusion: "success", HTMLURL: "run"}},
		Jobs:     map[int64][]Job{1: jobs},
	}
	if q := QALanes(f, nil, true); q.State == "finished" {
		t.Fatalf("QA = %+v, must not be finished when the fan-out was skipped", q)
	}
}
