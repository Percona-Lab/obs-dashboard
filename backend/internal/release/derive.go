package release

import (
	"strings"
)

// Facts are the GitHub observations the derivation functions work from.
type Facts struct {
	Pull        *Pull // nil while no PR exists
	CutRun      *Run  // newest run named exactly CutRunName(branch)
	HeadRuns    []Run // runs for Pull.HeadSHA
	Jobs        map[int64][]Job
	Annotations map[int64][]Annotation
	ReleaseRun  *Run
	ReleaseJobs []Job
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
// newest first.
func headRuns(f Facts) []Run {
	if f.Pull == nil {
		return nil
	}
	var out []Run
	for _, r := range f.HeadRuns {
		if r.WorkflowFile == WorkflowPR && r.HeadSHA == f.Pull.HeadSHA {
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

// SyncCards derives the sync state and per-instance cards from the newest
// run for the head that has at least one non-skipped populate job.
func SyncCards(f Facts) (string, []InstanceCard, *Run) {
	run := findRunWith(f, isPopulate)
	cards := []InstanceCard{}
	if run == nil {
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
			FailedUnits: []string{}, Errors: []string{},
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
	case allOK:
		return string(StateSuccess), cards, run
	}
	return string(StateRunning), cards, run
}

// QALanes derives the QA lanes from the newest run for the head that has at
// least one lane job.
func QALanes(f Facts, units []UnitState, hasQALabel bool) QA {
	qa := QA{State: "waiting", Lanes: []Lane{}}
	if !hasQALabel {
		return qa
	}
	qa.State = "running"
	run := findRunWith(f, isLane)
	if run == nil {
		return qa
	}
	if run.Status == "completed" {
		qa.State = "finished"
	}
	for _, j := range f.Jobs[run.ID] {
		if !isLane(j) {
			continue
		}
		l := Lane{Name: strings.TrimPrefix(j.Name, JobLanePrefix), URL: j.HTMLURL, State: jobState(j)}
		for _, a := range f.Annotations[j.ID] {
			if a.Title != AnnotationJenkins {
				continue
			}
			parts := strings.SplitN(a.Message, " ", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				continue
			}
			l.Unit = unitFor(units, parts[0])
			l.JenkinsURL = parts[1]
			break
		}
		qa.Lanes = append(qa.Lanes, l)
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
// exists.
func FinalStatus(f Facts) *Final {
	if f.ReleaseRun == nil {
		return nil
	}
	fin := &Final{
		Tags: []string{}, Instances: []InstanceCard{},
		State: RunStateOf(f.ReleaseRun.Status, f.ReleaseRun.Conclusion),
		URL:   f.ReleaseRun.HTMLURL,
	}
	var cleanup []Job
	for _, j := range f.ReleaseJobs {
		if inst, ok := InstanceOf(j.Name, JobCopyPrefix); ok {
			fin.Instances = append(fin.Instances, InstanceCard{
				Instance: inst, State: jobState(j), URL: j.HTMLURL,
				StartedAt: j.StartedAt, FinishedAt: j.CompletedAt,
				FailedUnits: []string{}, Errors: []string{},
			})
		} else if j.Name == JobFinalize {
			fin.GitHubReleases = string(jobState(j))
		} else if _, ok := InstanceOf(j.Name, JobDeletePrefix); ok {
			cleanup = append(cleanup, j)
		}
	}
	fin.Cleanup = aggregate(cleanup)
	return fin
}

func aggregate(jobs []Job) string {
	if len(jobs) == 0 {
		return ""
	}
	out := StateSuccess
	for _, j := range jobs {
		if isSkipped(j) {
			continue
		}
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
	return string(out)
}
