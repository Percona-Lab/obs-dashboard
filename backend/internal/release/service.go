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
	defaultTTL      = 15 * time.Second
	mergedWindow    = 7 * 24 * time.Hour
	failedCutWindow = 24 * time.Hour
	historyPerPage  = 20
	runListLimit    = 100
	refreshTimeout  = time.Minute
	mainRef         = "main"

	auditCut     = "cut"
	auditRelease = "release"
)

// A cut run held for approval carries no branch in its name. It belongs to the
// cut or recut recorded closest to its creation, no more than heldBefore
// before or heldAfter after that record.
const (
	heldBefore = 30 * time.Second
	heldAfter  = 180 * time.Second
)

// missingArtifactTTL is how long a finished run's missing artifact is not
// asked for again.
const missingArtifactTTL = 5 * time.Minute

// factCache holds facts that never change once observed (the jobs of a
// finished run, a file at a commit). It is bounded by use: an entry no
// refresh round touched is dropped at the end of that round, so what belongs
// to a release that left the snapshot goes with it. Service.cacheMu guards it.
type factCache[K comparable, V any] struct {
	m map[K]factEntry[V]
}

type factEntry[V any] struct {
	v     V
	round uint64 // refresh round that last used the entry
}

func (c *factCache[K, V]) get(k K, round uint64) (V, bool) {
	e, ok := c.m[k]
	if ok && e.round != round {
		e.round = round
		c.m[k] = e
	}
	return e.v, ok
}

func (c *factCache[K, V]) put(k K, v V, round uint64) {
	if c.m == nil {
		c.m = map[K]factEntry[V]{}
	}
	c.m[k] = factEntry[V]{v: v, round: round}
}

func (c *factCache[K, V]) prune(round uint64) {
	for k, e := range c.m {
		if e.round != round {
			delete(c.m, k)
		}
	}
}

// cached and remember read and write a factCache under the service lock.
func cached[K comparable, V any](s *Service, c *factCache[K, V], k K) (V, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	return c.get(k, s.round)
}

func remember[K comparable, V any](s *Service, c *factCache[K, V], k K, v V) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	c.put(k, v, s.round)
}

// runKey names one attempt of a run: a re-run keeps the id.
type runKey struct {
	id      int64
	attempt int
}

type artifactKey struct {
	run  runKey
	name string
}

// artifact is the first file of a run's artifact, or the fact that the
// finished run had none when last asked.
type artifact struct {
	data    []byte
	missing bool
	at      time.Time
}

type fileKey struct{ sha, path string }

// commitFile is what a path holds at a commit: its content, or the reason it
// cannot be read there (absent or unusable), which is just as final.
type commitFile struct {
	data []byte
	err  error
}

// fileRef is where a file is read: a commit (whose content never changes and
// is kept) or a branch.
type fileRef struct {
	name   string
	commit bool
}

// pruneCaches ends a refresh round: what it did not use is dropped.
func (s *Service) pruneCaches() {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.pullFiles.prune(s.round)
	s.generated.prune(s.round)
	s.reports.prune(s.round)
	s.runJobs.prune(s.round)
	s.jobAnnotations.prune(s.round)
	s.artifacts.prune(s.round)
	s.commitFiles.prune(s.round)
	s.closedPulls.prune(s.round)
}

// cacheSize counts the entries of the per-release caches.
func (s *Service) cacheSize() int {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	return len(s.pullFiles.m) + len(s.generated.m) + len(s.reports.m) + len(s.runJobs.m) +
		len(s.jobAnnotations.m) + len(s.artifacts.m) + len(s.commitFiles.m) + len(s.closedPulls.m)
}

// unusable reports whether err means the file is absent or cannot be read as
// a file: the unit then degrades instead of failing the refresh.
func unusable(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnusable)
}

// jobsOf returns the jobs of run. Those of a finished run do not change and
// are kept; a run in progress is always read again.
func (s *Service) jobsOf(ctx context.Context, run Run) ([]Job, error) {
	key := runKey{run.ID, run.Attempt}
	done := run.Status == "completed"
	if done {
		if jobs, ok := cached(s, &s.runJobs, key); ok {
			return jobs, nil
		}
	}
	jobs, err := s.gh.Jobs(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		// The run list and the job list are two reads: keep only a settled one.
		done = done && j.Status == "completed"
	}
	if done {
		remember(s, &s.runJobs, key, jobs)
	}
	return jobs, nil
}

