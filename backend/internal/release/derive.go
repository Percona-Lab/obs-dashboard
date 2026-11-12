package release

import (
	"strings"
)

// Facts are the GitHub observations the derivation functions work from.
type Facts struct {
	Pull        *Pull // nil while no PR exists
	CutRun      *Run  // newest run named exactly CutRunName(branch), held runs left out
	HeadRuns    []Run // runs for Pull.HeadSHA
	Jobs        map[int64][]Job
	Annotations map[int64][]Annotation
	ReleaseRun  *Run
	ReleaseJobs []Job
	// Unpopulated names the release projects that every instance skipped.
	// Set once every populate job has reported; it turns a sync whose jobs
	// all succeeded into a failure.
	Unpopulated []string
}

// RunStateOf maps GitHub status and conclusion to a RunState.
func RunStateOf(status, conclusion string) RunState {
	switch status {
	case "completed":
		if conclusion == "success" {
			return StateSuccess
		}
		return StateFailure
	case "in_progress":
		return StateRunning
	}
	return StateQueued
}

// needsApproval reports whether GitHub ended a run at once, with no jobs,
// because a person has to approve it first.
func needsApproval(r Run) bool {
	return r.Status == "completed" && r.Conclusion == "action_required"
}

func jobState(j Job) RunState { return RunStateOf(j.Status, j.Conclusion) }

func isSkipped(j Job) bool { return j.Status == "completed" && j.Conclusion == "skipped" }

// FindRun returns the newest run whose Name equals name exactly, or nil.
func FindRun(runs []Run, name string) *Run {
	var best *Run
	for i := range runs {
		if runs[i].Name == name && (best == nil || newer(runs[i], *best)) {
			best = &runs[i]
		}
	}
	return best
}

func newer(a, b Run) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.ID > b.ID
}

