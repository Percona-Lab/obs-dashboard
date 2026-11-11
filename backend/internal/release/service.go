package release

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/percona/obs-dashboard/internal/store"
)

const (
	defaultTTL     = 15 * time.Second
	mergedWindow   = 7 * 24 * time.Hour
	historyPerPage = 20
	runListLimit   = 100
	refreshTimeout = time.Minute
	mainRef        = "main"

	auditCut     = "cut"
	auditRelease = "release"
)

// errUnitsFile marks a units file that exists but cannot be used.
var errUnitsFile = errors.New("release units file is invalid")

// releaseYAMLRe matches the release.yaml of a unit at any depth below
// root/<product>/releases/.
var releaseYAMLRe = regexp.MustCompile(`^root/[^/]+/releases/(?:[^/]+/)*release\.yaml$`)

// Options configures a Service.
type Options struct {
	GitHub   GitHub
	DB       *sql.DB
	Packages PackageSource
	Now      func() time.Time
	TTL      time.Duration // how long a snapshot is reused; 15s when zero
}

// Snapshot is every release in flight, as last read from GitHub.
type Snapshot struct {
	Releases    []Release `json:"releases"`
	Stale       bool      `json:"stale"`
	Error       string    `json:"error"`
	RefreshedAt time.Time `json:"refreshed_at"`
}

// HistoryRow is one finished release of one unit.
type HistoryRow struct {
	Tag         string    `json:"tag"`
	ReleaseID   string    `json:"release_id"`
	Unit        string    `json:"unit"`
	ReleasedAt  time.Time `json:"released_at"`
	ReleaseURL  string    `json:"release_url"`
	PRURL       string    `json:"pr_url"`
	Notes       string    `json:"notes"`
	ReleasedBy  string    `json:"released_by"`
	FailedLanes []string  `json:"failed_lanes"`
}

// Service reads the state of releases from GitHub. It keeps no release state
// of its own: everything it holds is a cache of GitHub.
type Service struct {
	gh   GitHub
	db   *sql.DB
	pkgs PackageSource
	now  func() time.Time
	ttl  time.Duration

	mu       sync.Mutex
	gen      uint64       // bumped by Invalidate
	good     Snapshot     // result of the last successful refresh
	haveGood bool         // false until a refresh has succeeded
	lastErr  string       // error of the last refresh, "" when it succeeded
	at       time.Time    // when the last refresh finished
	atGen    uint64       // gen the last refresh started under
	inflight *refreshCall // the refresh running now, if any

	mainMu  sync.Mutex
	main    []mainUnit
	mainAt  time.Time
	mainGen uint64
	mainOK  bool

	// Facts that never change once observed.
	cacheMu   sync.Mutex
	pullFiles map[string][]string         // "<number>@<head sha>"
	generated map[int64]map[string]string // successful cut run -> unit -> section
	reports   map[int64]jobReport         // finished populate job
	tagPulls  map[string]tagPull          // tag -> PR of the tagged commit
}

type refreshCall struct {
	gen  uint64
	done chan struct{}
	snap Snapshot
	err  error
}

// mainUnit is a unit of the units file with its release.yaml on main.
type mainUnit struct {
	def  UnitDef
	yaml []byte   // nil when the unit has no release.yaml on main
	tags []string // every release tag, oldest first
}

type jobReport struct {
	failedUnits []string
	removals    []Removal
	errors      []string
}

type tagPull struct {
	number int
	url    string
}

// NewService returns a Service. Nothing is read until the first call.
func NewService(o Options) *Service {
	s := &Service{
		gh: o.GitHub, db: o.DB, pkgs: o.Packages, now: o.Now, ttl: o.TTL,
		pullFiles: map[string][]string{},
		generated: map[int64]map[string]string{},
		reports:   map[int64]jobReport{},
		tagPulls:  map[string]tagPull{},
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.ttl <= 0 {
		s.ttl = defaultTTL
	}
	return s
}

// Invalidate makes the next Snapshot read GitHub again, even when a refresh
// that started earlier is still running.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.gen++
	s.mu.Unlock()
}