// annotationsOf returns the annotations of a job; those of a finished job
// are kept.
func (s *Service) annotationsOf(ctx context.Context, j Job) ([]Annotation, error) {
	done := j.Status == "completed"
	if done {
		if anns, ok := cached(s, &s.jobAnnotations, j.ID); ok {
			return anns, nil
		}
	}
	anns, err := s.gh.Annotations(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	if done {
		remember(s, &s.jobAnnotations, j.ID, anns)
	}
	return anns, nil
}

// artifactOf returns the first file of the named artifact of run. For a
// finished run the content is kept; a missing artifact is not asked for
// again for missingArtifactTTL.
func (s *Service) artifactOf(ctx context.Context, run Run, name string) ([]byte, error) {
	key := artifactKey{runKey{run.ID, run.Attempt}, name}
	done := run.Status == "completed"
	if done {
		if a, ok := cached(s, &s.artifacts, key); ok {
			if !a.missing {
				return a.data, nil
			}
			if s.now().Sub(a.at) < missingArtifactTTL {
				return nil, fmt.Errorf("artifact %s of run %d: %w", name, run.ID, ErrNotFound)
			}
		}
	}
	data, err := s.gh.Artifact(ctx, run.ID, name)
	switch {
	case err == nil && done:
		remember(s, &s.artifacts, key, artifact{data: data})
	case errors.Is(err, ErrNotFound) && done:
		remember(s, &s.artifacts, key, artifact{missing: true, at: s.now()})
	}
	return data, err
}

// fileAt reads a file at ref. What a commit holds is kept, including that a
// path is absent or unusable there; a failed call is not.
func (s *Service) fileAt(ctx context.Context, p string, ref fileRef) ([]byte, error) {
	if !ref.commit {
		return s.gh.File(ctx, p, ref.name)
	}
	key := fileKey{ref.name, p}
	if f, ok := cached(s, &s.commitFiles, key); ok {
		return f.data, f.err
	}
	b, err := s.gh.File(ctx, p, ref.name)
	if err == nil || unusable(err) {
		remember(s, &s.commitFiles, key, commitFile{data: b, err: err})
	}
	return b, err
}

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

	// actMu serialises the wizard actions; the fields below it are guarded by it.
	actMu      sync.Mutex
	lastCutAt  time.Time
	recentCuts map[string]time.Time // unit -> when its cut was dispatched
	lastRetry  map[string]time.Time // "<audit action> <release id>" -> when the retry was accepted

	mainMu  sync.Mutex
	main    []mainUnit
	mainAt  time.Time
	mainGen uint64
	mainOK  bool

	// Facts that never change once observed. Those of a release are dropped
	// once no refresh uses them any more (see factCache).
	cacheMu        sync.Mutex
	round          uint64                              // refresh round, for factCache
	pullFiles      factCache[string, []string]         // "<number>@<head sha>"
	generated      factCache[int64, map[string]string] // successful cut run -> unit -> section
	reports        factCache[int64, jobReport]         // finished populate job
	runJobs        factCache[runKey, []Job]            // finished run
	jobAnnotations factCache[int64, []Annotation]      // finished job
	artifacts      factCache[artifactKey, artifact]    // finished run + artifact name
	commitFiles    factCache[fileKey, commitFile]      // commit sha + path
	closedPulls    factCache[string, struct{}]         // branch that has a closed PR
	tagPulls       map[string]tagPull                  // tag -> PR of the tagged commit
	releasedTags   map[string]struct{}                 // tags that have a GitHub release

	// Cuts dispatched and not yet listed by GitHub, by branch. Also under
	// cacheMu. Not release state: an entry lives two minutes at most.
	pending map[string]pendingCut
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
	// units is every entry of the report with its outcome.
	units    []CardUnit
	removals []Removal
	errors   []string
}

type tagPull struct {
	number int
	url    string
}