func hasLabel(p *Pull, label string) bool {
	for _, l := range p.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// DeriveStep maps the facts to the wizard step.
func DeriveStep(f Facts) Step {
	if f.Pull == nil {
		return StepCutting
	}
	if f.Pull.MergedAt != nil {
		if f.ReleaseRun != nil && RunStateOf(f.ReleaseRun.Status, f.ReleaseRun.Conclusion) == StateSuccess {
			return StepDone
		}
		return StepReleasing
	}
	if f.CutRun != nil && f.CutRun.Status != "completed" {
		return StepCutting
	}
	if !hasLabel(f.Pull, LabelSync) {
		return StepChangelog
	}
	if st, _, _ := SyncCards(f); st != string(StateSuccess) {
		return StepSync
	}
	return StepQA
}

// headRuns returns the PR-workflow runs for the current head,
// newest first. A run that waits for approval (what a re-cut's push leaves on
// the new head) never ran: it is left out, as if there were no run yet.
func headRuns(f Facts) []Run {
	if f.Pull == nil || f.Pull.HeadSHA == "" {
		return nil
	}
	var out []Run
	for _, r := range f.HeadRuns {
		if r.WorkflowFile == WorkflowPR && r.HeadSHA == f.Pull.HeadSHA && !needsApproval(r) {
			out = append(out, r)
		}
	}
	// insertion sort, newest first (lists are tiny)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && newer(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func findRunWith(f Facts, match func(Job) bool) *Run {
	runs := headRuns(f)
	for i := range runs {
		for _, j := range f.Jobs[runs[i].ID] {
			if match(j) {
				return &runs[i]
			}
		}
	}
	return nil
}

func isPopulate(j Job) bool {
	_, ok := InstanceOf(j.Name, JobPopulatePrefix)
	return ok && !isSkipped(j)
}

func isLane(j Job) bool {
	return strings.HasPrefix(j.Name, JobLanePrefix) && !isSkipped(j)
}

// isQAFanOut reports a job of the QA fan-out that ran, lane or not.
func isQAFanOut(j Job) bool {
	return strings.HasPrefix(j.Name, JobQAPrefix) && !isSkipped(j)
}

func isCDN(j Job) bool {
	_, ok := InstanceOf(j.Name, JobCDNPrefix)
	return ok && !isSkipped(j)
}

func hasJob(jobs []Job, match func(Job) bool) bool {
	for _, j := range jobs {
		if match(j) {
			return true
		}
	}
	return false
}

// runFailed reports whether a run ended without doing its work. A run GitHub
// skipped altogether did not fail.
func runFailed(r Run) bool {
	if r.Status != "completed" {
		return false
	}
	switch r.Conclusion {
	case "success", "skipped", "neutral":
		return false
	}
	return true
}

// Texts of QA.Error.
const (
	qaErrCDN   = "The download servers did not serve the pre-release in time."
	qaErrStart = "QA could not start."
)

// SyncCards derives the sync state and per-instance cards from the newest
// run for the head that has at least one non-skipped populate job. Without
// such a run the sync is queued, unless the newest run that is not a QA-only
// run (one with lane or CDN jobs) ended in failure: the workflow then failed
// before any populate job, and that run is returned with no cards.
func SyncCards(f Facts) (string, []InstanceCard, *Run) {
	run := findRunWith(f, isPopulate)
	cards := []InstanceCard{}
	if run == nil {
		runs := headRuns(f)
		for i := range runs {
			jobs := f.Jobs[runs[i].ID]
			if hasJob(jobs, isLane) || hasJob(jobs, isCDN) {
				continue
			}
			if runFailed(runs[i]) {
				return string(StateFailure), cards, &runs[i]
			}
			break
		}
		return string(StateQueued), cards, nil
	}
	idx := map[string]int{}
	jobs := f.Jobs[run.ID]
	for _, j := range jobs {
		if !isPopulate(j) {
			continue
		}
		inst, _ := InstanceOf(j.Name, JobPopulatePrefix)
		idx[inst] = len(cards)
		cards = append(cards, InstanceCard{
			Instance: inst, State: jobState(j), URL: j.HTMLURL,
			StartedAt: j.StartedAt, FinishedAt: j.CompletedAt,
			Errors: []string{}, Units: []CardUnit{},
		})
	}
	failed, allOK := false, true
	for _, c := range cards {
		if c.State == StateFailure {
			failed = true
		}
		if c.State != StateSuccess {
			allOK = false
		}
	}
	// Only the CDN jobs of the sync run itself count here; those of the run
	// the qa-release label starts belong to QA.
	for _, j := range jobs {
		inst, ok := InstanceOf(j.Name, JobCDNPrefix)
		if !ok {
			continue
		}
		cdn := string(jobState(j))
		if isSkipped(j) {
			cdn = "skipped"
		}
		if i, found := idx[inst]; found {
			cards[i].CDN = cdn
		}
		switch cdn {
		case string(StateFailure):
			failed = true
			allOK = false
		case string(StateSuccess), "skipped":
		default:
			allOK = false
		}
	}
	switch {
	case failed:
		return string(StateFailure), cards, run
	case allOK && len(f.Unpopulated) > 0:
		return string(StateFailure), cards, run
	case allOK:
		return string(StateSuccess), cards, run
	}
	return string(StateRunning), cards, run
}

// QARun returns the run QA is read from: the newest run for the head that has
// lane or CDN jobs, else the newest run created after the sync run (the run
// the qa-release label started, before it has such jobs or when it never got
// that far). nil when there is none.
func QARun(f Facts) *Run {
	runs := headRuns(f)
	for i := range runs {
		jobs := f.Jobs[runs[i].ID]
		if hasJob(jobs, isLane) || hasJob(jobs, isCDN) {
			return &runs[i]
		}
	}
	_, _, sync := SyncCards(f)
	if sync == nil || len(runs) == 0 {
		return nil
	}
	if runs[0].ID != sync.ID && newer(runs[0], *sync) {
		return &runs[0]
	}
	return nil
}

// QALanes derives the QA state and lanes from QARun. The state is "failure"
// when a CDN wait job failed, or when the run ended in failure without a
// single lane.
func QALanes(f Facts, units []UnitState, hasQALabel bool) QA {
	qa := QA{State: "waiting", Lanes: []Lane{}}
	if !hasQALabel {
		return qa
	}
	qa.State = "running"
	run := QARun(f)
	if run == nil {
		return qa
	}
	qa.URL = run.HTMLURL
	cdnFailed := false
	for _, j := range f.Jobs[run.ID] {
		if isCDN(j) && jobState(j) == StateFailure {
			cdnFailed = true
		}
		if !isLane(j) {
			continue
		}
		l := Lane{Name: strings.TrimPrefix(j.Name, JobLanePrefix), URL: j.HTMLURL, State: jobState(j)}
		for _, a := range f.Annotations[j.ID] {
			if a.Title != AnnotationJenkins {
				continue
			}
			parts := strings.Fields(a.Message)
			if len(parts) < 2 {
				continue
			}
			l.Unit = unitFor(units, parts[0])
			l.JenkinsURL = parts[1]
			break
		}
		qa.Lanes = append(qa.Lanes, l)
	}
	switch {
	case cdnFailed:
		qa.State, qa.Error = "failure", qaErrCDN
	case len(qa.Lanes) == 0 && runFailed(*run):
		qa.State, qa.Error = "failure", qaErrStart
	case len(qa.Lanes) > 0 && run.Status == "completed":
		qa.State = "finished"
	case run.Status == "completed" && hasJob(f.Jobs[run.ID], isQAFanOut):
		// The fan-out ran and declared no lane: the release has no QA
		// tests. Nothing to wait for.
		qa.State = "finished"
	}
	return qa
}

// unitFor returns the unit whose ReleaseProject equals project or is a
// ":"-prefix of it, preferring the longest such ReleaseProject.
func unitFor(units []UnitState, project string) string {
	best, bestLen := "", -1
	for _, u := range units {
		rp := u.ReleaseProject
		if rp == "" {
			continue
		}
		if project == rp || strings.HasPrefix(project, rp+":") {
			if len(rp) > bestLen {
				best, bestLen = u.Unit, len(rp)
			}
		}
	}
	return best
}

// FinalStatus derives the releasing-phase status; nil while no release run
// exists. Skipped jobs produce no card; a skipped finalize job reports
// "skipped", as does cleanup when every cleanup job was skipped.
func FinalStatus(f Facts) *Final {
	if f.ReleaseRun == nil {
		return nil
	}
	fin := &Final{
		Tags: []string{}, Instances: []InstanceCard{},
		State: StateRunning,
		URL:   f.ReleaseRun.HTMLURL,
	}
	copyFailed := false
	var cleanup []Job
	for _, j := range f.ReleaseJobs {
		if inst, ok := InstanceOf(j.Name, JobCopyPrefix); ok {
			if isSkipped(j) {
				continue
			}
			if jobState(j) == StateFailure {
				copyFailed = true
			}
			fin.Instances = append(fin.Instances, InstanceCard{
				Instance: inst, State: jobState(j), URL: j.HTMLURL,
				StartedAt: j.StartedAt, FinishedAt: j.CompletedAt,
				Errors: []string{}, Units: []CardUnit{},
			})
		} else if j.Name == JobFinalize {
			if isSkipped(j) {
				fin.GitHubReleases = "skipped"
			} else {
				fin.GitHubReleases = string(jobState(j))
			}
		} else if _, ok := InstanceOf(j.Name, JobDeletePrefix); ok {
			cleanup = append(cleanup, j)
		}
	}
	fin.Cleanup = aggregate(cleanup)
	run := f.ReleaseRun
	switch {
	case run.Status == "completed" && run.Conclusion != "success", copyFailed:
		fin.State = StateFailure
	case run.Status == "completed":
		fin.State = StateSuccess
	case run.Status == "in_progress":
		fin.State = StateRunning
	default:
		fin.State = StateQueued
	}
	return fin
}

func aggregate(jobs []Job) string {
	if len(jobs) == 0 {
		return ""
	}
	out := StateSuccess
	allSkipped := true
	for _, j := range jobs {
		if isSkipped(j) {
			continue
		}
		allSkipped = false
		switch jobState(j) {
		case StateFailure:
			return string(StateFailure)
		case StateRunning:
			out = StateRunning
		case StateQueued:
			if out != StateRunning {
				out = StateQueued
			}
		}
	}
	if allSkipped {
		return "skipped"
	}
	return string(out)
}