// Snapshot returns the releases in flight. A result younger than the TTL is
// reused; otherwise one refresh runs and concurrent callers share it. When
// the refresh fails, the last good result is returned marked stale; with no
// earlier result the error is returned.
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	for {
		s.mu.Lock()
		if s.haveGood && s.atGen == s.gen && s.now().Sub(s.at) < s.ttl {
			snap := s.currentLocked()
			s.mu.Unlock()
			return snap, nil
		}
		c := s.inflight
		if c == nil {
			c = &refreshCall{gen: s.gen, done: make(chan struct{})}
			s.inflight = c
			// The refresh outlives the caller that started it: others wait on it.
			go s.runRefresh(context.WithoutCancel(ctx), c)
		}
		// A refresh that started before the last Invalidate may predate the
		// change the caller wants to see: wait for it, then start another.
		join := c.gen == s.gen
		s.mu.Unlock()

		select {
		case <-c.done:
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
		if join {
			return c.snap, c.err
		}
	}
}

// currentLocked is the last good snapshot, marked stale when the refresh
// after it failed. s.mu must be held.
func (s *Service) currentLocked() Snapshot {
	snap := s.good
	if s.lastErr != "" {
		snap.Stale = true
		snap.Error = s.lastErr
	}
	return snap
}

func (s *Service) runRefresh(ctx context.Context, c *refreshCall) {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	releases, err := s.refresh(ctx)

	s.mu.Lock()
	s.inflight = nil
	if err == nil {
		s.good = Snapshot{Releases: releases, RefreshedAt: s.now().UTC()}
		s.haveGood = true
		s.lastErr = ""
	} else if s.haveGood {
		s.lastErr = err.Error()
	}
	if s.haveGood {
		s.at = s.now()
		s.atGen = c.gen
		c.snap = s.currentLocked()
	} else {
		c.err = err
	}
	s.mu.Unlock()
	close(c.done)
}

// candidate is a release found in GitHub, before its details are read.
type candidate struct {
	id        string
	branch    string
	branchSHA string // "" when the branch does not exist
	pull      *Pull
	fork      bool
	paths     []string // release.yaml files the PR changes
}

// refresh reads every release in flight from GitHub.
func (s *Service) refresh(ctx context.Context) ([]Release, error) {
	now := s.now()
	branches, err := s.gh.Branches(ctx, BranchPrefix)
	if err != nil {
		return nil, err
	}
	open, err := s.gh.OpenPulls(ctx)
	if err != nil {
		return nil, err
	}
	merged, err := s.gh.MergedPulls(ctx, now.Add(-mergedWindow))
	if err != nil {
		return nil, err
	}
	cutRuns, err := s.gh.WorkflowRuns(ctx, WorkflowCut, runListLimit)
	if err != nil {
		return nil, err
	}
	releaseRuns, err := s.gh.WorkflowRuns(ctx, WorkflowRelease, runListLimit)
	if err != nil {
		return nil, err
	}

	branchSHA := map[string]string{}
	for _, b := range branches {
		branchSHA[b.Name] = b.SHA
	}
	var cands []candidate
	// Branches of this repository that a PR accounts for, in flight or not.
	claimed := map[string]bool{}
	addPull := func(p Pull, isMerged bool) error {
		fork := p.HeadRepo != p.BaseRepo
		dashboard := !fork && strings.HasPrefix(p.HeadRef, BranchPrefix)
		paths, err := s.releaseFiles(ctx, p)
		if err != nil {
			return err
		}
		if !fork {
			claimed[p.HeadRef] = true
		}
		if len(paths) == 0 && (isMerged || !dashboard) {
			return nil
		}
		if isMerged {
			if run := FindRun(releaseRuns, ReleaseRunName(p.Number)); run != nil &&
				RunStateOf(run.Status, run.Conclusion) == StateSuccess {
				return nil
			}
		}
		c := candidate{id: fmt.Sprintf("pr-%d", p.Number), pull: &p, fork: fork, paths: paths}
		if !fork {
			c.branch = p.HeadRef
			c.branchSHA = branchSHA[p.HeadRef]
		}
		if dashboard {
			c.id = strings.TrimPrefix(p.HeadRef, BranchPrefix)
		}
		cands = append(cands, c)
		return nil
	}
	for _, p := range open {
		if err := addPull(p, false); err != nil {
			return nil, err
		}
	}
	for _, p := range merged {
		// An open PR on the same branch is the release; this one is its past.
		if p.HeadRepo == p.BaseRepo && claimed[p.HeadRef] {
			continue
		}
		if err := addPull(p, true); err != nil {
			return nil, err
		}
	}
	for _, b := range branches {
		if !claimed[b.Name] {
			claimed[b.Name] = true
			cands = append(cands, candidate{id: strings.TrimPrefix(b.Name, BranchPrefix), branch: b.Name, branchSHA: b.SHA})
		}
	}
	// A first cut that has not created its branch yet.
	cutPrefix := CutRunName(BranchPrefix)
	for _, r := range cutRuns {
		if r.Status == "completed" || !strings.HasPrefix(r.Name, cutPrefix) {
			continue
		}
		branch := strings.TrimPrefix(r.Name, CutRunName(""))
		if !claimed[branch] {
			claimed[branch] = true
			cands = append(cands, candidate{id: strings.TrimPrefix(branch, BranchPrefix), branch: branch})
		}
	}

	audit := map[string][]store.ReleaseAudit{}
	if s.db != nil {
		var names []string
		for _, c := range cands {
			if c.branch != "" {
				names = append(names, c.branch)
			}
		}
		if audit, err = store.ReleaseAuditByBranch(s.db, names); err != nil {
			return nil, fmt.Errorf("release audit: %w", err)
		}
	}

	releases := make([]Release, 0, len(cands))
	for _, c := range cands {
		rel, err := s.build(ctx, c, cutRuns, releaseRuns, audit[c.branch])
		if err != nil {
			return nil, err
		}
		releases = append(releases, rel)
	}
	// A cancelled or timed out refresh may have skipped optional reads.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].ID < releases[j].ID })
	return releases, nil
}