// NewService returns a Service. Nothing is read until the first call.
func NewService(o Options) *Service {
	s := &Service{
		gh: o.GitHub, db: o.DB, pkgs: o.Packages, now: o.Now, ttl: o.TTL,
		tagPulls:     map[string]tagPull{},
		releasedTags: map[string]struct{}{},
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
	} else {
		slog.Warn("release: refresh failed", "err", err)
		if s.haveGood {
			s.lastErr = publicError(err)
		}
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

// publicError is the text of a refresh error that may be shown: a GitHub
// call that failed says which (method, path, status); anything else is kept
// to the log.
func publicError(err error) string {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Error()
	}
	return "internal error"
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
	s.cacheMu.Lock()
	s.round++
	s.cacheMu.Unlock()
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
	// Runs held for approval are not cut runs of a branch yet: from here on
	// cutRuns is the runs that started.
	cutRuns, held, err := s.heldCuts(cutRuns, now)
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
			// A branch whose PR was closed without merging is not a release,
			// and gets no new PR.
			// A closed PR stays closed: only "none yet" is asked again.
			_, closed := cached(s, &s.closedPulls, b.Name)
			if !closed {
				if closed, err = s.gh.HasClosedPull(ctx, b.Name); err != nil {
					return nil, err
				}
				if closed {
					remember(s, &s.closedPulls, b.Name, struct{}{})
				}
			}
			if closed {
				continue
			}
			cands = append(cands, candidate{id: strings.TrimPrefix(b.Name, BranchPrefix), branch: b.Name, branchSHA: b.SHA})
		}
	}
	// A first cut that has not created its branch yet, or that failed before
	// pushing one (the workflow pushes nothing then); a failure is listed for
	// a day.
	cutPrefix := CutRunName(BranchPrefix)
	for i := range cutRuns {
		r := cutRuns[i]
		if !strings.HasPrefix(r.Name, cutPrefix) {
			continue
		}
		if latest := FindRun(cutRuns, r.Name); latest == nil || latest.ID != r.ID {
			continue
		}
		if r.Status == "completed" && (r.Conclusion == "success" || now.Sub(r.CreatedAt) >= failedCutWindow) {
			continue
		}
		branch := strings.TrimPrefix(r.Name, CutRunName(""))
		if !claimed[branch] {
			claimed[branch] = true
			cands = append(cands, candidate{id: strings.TrimPrefix(branch, BranchPrefix), branch: branch})
		}
	}
	// A cut dispatched a moment ago whose run GitHub does not list yet, and
	// a first cut that waits for approval.
	waiting := s.pendingBranches(cutRuns)
	for branch := range held {
		waiting = append(waiting, branch)
	}
	sort.Strings(waiting)
	for _, branch := range waiting {
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
		var heldRun *Run
		if r, ok := held[c.branch]; ok && !c.fork {
			heldRun = &r
		}
		rel, err := s.build(ctx, c, cutRuns, releaseRuns, audit[c.branch], heldRun)
		if err != nil {
			return nil, err
		}
		if rel.finished {
			continue
		}
		releases = append(releases, rel)
	}
	// A cancelled or timed out refresh may have skipped optional reads.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].ID < releases[j].ID })
	s.pruneCaches()
	return releases, nil
}

// heldCuts splits the cut runs into those that started and, by branch, the
// newest one GitHub holds for approval. A held run older than failedCutWindow
// or one no recorded cut or recut accounts for is dropped.
func (s *Service) heldCuts(runs []Run, now time.Time) ([]Run, map[string]Run, error) {
	started := make([]Run, 0, len(runs))
	var waiting []Run
	for _, r := range runs {
		switch {
		case !needsApproval(r):
			started = append(started, r)
		case now.Sub(r.CreatedAt) < failedCutWindow:
			waiting = append(waiting, r)
		}
	}
	held := map[string]Run{}
	if len(waiting) == 0 || s.db == nil {
		return started, held, nil
	}
	rows, err := store.ReleaseAuditSince(s.db, []string{auditCut, auditRecut}, now.Add(-failedCutWindow-heldAfter))
	if err != nil {
		return nil, nil, fmt.Errorf("release audit: %w", err)
	}
	// One-to-one: take the (run, row) pairs in the window by smallest time
	// distance, each run and each row used at most once.
	type pair struct {
		run, row int
		d        time.Duration
	}
	var pairs []pair
	for i, r := range waiting {
		for j, a := range rows {
			d := r.CreatedAt.Sub(a.At)
			if d < -heldBefore || d > heldAfter {
				continue
			}
			if d < 0 {
				d = -d
			}
			pairs = append(pairs, pair{i, j, d})
		}
	}
	sort.SliceStable(pairs, func(x, y int) bool { return pairs[x].d < pairs[y].d })
	usedRun, usedRow := map[int]bool{}, map[int]bool{}
	matchedAt := map[string]time.Time{}
	for _, p := range pairs {
		if usedRun[p.run] || usedRow[p.row] {
			continue
		}
		usedRun[p.run], usedRow[p.row] = true, true
		r, a := waiting[p.run], rows[p.row]
		if cur, ok := held[a.Branch]; !ok || newer(r, cur) {
			held[a.Branch] = r
			matchedAt[a.Branch] = a.At
		}
	}
	// A branch abandoned after the dispatch the run belongs to is gone: the
	// run stays listed on GitHub but accounts for nothing.
	if len(held) > 0 {
		names := make([]string, 0, len(held))
		for b := range held {
			names = append(names, b)
		}
		byBranch, err := store.ReleaseAuditByBranch(s.db, names)
		if err != nil {
			return nil, nil, fmt.Errorf("release audit: %w", err)
		}
		for b, at := range matchedAt {
			if rs := byBranch[b]; len(rs) > 0 {
				if last := rs[len(rs)-1]; last.Action == auditAbandon && last.At.After(at) {
					delete(held, b)
				}
			}
		}
	}
	return started, held, nil
}

