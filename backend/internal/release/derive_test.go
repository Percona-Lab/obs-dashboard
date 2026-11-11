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
	if cards[0].FailedUnits == nil || cards[0].Errors == nil {
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