// releaseFiles returns the release.yaml files a PR changes. The answer for a
// given head never changes, so it is kept.
func (s *Service) releaseFiles(ctx context.Context, p Pull) ([]string, error) {
	key := fmt.Sprintf("%d@%s", p.Number, p.HeadSHA)
	s.cacheMu.Lock()
	paths, ok := s.pullFiles[key]
	s.cacheMu.Unlock()
	if ok {
		return paths, nil
	}
	files, err := s.gh.PullFiles(ctx, p.Number)
	if err != nil {
		return nil, err
	}
	paths = []string{}
	for _, f := range files {
		if releaseYAMLRe.MatchString(f) {
			paths = append(paths, f)
		}
	}
	sort.Strings(paths)
	s.cacheMu.Lock()
	s.pullFiles[key] = paths
	s.cacheMu.Unlock()
	return paths, nil
}

// build reads the details of one release and derives its step.
func (s *Service) build(ctx context.Context, c candidate, cutRuns, releaseRuns []Run, audit []store.ReleaseAudit) (Release, error) {
	rel := Release{
		ID: c.id, Branch: c.branch, HeadSHA: c.branchSHA, Dashboard: !c.fork,
		Units: []UnitState{}, Sync: []InstanceCard{}, Removals: []Removal{}, Actions: []AuditEntry{},
	}
	f := Facts{Pull: c.pull, Jobs: map[int64][]Job{}, Annotations: map[int64][]Annotation{}}
	var generated map[string]string
	if !c.fork {
		f.CutRun = FindRun(cutRuns, CutRunName(c.branch))
		generated = s.generatedSections(ctx, cutRuns, c.branch)
	}
	if f.CutRun != nil {
		rel.Cut = &RunRef{State: RunStateOf(f.CutRun.Status, f.CutRun.Conclusion), URL: f.CutRun.HTMLURL}
	}

	var err error
	if c.pull != nil {
		rel.Units, err = s.pullUnits(ctx, c, generated)
	} else if c.branchSHA != "" {
		rel.Units, err = s.branchUnits(ctx, c.branch, generated)
	}
	if err != nil {
		return rel, err
	}

	// The cut workflow does not open the PR; the dashboard does, once.
	if c.pull == nil && c.branchSHA != "" && len(rel.Units) > 0 && rel.Cut != nil && rel.Cut.State == StateSuccess {
		p, err := s.gh.CreatePull(ctx, c.branch, pullTitle(rel.Units), pullBody(rel.Units))
		if err != nil {
			// Not fatal: the release stays at the cutting step and the next
			// refresh tries again.
			slog.Warn("release: open pull request", "branch", c.branch, "err", err)
		} else {
			f.Pull = &p
		}
	}

	if p := f.Pull; p != nil {
		rel.PR, rel.PRURL, rel.HeadSHA = p.Number, p.HTMLURL, p.HeadSHA
		if p.MergedAt != nil {
			err = s.readFinal(ctx, &f, &rel, releaseRuns)
		} else {
			err = s.readHead(ctx, &f, &rel)
		}
		if err != nil {
			return rel, err
		}
	}
	rel.Step = DeriveStep(f)

	for _, a := range audit {
		rel.Actions = append(rel.Actions, AuditEntry{At: a.At, Email: a.Email, Action: a.Action})
		if a.Action == auditCut && rel.StartedAt == nil {
			at := a.At
			rel.StartedBy, rel.StartedAt = a.Email, &at
		}
	}
	return rel, nil
}