// releaseFiles returns the release.yaml files a PR changes. The answer for a
// given head never changes, so it is kept.
func (s *Service) releaseFiles(ctx context.Context, p Pull) ([]string, error) {
	key := fmt.Sprintf("%d@%s", p.Number, p.HeadSHA)
	if paths, ok := cached(s, &s.pullFiles, key); ok {
		return paths, nil
	}
	files, err := s.gh.PullFiles(ctx, p.Number)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, f := range files {
		if releaseYAMLRe.MatchString(f) {
			paths = append(paths, f)
		}
	}
	sort.Strings(paths)
	remember(s, &s.pullFiles, key, paths)
	return paths, nil
}

// build reads the details of one release and derives its step. held is the
// cut run of the branch that waits for approval, if any.
func (s *Service) build(ctx context.Context, c candidate, cutRuns, releaseRuns []Run, audit []store.ReleaseAudit, held *Run) (Release, error) {
	rel := Release{
		ID: c.id, Branch: c.branch, HeadSHA: c.branchSHA, Dashboard: !c.fork,
		Units: []UnitState{}, Sync: []InstanceCard{}, Removals: []Removal{}, Actions: []AuditEntry{},
		CutUnits: []string{},
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
	// A held run counts while no cut of the branch started after it, and
	// until the PR is merged.
	waiting := held != nil && (f.CutRun == nil || newer(*held, *f.CutRun)) && (c.pull == nil || c.pull.MergedAt == nil)
	if waiting {
		rel.Cut = &RunRef{State: StateApproval, URL: held.HTMLURL}
	}

	var err error
	if c.pull != nil {
		rel.Units, err = s.pullUnits(ctx, c, generated)
	} else if c.branchSHA != "" {
		rel.Units, err = s.branchUnits(ctx, c, generated)
	}
	if err != nil {
		return rel, err
	}

	// The cut workflow does not open the PR; the dashboard does, once.
	if c.pull == nil && c.branchSHA != "" && pullable(rel.Units) && rel.Cut != nil && rel.Cut.State == StateSuccess {
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
			if err == nil && f.ReleaseRun == nil {
				// No run is named after the PR (a release made before the
				// run names, or by hand): it is done once every tag has a
				// GitHub release. A PR whose tags cannot be read is not shown.
				rel.finished, err = s.allReleased(ctx, rel.Units)
			}
		} else {
			err = s.readHead(ctx, &f, &rel)
		}
		if err != nil {
			return rel, err
		}
	}
	rel.Step = DeriveStep(f)
	if waiting {
		// Whatever the PR shows is from before the dispatch. The held run is
		// what the pending marker waited for.
		rel.Step = StepCutting
		if len(rel.Units) == 0 {
			rel.heldUnits = cutUnits(audit)
		}
	} else if !c.fork && c.branch != "" && (f.Pull == nil || f.Pull.MergedAt == nil) {
		// GitHub still shows the state from before the dispatch: nothing may
		// be decided on it.
		if p, ok := s.pendingCutOf(c.branch, cutRuns); ok {
			rel.Step, rel.Cut, rel.pending = StepCutting, &RunRef{State: StateQueued}, true
			if len(rel.Units) == 0 {
				rel.pendingUnits = p.units
			}
		}
	}

	for _, a := range audit {
		rel.Actions = append(rel.Actions, AuditEntry{At: a.At, Email: a.Email, Action: a.Action})
		if a.Action == auditCut && rel.StartedAt == nil {
			at := a.At
			rel.StartedBy, rel.StartedAt = a.Email, &at
		}
	}
	if len(rel.Units) == 0 && !c.fork {
		if u := cutUnits(audit); len(u) > 0 {
			rel.CutUnits = u
		}
	}
	return rel, nil
}