// readHead reads the PR-workflow runs of an open PR's head: sync cards with
// their reports, and QA lanes.
func (s *Service) readHead(ctx context.Context, f *Facts, rel *Release) error {
	p := f.Pull
	if p.HeadSHA != "" {
		runs, err := s.gh.RunsForSHA(ctx, p.HeadSHA)
		if err != nil {
			return err
		}
		f.HeadRuns = runs
		for _, r := range runs {
			if r.WorkflowFile != WorkflowPR || r.HeadSHA != p.HeadSHA {
				continue
			}
			if f.Jobs[r.ID], err = s.gh.Jobs(ctx, r.ID); err != nil {
				return err
			}
		}
	}

	state, cards, syncRun := SyncCards(*f)
	rel.SyncState, rel.Sync = state, cards
	if syncRun != nil {
		seen := map[Removal]bool{}
		for _, j := range f.Jobs[syncRun.ID] {
			inst, ok := InstanceOf(j.Name, JobPopulatePrefix)
			if !ok || isSkipped(j) || j.Status != "completed" {
				continue
			}
			rep := s.populateReport(ctx, syncRun.ID, j, inst)
			for i := range cards {
				if cards[i].Instance == inst {
					cards[i].FailedUnits = append(cards[i].FailedUnits, rep.failedUnits...)
					cards[i].Errors = append(cards[i].Errors, rep.errors...)
				}
			}
			for _, r := range rep.removals {
				if !seen[r] {
					seen[r] = true
					rel.Removals = append(rel.Removals, r)
				}
			}
		}
	}

	qaLabel := hasLabel(p, LabelQA)
	if qaLabel {
		if run := findRunWith(*f, isLane); run != nil {
			for _, j := range f.Jobs[run.ID] {
				if !isLane(j) {
					continue
				}
				// A lane without its Jenkins link falls back to the job page.
				if anns, err := s.gh.Annotations(ctx, j.ID); err == nil {
					f.Annotations[j.ID] = anns
				}
			}
		}
	}
	qa := QALanes(*f, rel.Units, qaLabel)
	rel.QA = &qa
	return nil
}

// readFinal reads the release run of a merged PR.
func (s *Service) readFinal(ctx context.Context, f *Facts, rel *Release, releaseRuns []Run) error {
	f.ReleaseRun = FindRun(releaseRuns, ReleaseRunName(f.Pull.Number))
	if f.ReleaseRun == nil {
		return nil
	}
	jobs, err := s.gh.Jobs(ctx, f.ReleaseRun.ID)
	if err != nil {
		return err
	}
	f.ReleaseJobs = jobs
	rel.Final = FinalStatus(*f)
	for _, u := range rel.Units {
		if u.Tag != "" {
			rel.Final.Tags = append(rel.Final.Tags, u.Tag)
		}
	}
	return nil
}

// populateReport returns what a finished populate job reported: the projects
// that failed, the removals the release would make, and the job's error
// annotations when it failed. A missing artifact leaves the lists empty.
func (s *Service) populateReport(ctx context.Context, runID int64, j Job, instance string) jobReport {
	s.cacheMu.Lock()
	rep, ok := s.reports[j.ID]
	s.cacheMu.Unlock()
	if ok {
		return rep
	}
	complete := true
	data, err := s.gh.Artifact(ctx, runID, ArtifactReportPrefix+instance)
	if err != nil {
		// The artifact may still appear, or the call may work next time.
		complete = false
	} else {
		var entries []struct {
			Project  string   `json:"project"`
			Outcome  string   `json:"outcome"`
			Removals []string `json:"removals"`
		}
		if json.Unmarshal(data, &entries) == nil {
			for _, e := range entries {
				if e.Outcome != "success" {
					rep.failedUnits = append(rep.failedUnits, e.Project)
				}
				for _, item := range e.Removals {
					rep.removals = append(rep.removals, Removal{Project: e.Project, Item: item})
				}
			}
		}
	}
	if jobState(j) == StateFailure {
		anns, err := s.gh.Annotations(ctx, j.ID)
		if err != nil {
			complete = false
		}
		for _, a := range anns {
			if a.Level == "failure" {
				rep.errors = append(rep.errors, a.Message)
			}
		}
	}
	if complete {
		s.cacheMu.Lock()
		s.reports[j.ID] = rep
		s.cacheMu.Unlock()
	}
	return rep
}

// generatedSections returns the changelog sections the latest successful cut
// of branch generated, by unit; nil when it left no usable artifact.
func (s *Service) generatedSections(ctx context.Context, cutRuns []Run, branch string) map[string]string {
	name := CutRunName(branch)
	var run *Run
	for i := range cutRuns {
		r := &cutRuns[i]
		if r.Name == name && RunStateOf(r.Status, r.Conclusion) == StateSuccess && (run == nil || newer(*r, *run)) {
			run = r
		}
	}
	if run == nil {
		return nil
	}
	s.cacheMu.Lock()
	sections, ok := s.generated[run.ID]
	s.cacheMu.Unlock()
	if ok {
		return sections
	}
	data, err := s.gh.Artifact(ctx, run.ID, ArtifactChangelogs)
	if err != nil {
		return nil
	}
	sections = map[string]string{}
	if json.Unmarshal(data, &sections) != nil {
		sections = map[string]string{}
	}
	s.cacheMu.Lock()
	s.generated[run.ID] = sections
	s.cacheMu.Unlock()
	return sections
}

// pullUnits returns the units of a PR: one per release.yaml it changes.
func (s *Service) pullUnits(ctx context.Context, c candidate, generated map[string]string) ([]UnitState, error) {
	p := c.pull
	// A fork's branch name means nothing in this repository, and the branch
	// of a merged PR is deleted: read those by commit.
	var refs []string
	switch {
	case c.fork:
		refs = []string{p.HeadSHA}
	case p.MergedAt != nil:
		refs = []string{p.MergeSHA, p.HeadSHA}
	default:
		refs = []string{p.HeadRef, p.HeadSHA}
	}
	units := make([]UnitState, 0, len(c.paths))
	for _, yamlPath := range c.paths {
		var data []byte
		var ref string
		for _, r := range refs {
			if r == "" {
				continue
			}
			b, err := s.gh.File(ctx, yamlPath, r)
			if err == nil {
				data, ref = b, r
				break
			}
			if !errors.Is(err, ErrNotFound) {
				return nil, err
			}
		}
		u, err := s.unitState(ctx, yamlPath, ref, data, generated)
		if err != nil {
			return nil, err
		}
		units = append(units, u)
	}
	return units, nil
}