// cutUnits returns the unit roots of the newest cut or recut among the audit
// rows of a branch (oldest first) that recorded any.
func cutUnits(audit []store.ReleaseAudit) []string {
	for i := len(audit) - 1; i >= 0; i-- {
		if a := audit[i]; (a.Action == auditCut || a.Action == auditRecut) && strings.TrimSpace(a.Detail) != "" {
			return strings.Fields(a.Detail)
		}
	}
	return nil
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
			if r.WorkflowFile != WorkflowPR || r.HeadSHA != p.HeadSHA || needsApproval(r) {
				continue
			}
			if f.Jobs[r.ID], err = s.jobsOf(ctx, r); err != nil {
				return err
			}
		}
	}

	state, cards, syncRun := SyncCards(*f)
	rel.SyncState, rel.Sync = state, cards
	if syncRun != nil {
		rel.SyncURL, rel.syncRunDone = syncRun.HTMLURL, syncRun.Status == "completed"
		seen := map[Removal]bool{}
		// Per release project: was it populated on some instance?
		populated, order := map[string]bool{}, []string{}
		reported := 0
		for _, j := range f.Jobs[syncRun.ID] {
			inst, ok := InstanceOf(j.Name, JobPopulatePrefix)
			if !ok || isSkipped(j) || j.Status != "completed" {
				continue
			}
			rep := s.populateReport(ctx, *syncRun, j, inst)
			if len(rep.units) > 0 {
				reported++
			}
			skipped := 0
			for _, u := range rep.units {
				if _, known := populated[u.Project]; !known {
					populated[u.Project] = false
					order = append(order, u.Project)
				}
				switch u.Outcome {
				case "success":
					populated[u.Project] = true
				case "skipped":
					skipped++
				}
			}
			for i := range cards {
				if cards[i].Instance == inst {
					cards[i].Units = append(cards[i].Units, rep.units...)
					cards[i].NothingToSync = cards[i].State == StateSuccess && len(rep.units) > 0 && skipped == len(rep.units)
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
		// A unit is populated when every instance reports success or skipped
		// and at least one reports success. Only judged once every instance
		// has reported, so a missing report cannot make a unit look skipped
		// everywhere.
		if state == string(StateSuccess) && len(cards) > 0 && reported == len(cards) {
			for _, project := range order {
				if !populated[project] {
					f.Unpopulated = append(f.Unpopulated, project)
				}
			}
			if len(f.Unpopulated) > 0 {
				rel.SyncState = string(StateFailure)
				verb := "was"
				if len(f.Unpopulated) > 1 {
					verb = "were"
				}
				rel.SyncError = strings.Join(f.Unpopulated, ", ") + " " + verb + " not populated on any instance."
			}
		}
	}

	qaLabel := hasLabel(p, LabelQA)
	if qaLabel {
		if run := QARun(*f); run != nil {
			for _, j := range f.Jobs[run.ID] {
				if !isLane(j) {
					continue
				}
				// A lane without its Jenkins link falls back to the job page.
				if anns, err := s.annotationsOf(ctx, j); err == nil {
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
	jobs, err := s.jobsOf(ctx, *f.ReleaseRun)
	if err != nil {
		return err
	}
	f.ReleaseJobs = jobs
	rel.Final = FinalStatus(*f)
	rel.finalRunDone = f.ReleaseRun.Status == "completed"
	for _, u := range rel.Units {
		if u.Tag != "" {
			rel.Final.Tags = append(rel.Final.Tags, u.Tag)
		}
	}
	return nil
}

// releasePages bounds how far allReleased reads the GitHub releases.
const releasePages, releasesPerPage = 3, 100

// allReleased reports whether every unit's tag has a GitHub release. It is
// true when no unit has a tag. A tag with a release is kept for good; one
// without is asked for again.
func (s *Service) allReleased(ctx context.Context, units []UnitState) (bool, error) {
	var want []string
	for _, u := range units {
		if u.Tag != "" {
			want = append(want, u.Tag)
		}
	}
	missing := func() int {
		s.cacheMu.Lock()
		defer s.cacheMu.Unlock()
		n := 0
		for _, t := range want {
			if _, ok := s.releasedTags[t]; !ok {
				n++
			}
		}
		return n
	}
	for page := 1; missing() > 0 && page <= releasePages; page++ {
		rs, err := s.gh.Releases(ctx, page, releasesPerPage)
		if err != nil {
			return false, err
		}
		s.cacheMu.Lock()
		for _, r := range rs {
			if !r.PublishedAt.IsZero() { // a draft is not published
				s.releasedTags[r.Tag] = struct{}{}
			}
		}
		s.cacheMu.Unlock()
		if len(rs) < releasesPerPage {
			break
		}
	}
	return missing() == 0, nil
}

// populateReport returns what a finished populate job reported: the projects
// that failed, the removals the release would make, and the job's error
// annotations when it failed. A missing artifact leaves the lists empty.
func (s *Service) populateReport(ctx context.Context, run Run, j Job, instance string) jobReport {
	rep, ok := cached(s, &s.reports, j.ID)
	if ok {
		return rep
	}
	complete := true
	data, err := s.artifactOf(ctx, run, ArtifactReportPrefix+instance)
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
				// "skipped": the instance holds nothing of this project.
				// Anything unknown is shown as a failure.
				outcome := e.Outcome
				if outcome != "success" && outcome != "skipped" {
					outcome = "failure"
				}
				rep.units = append(rep.units, CardUnit{Project: e.Project, Outcome: outcome})
				for _, item := range e.Removals {
					rep.removals = append(rep.removals, Removal{Project: e.Project, Item: item})
				}
			}
		}
	}
	if jobState(j) == StateFailure {
		anns, err := s.annotationsOf(ctx, j)
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
		remember(s, &s.reports, j.ID, rep)
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
	if sections, ok := cached(s, &s.generated, run.ID); ok {
		return sections
	}
	data, err := s.artifactOf(ctx, *run, ArtifactChangelogs)
	if err != nil {
		return nil
	}
	sections := map[string]string{}
	if json.Unmarshal(data, &sections) != nil {
		sections = map[string]string{}
	}
	remember(s, &s.generated, run.ID, sections)
	return sections
}

// pullUnits returns the units of a PR: one per release.yaml it changes. A
// file that is missing or unusable leaves the unit with what its path tells.
// For a fork no failed read fails the refresh: the content of a fork is not
// ours to depend on.
func (s *Service) pullUnits(ctx context.Context, c candidate, generated map[string]string) ([]UnitState, error) {
	p := c.pull
	// Read by commit where there is one: the answer is kept. A fork's branch
	// name means nothing in this repository, and the branch of a merged PR is
	// deleted.
	var refs []fileRef
	switch {
	case c.fork:
		refs = []fileRef{{p.HeadSHA, true}}
	case p.MergedAt != nil:
		refs = []fileRef{{p.MergeSHA, true}, {p.HeadSHA, true}}
	default:
		refs = []fileRef{{p.HeadSHA, true}, {p.HeadRef, false}}
	}
	units := make([]UnitState, 0, len(c.paths))
	for _, yamlPath := range c.paths {
		var data []byte
		var ref fileRef
		for _, r := range refs {
			if r.name == "" {
				continue
			}
			b, err := s.fileAt(ctx, yamlPath, r)
			if err == nil {
				data, ref = b, r
				break
			}
			if !unusable(err) && !c.fork {
				return nil, err
			}
		}
		u, err := s.unitState(ctx, yamlPath, ref, data, generated, c.fork)
		if err != nil {
			return nil, err
		}
		units = append(units, u)
	}
	return units, nil
}

// branchUnits returns the units of a branch that has no PR: those whose
// release.yaml on the branch differs from main.
func (s *Service) branchUnits(ctx context.Context, c candidate, generated map[string]string) ([]UnitState, error) {
	units := []UnitState{}
	main, err := s.mainUnits(ctx)
	if err != nil {
		if soft(err) {
			return units, nil
		}
		return nil, err
	}
	refs := []fileRef{{c.branchSHA, true}, {c.branch, false}}
	for _, m := range main {
		yamlPath := path.Join(m.def.ReleaseDir, "release.yaml")
		var data []byte
		var ref fileRef
		found := false
		for _, r := range refs {
			b, err := s.fileAt(ctx, yamlPath, r)
			if err == nil {
				data, ref, found = b, r, true
				break
			}
			if !unusable(err) {
				return nil, err
			}
		}
		if !found || string(data) == string(m.yaml) {
			continue
		}
		u, err := s.unitState(ctx, yamlPath, ref, data, generated, false)
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
// the path tells is filled in. A changelog that is missing or unusable leaves
// the text empty; with lenient set, so does any failed read.
func (s *Service) unitState(ctx context.Context, yamlPath string, ref fileRef, data []byte, generated map[string]string, lenient bool) (UnitState, error) {
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
	changelog, err := s.fileAt(ctx, path.Join(dir, "CHANGELOG.md"), ref)
	switch {
	case err == nil:
		if section, err := ExtractSection(changelog, u.ReleaseID); err == nil {
			u.Changelog = section
		}
	case !unusable(err) && !lenient:
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

// pullable reports whether a PR can be opened for units: there is at least
// one, and each has a release id and some changelog text.
func pullable(units []UnitState) bool {
	if len(units) == 0 {
		return false
	}
	for _, u := range units {
		text := u.Changelog
		if u.Generated != nil {
			text = *u.Generated
		}
		if u.ReleaseID == "" || strings.TrimSpace(text) == "" {
			return false
		}
	}
	return true
}

// soft reports whether err means "not there or not usable" rather than a
// failed GitHub call.
func soft(err error) bool {
	return unusable(err) || errors.Is(err, errUnitsFile)
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
		case !unusable(err):
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
	held := map[string]string{} // unit -> release whose cut waits for approval
	for _, r := range snap.Releases {
		for _, u := range r.Units {
			if u.Unit != "" {
				inFlight[u.Unit] = r.ID
			}
		}
		// A first cut dispatched a moment ago: its units are known from the
		// dispatch, GitHub does not show them yet.
		for _, u := range r.pendingUnits {
			inFlight[u] = r.ID
		}
		// A first cut that waits for approval: its units are known from the
		// audit log only.
		for _, u := range r.heldUnits {
			inFlight[u] = r.ID
			held[u] = r.ID
		}
	}
	defs := make([]UnitDef, 0, len(main))
	last := map[string]string{}
	cutting := false // a cut is running and its units are not known yet
	for _, r := range snap.Releases {
		if r.Step == StepCutting && len(r.Units) == 0 && len(r.pendingUnits) == 0 && r.Cut != nil &&
			(r.Cut.State == StateQueued || r.Cut.State == StateRunning) {
			cutting = true
		}
		// A unit whose identity could not be read still names its release
		// project.
		for _, u := range r.Units {
			for _, m := range main {
				if u.ReleaseProject != "" && u.ReleaseProject == m.def.ReleaseProject {
					inFlight[m.def.Unit] = r.ID
				}
			}
		}
	}
	for _, m := range main {
		defs = append(defs, m.def)
		if n := len(m.tags); n > 0 {
			last[m.def.Unit] = ReleaseID(m.tags[n-1])
		}
	}
	rows, err := BuildUnitRows(defs, s.pkgs, inFlight, last)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if b := rows[i].Blocked; b != nil && b.Reason == "in_flight" && b.ReleaseID != "" && b.ReleaseID == held[rows[i].Unit] {
			b.Text = "Release " + b.ReleaseID + " is waiting for approval on GitHub."
		}
	}
	if cutting {
		// Fail closed: the cut has not said which units it takes.
		for i := range rows {
			rows[i].Tickable = false
			if rows[i].Blocked == nil || rows[i].Blocked.Reason != "in_flight" {
				rows[i].Blocked = &Blocked{Reason: "in_flight", Text: "A release is being cut. Try again in a minute."}
			}
		}
	}
	return rows, nil
}

// History returns one page (1-based, 20 GitHub releases per page) of
// finished releases, newest first, and whether a further page may exist.
// GitHub releases whose tag no unit records are left out, so a page can hold
// fewer than 20 rows, or none, with more pages still to come: "more" is told
// from the GitHub page, not from the rows.
func (s *Service) History(ctx context.Context, page int) ([]HistoryRow, bool, error) {
	if page < 1 {
		page = 1
	}
	published, err := s.gh.Releases(ctx, page, historyPerPage)
	if err != nil {
		return nil, false, err
	}
	more := len(published) >= historyPerPage
	main, err := s.mainUnits(ctx)
	if err != nil {
		return nil, false, err
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
			return nil, false, err
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
			return nil, false, fmt.Errorf("release audit: %w", err)
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
	return rows, more, nil
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