// branchUnits returns the units of a branch that has no PR: those whose
// release.yaml on the branch differs from main.
func (s *Service) branchUnits(ctx context.Context, branch string, generated map[string]string) ([]UnitState, error) {
	units := []UnitState{}
	main, err := s.mainUnits(ctx)
	if err != nil {
		if soft(err) {
			return units, nil
		}
		return nil, err
	}
	for _, m := range main {
		yamlPath := path.Join(m.def.ReleaseDir, "release.yaml")
		data, err := s.gh.File(ctx, yamlPath, branch)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if string(data) == string(m.yaml) {
			continue
		}
		u, err := s.unitState(ctx, yamlPath, branch, data, generated)
		if err != nil {
			return nil, err
		}
		if m.def.ReleaseProject != "" {
			u.ReleaseProject = m.def.ReleaseProject
		}
		units = append(units, u)
	}
	return units, nil
}

// unitState describes the unit whose release.yaml is yamlPath with content
// data at ref. Without data, or with data that cannot be parsed, only what
// the path tells is filled in.
func (s *Service) unitState(ctx context.Context, yamlPath, ref string, data []byte, generated map[string]string) (UnitState, error) {
	dir := path.Dir(yamlPath)
	u := UnitState{ReleaseDir: dir, ReleaseProject: strings.ReplaceAll(strings.TrimPrefix(dir, "root/"), "/", ":")}
	if data == nil {
		return u, nil
	}
	rec, err := CurrentRelease(data)
	if err != nil {
		return u, nil
	}
	u.Unit, u.Tag, u.ReleaseID = rec.Project, rec.Tag, ReleaseID(rec.Tag)
	changelog, err := s.gh.File(ctx, path.Join(dir, "CHANGELOG.md"), ref)
	switch {
	case err == nil:
		if section, err := ExtractSection(changelog, u.ReleaseID); err == nil {
			u.Changelog = section
		}
	case !errors.Is(err, ErrNotFound):
		return u, err
	}
	if g, ok := generated[u.Unit]; ok {
		u.Generated = &g
	}
	return u, nil
}

// pullTitle and pullBody are the text of the PR the dashboard opens. They are
// built from the release content only: the repository is public and must not
// learn who is using the dashboard.
func pullTitle(units []UnitState) string {
	ids := make([]string, 0, len(units))
	for _, u := range units {
		ids = append(ids, u.ReleaseID)
	}
	return "Release " + strings.Join(ids, ", ")
}

func pullBody(units []UnitState) string {
	sections := make([]string, 0, len(units))
	for _, u := range units {
		text := u.Changelog
		if u.Generated != nil {
			text = *u.Generated
		}
		if text = strings.TrimSpace(text); text != "" {
			sections = append(sections, text)
		}
	}
	if len(sections) == 0 {
		return ""
	}
	return strings.Join(sections, "\n\n") + "\n"
}

// soft reports whether err means "not there or not usable" rather than a
// failed GitHub call.
func soft(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, errUnitsFile)
}

// mainUnits returns the units file of main with each unit's release.yaml on
// main. It is reused for the TTL and dropped by Invalidate.
func (s *Service) mainUnits(ctx context.Context) ([]mainUnit, error) {
	s.mu.Lock()
	gen := s.gen
	s.mu.Unlock()

	s.mainMu.Lock()
	defer s.mainMu.Unlock()
	if s.mainOK && s.mainGen == gen && s.now().Sub(s.mainAt) < s.ttl {
		return s.main, nil
	}
	data, err := s.gh.File(ctx, UnitsPath, mainRef)
	if err != nil {
		return nil, err
	}
	defs, err := ParseUnits(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errUnitsFile, err)
	}
	units := make([]mainUnit, 0, len(defs))
	for _, d := range defs {
		m := mainUnit{def: d}
		b, err := s.gh.File(ctx, path.Join(d.ReleaseDir, "release.yaml"), mainRef)
		switch {
		case err == nil:
			m.yaml, m.tags = b, releaseTags(b)
		case !errors.Is(err, ErrNotFound):
			return nil, err
		}
		units = append(units, m)
	}
	s.main, s.mainAt, s.mainGen, s.mainOK = units, s.now(), gen, true
	return units, nil
}

// releaseTags returns every tag of a release.yaml, oldest first.
func releaseTags(releaseYAML []byte) []string {
	var doc struct {
		Releases []yaml.Node `yaml:"releases"`
	}
	if yaml.Unmarshal(releaseYAML, &doc) != nil {
		return nil
	}
	var tags []string
	for _, n := range doc.Releases {
		tag := n.Value
		if n.Kind != yaml.ScalarNode {
			var entry struct {
				Tag string `yaml:"tag"`
			}
			if n.Decode(&entry) != nil {
				continue
			}
			tag = entry.Tag
		}
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// Units returns the selection list: every unit with its last release, build
// progress and eligibility.
func (s *Service) Units(ctx context.Context) ([]UnitRow, error) {
	snap, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	main, err := s.mainUnits(ctx)
	if err != nil {
		return nil, err
	}
	inFlight := map[string]string{}
	for _, r := range snap.Releases {
		for _, u := range r.Units {
			if u.Unit != "" {
				inFlight[u.Unit] = r.ID
			}
		}
	}
	defs := make([]UnitDef, 0, len(main))
	last := map[string]string{}
	for _, m := range main {
		defs = append(defs, m.def)
		if n := len(m.tags); n > 0 {
			last[m.def.Unit] = ReleaseID(m.tags[n-1])
		}
	}
	return BuildUnitRows(defs, s.pkgs, inFlight, last)
}

// History returns one page (1-based, 20 GitHub releases per page) of
// finished releases, newest first. GitHub releases whose tag no unit records
// are left out, so a page can hold fewer than 20 rows.
func (s *Service) History(ctx context.Context, page int) ([]HistoryRow, error) {
	if page < 1 {
		page = 1
	}
	published, err := s.gh.Releases(ctx, page, historyPerPage)
	if err != nil {
		return nil, err
	}
	main, err := s.mainUnits(ctx)
	if err != nil {
		return nil, err
	}
	unitOf := map[string]string{}
	for _, m := range main {
		for _, tag := range m.tags {
			unitOf[tag] = m.def.Unit
		}
	}

	rows := []HistoryRow{}
	prOf := map[int]int{} // row index -> PR number
	var prs []int
	for _, r := range published {
		unit, ok := unitOf[r.Tag]
		if !ok {
			continue
		}
		row := HistoryRow{
			Tag: r.Tag, ReleaseID: ReleaseID(r.Tag), Unit: unit, ReleasedAt: r.PublishedAt,
			ReleaseURL: r.HTMLURL, Notes: r.Body, FailedLanes: []string{},
		}
		tp, err := s.pullOfTag(ctx, r.Tag)
		if err != nil {
			return nil, err
		}
		if tp.number != 0 {
			row.PRURL = tp.url
			prOf[len(rows)] = tp.number
			prs = append(prs, tp.number)
		}
		rows = append(rows, row)
	}
	if s.db != nil && len(prs) > 0 {
		audit, err := store.ReleaseAuditByPR(s.db, prs, auditRelease)
		if err != nil {
			return nil, fmt.Errorf("release audit: %w", err)
		}
		for i, pr := range prOf {
			a, ok := audit[pr]
			if !ok {
				continue
			}
			rows[i].ReleasedBy = a.Email
			for _, lane := range strings.Split(a.Detail, "\n") {
				if lane != "" {
					rows[i].FailedLanes = append(rows[i].FailedLanes, lane)
				}
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ReleasedAt.After(rows[j].ReleasedAt) })
	return rows, nil
}

// pullOfTag returns the PR associated with the commit a tag points at; the
// zero value when there is none. Tags do not move, so answers are kept.
func (s *Service) pullOfTag(ctx context.Context, tag string) (tagPull, error) {
	s.cacheMu.Lock()
	tp, ok := s.tagPulls[tag]
	s.cacheMu.Unlock()
	if ok {
		return tp, nil
	}
	sha, err := s.gh.TagSHA(ctx, tag)
	if errors.Is(err, ErrNotFound) {
		return tagPull{}, nil
	}
	if err != nil {
		return tagPull{}, err
	}
	number, url, err := s.gh.PullForCommit(ctx, sha)
	if errors.Is(err, ErrNotFound) {
		return tagPull{}, nil
	}
	if err != nil {
		return tagPull{}, err
	}
	tp = tagPull{number: number, url: url}
	s.cacheMu.Lock()
	s.tagPulls[tag] = tp
	s.cacheMu.Unlock()
	return tp, nil
}
