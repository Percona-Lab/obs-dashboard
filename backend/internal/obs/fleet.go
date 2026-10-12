package obs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// Fleet fans OBS calls out across every configured instance. Callers speak
// logical project names; Fleet translates them per instance, merges or
// routes each call, and stamps results with the instance slug.
type Fleet struct {
	instances []*Instance
	bySlug    map[string]*Instance

	mu      sync.RWMutex
	members map[string]map[string]bool // logical project → hosting slugs
	owners  map[string]string          // logical project + "\x00" + repo → slug
}

// NewFleet builds a Fleet over instances, in config order.
func NewFleet(instances ...*Instance) *Fleet {
	f := &Fleet{
		instances: instances,
		bySlug:    make(map[string]*Instance, len(instances)),
		members:   map[string]map[string]bool{},
		owners:    map[string]string{},
	}
	for _, in := range instances {
		f.bySlug[in.Slug] = in
	}
	return f
}

// SingleFleet wraps one client as the legacy openSUSE instance in identity
// mode: logical names equal instance names.
func SingleFleet(c *Client, root string) *Fleet {
	return NewFleet(LegacyInstance(c, root))
}

var fallbackInstance = LegacyInstance(nil, "")

func (f *Fleet) Instances() []*Instance { return f.instances }

// Default returns the first configured instance.
func (f *Fleet) Default() *Instance { return f.InstanceOrDefault("") }

// Instance returns the instance with slug, or nil.
func (f *Fleet) Instance(slug string) *Instance { return f.bySlug[slug] }

// InstanceOrDefault returns the instance with slug, else the first
// configured instance. Safe on a nil Fleet (tests with no OBS).
func (f *Fleet) InstanceOrDefault(slug string) *Instance {
	if f == nil || len(f.instances) == 0 {
		return fallbackInstance
	}
	if in := f.bySlug[slug]; in != nil {
		return in
	}
	return f.instances[0]
}

// ImageBase and PackageURL let the CVE scanner resolve URLs by slug.
func (f *Fleet) ImageBase(slug, project, repo, name string) string {
	return f.InstanceOrDefault(slug).ImageBase(project, repo, name)
}

func (f *Fleet) PackageURL(slug, project, pkg string) string {
	return f.InstanceOrDefault(slug).PackageURL(project, pkg)
}

// ── membership ──

// SetMembers replaces the set of projects slug hosts.
func (f *Fleet) SetMembers(slug string, projects []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for p, set := range f.members {
		delete(set, slug)
		if len(set) == 0 {
			delete(f.members, p)
		}
	}
	for _, p := range projects {
		f.addMemberLocked(slug, p)
	}
}

func (f *Fleet) addMemberLocked(slug, project string) {
	set := f.members[project]
	if set == nil {
		set = map[string]bool{}
		f.members[project] = set
	}
	set[slug] = true
}

func (f *Fleet) AddMember(slug, project string) {
	f.mu.Lock()
	f.addMemberLocked(slug, project)
	f.mu.Unlock()
}

func (f *Fleet) RemoveMember(slug, project string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if set := f.members[project]; set != nil {
		delete(set, slug)
		if len(set) == 0 {
			delete(f.members, project)
		}
	}
}

// IsHosted reports whether any instance is known to host project.
func (f *Fleet) IsHosted(project string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.members[project]) > 0
}

// hosts returns the instances hosting project in config order, or every
// instance when membership is unknown.
func (f *Fleet) hosts(project string) []*Instance {
	f.mu.RLock()
	set := f.members[project]
	f.mu.RUnlock()
	if len(set) == 0 {
		return f.instances
	}
	out := make([]*Instance, 0, len(set))
	for _, in := range f.instances {
		if set[in.Slug] {
			out = append(out, in)
		}
	}
	return out
}

// ── repo owners ──

func ownerKey(project, repo string) string { return project + "\x00" + repo }

func (f *Fleet) SetOwner(project, repo, slug string) {
	f.mu.Lock()
	f.owners[ownerKey(project, repo)] = slug
	f.mu.Unlock()
}

// Owner returns the instance that builds repo for project, or nil.
func (f *Fleet) Owner(project, repo string) *Instance {
	f.mu.RLock()
	slug := f.owners[ownerKey(project, repo)]
	f.mu.RUnlock()
	return f.bySlug[slug]
}

// SeedOwners records repo owners and project membership from stored
// targets (startup). Seeded membership makes an instance that is down at
// startup still count as a host of its projects, so its targets are carried
// forward rather than dropped; Discover's SetMembers replaces it once the
// instance answers.
func (f *Fleet) SeedOwners(pkgs []*model.Package) {
	for _, p := range pkgs {
		for _, t := range p.Targets {
			if t.Instance != "" && f.bySlug[t.Instance] != nil {
				f.SetOwner(p.Project, t.Repo, t.Instance)
				f.AddMember(t.Instance, p.Project)
			}
		}
	}
}

func (f *Fleet) ownerFor(ctx context.Context, project, repo, pkg string) (*Instance, error) {
	if in := f.Owner(project, repo); in != nil {
		return in, nil
	}
	if len(f.instances) == 1 {
		return f.instances[0], nil
	}
	if hs := f.hosts(project); len(hs) == 1 {
		return hs[0], nil
	}
	if pkg != "" {
		_, _, _ = f.PackageBuildResults(ctx, project, pkg)
	} else {
		_, _, _, _ = f.BuildResults(ctx, project)
	}
	if in := f.Owner(project, repo); in != nil {
		return in, nil
	}
	return nil, fmt.Errorf("no OBS instance owns repo %s of %s", repo, project)
}

// ── fan-out helpers ──

type instResult[T any] struct {
	inst *Instance
	val  T
	err  error
}

func fanOut[T any](insts []*Instance, fn func(*Instance) (T, error)) []instResult[T] {
	out := make([]instResult[T], len(insts))
	if len(insts) == 1 {
		v, err := fn(insts[0])
		out[0] = instResult[T]{insts[0], v, err}
		return out
	}
	var wg sync.WaitGroup
	for i, in := range insts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := fn(in)
			out[i] = instResult[T]{in, v, err}
		}()
	}
	wg.Wait()
	return out
}

// settle records health and splits results into successes and the slugs of
// failed instances. 404 means "not hosted there" and counts as neither. err
// is non-nil only when nothing succeeded: the joined failures, or the
// NotFound error when every host answered 404.
func settle[T any](res []instResult[T]) (ok []instResult[T], failed []string, err error) {
	var errs []error
	var notFound error
	for _, r := range res {
		r.inst.observe(r.err)
		switch {
		case r.err == nil:
			ok = append(ok, r)
		case IsNotFound(r.err):
			notFound = r.err
		default:
			failed = append(failed, r.inst.Slug)
			errs = append(errs, fmt.Errorf("%s: %w", r.inst.Slug, r.err))
		}
	}
	if len(ok) == 0 {
		if len(errs) > 0 {
			err = errors.Join(errs...)
		} else {
			err = notFound
		}
	}
	return ok, failed, err
}

// ── merged calls ──

type buildResultsVal struct {
	states     []PackageBuildState
	repoStates map[string]string
}

// BuildResults merges one project's _result across hosting instances.
// failed lists instances that errored (their targets must be carried forward).
func (f *Fleet) BuildResults(ctx context.Context, project string) ([]PackageBuildState, map[string]string, []string, error) {
	res := fanOut(f.hosts(project), func(in *Instance) (buildResultsVal, error) {
		s, r, err := in.Client.BuildResults(ctx, in.ToInstance(project))
		return buildResultsVal{s, r}, err
	})
	ok, failed, err := settle(res)
	if err != nil {
		return nil, nil, failed, err
	}
	var states []PackageBuildState
	repoStates := map[string]string{}
	for _, r := range ok {
		states = append(states, f.stamp(project, r.inst, r.val.states)...)
		for k, v := range r.val.repoStates {
			repoStates[k] = v
			if repo, _, found := cutLast(k, "/"); found {
				f.SetOwner(project, repo, r.inst.Slug)
			}
		}
	}
	return states, repoStates, failed, nil
}

// PackageBuildResults merges one package's build states across instances.
func (f *Fleet) PackageBuildResults(ctx context.Context, project, pkg string) ([]PackageBuildState, []string, error) {
	res := fanOut(f.hosts(project), func(in *Instance) ([]PackageBuildState, error) {
		return in.Client.PackageBuildResults(ctx, in.ToInstance(project), pkg)
	})
	ok, failed, err := settle(res)
	if err != nil {
		return nil, failed, err
	}
	var states []PackageBuildState
	for _, r := range ok {
		states = append(states, f.stamp(project, r.inst, r.val)...)
	}
	return states, failed, nil
}

func (f *Fleet) stamp(project string, in *Instance, states []PackageBuildState) []PackageBuildState {
	out := make([]PackageBuildState, len(states))
	for i, s := range states {
		s.Project = project
		s.Instance = in.Slug
		f.SetOwner(project, s.Repo, in.Slug)
		out[i] = s
	}
	return out
}

func cutLast(s, sep string) (before, after string, found bool) {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}

func mergeMaps(ok []instResult[map[string]string]) map[string]string {
	out := map[string]string{}
	for _, r := range ok {
		for k, v := range r.val {
			out[k] = v
		}
	}
	return out
}

func (f *Fleet) RepoPublishStates(ctx context.Context, project, pkg string) (map[string]string, error) {
	ok, _, err := settle(fanOut(f.hosts(project), func(in *Instance) (map[string]string, error) {
		return in.Client.RepoPublishStates(ctx, in.ToInstance(project), pkg)
	}))
	if err != nil {
		return nil, err
	}
	return mergeMaps(ok), nil
}

func (f *Fleet) PackageBlockedReasons(ctx context.Context, project, pkg string) (map[string]string, error) {
	ok, _, err := settle(fanOut(f.hosts(project), func(in *Instance) (map[string]string, error) {
		return in.Client.PackageBlockedReasons(ctx, in.ToInstance(project), pkg)
	}))
	if err != nil {
		return nil, err
	}
	return mergeMaps(ok), nil
}

// ProjectBinaryList merges binary lists, translating each binary's project
// to its logical name and recording repo owners.
func (f *Fleet) ProjectBinaryList(ctx context.Context, project string) ([]BinaryArtifact, error) {
	ok, _, err := settle(fanOut(f.hosts(project), func(in *Instance) ([]BinaryArtifact, error) {
		return in.Client.ProjectBinaryList(ctx, in.ToInstance(project))
	}))
	if err != nil {
		return nil, err
	}
	var out []BinaryArtifact
	for _, r := range ok {
		for _, b := range r.val {
			if l, isIn := r.inst.ToLogical(b.Project); isIn {
				b.Project = l
			} else {
				b.Project = project
			}
			b.Instance = r.inst.Slug
			f.SetOwner(b.Project, b.Repo, r.inst.Slug)
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *Fleet) ProjectRepos(ctx context.Context, project string) ([]string, error) {
	ok, _, err := settle(fanOut(f.hosts(project), func(in *Instance) ([]string, error) {
		return in.Client.ProjectRepos(ctx, in.ToInstance(project))
	}))
	if err != nil {
		return nil, err
	}
	return unionStrings(ok, nil), nil
}

// SearchProjects returns the logical names of projects under prefix on
// every instance (deduplicated, first-seen order).
func (f *Fleet) SearchProjects(ctx context.Context, prefix string) ([]string, error) {
	ok, _, err := settle(fanOut(f.instances, func(in *Instance) ([]string, error) {
		return in.Client.SearchProjects(ctx, in.ToInstance(prefix))
	}))
	if err != nil {
		return nil, err
	}
	return unionStrings(ok, func(in *Instance, p string) (string, bool) { return in.ToLogical(p) }), nil
}

func unionStrings(ok []instResult[[]string], translate func(*Instance, string) (string, bool)) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range ok {
		for _, s := range r.val {
			if translate != nil {
				var in bool
				if s, in = translate(r.inst, s); !in {
					continue
				}
			}
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// Discover lists every instance's projects under its root, rebuilds
// membership from the instances that answered, and returns the union of
// logical names plus the set of instances that answered. It errors only
// when no instance answered.
func (f *Fleet) Discover(ctx context.Context) ([]string, map[string]bool, error) {
	res := fanOut(f.instances, func(in *Instance) ([]string, error) {
		return in.Client.SearchProjects(ctx, in.Root)
	})
	answered := map[string]bool{}
	seen := map[string]bool{}
	var out []string
	var errs []error
	for _, r := range res {
		r.inst.observe(r.err)
		if r.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.inst.Slug, r.err))
			continue
		}
		answered[r.inst.Slug] = true
		var logical []string
		for _, p := range r.val {
			l, in := r.inst.ToLogical(p)
			if !in {
				continue
			}
			logical = append(logical, l)
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
		f.SetMembers(r.inst.Slug, logical)
	}
	if len(answered) == 0 {
		return nil, nil, errors.Join(errs...)
	}
	return out, answered, nil
}

// ── routed calls (repo owner) ──

func (f *Fleet) Rebuild(ctx context.Context, project, repo, arch, pkg string) error {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return err
	}
	err = in.Client.Rebuild(ctx, in.ToInstance(project), repo, arch, pkg)
	in.observe(err)
	return err
}

func (f *Fleet) PackageBuildReason(ctx context.Context, project, repo, arch, pkg string) (BuildReasonResult, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return BuildReasonResult{}, err
	}
	r, err := in.Client.PackageBuildReason(ctx, in.ToInstance(project), repo, arch, pkg)
	in.observe(err)
	return r, err
}

func (f *Fleet) PackageContainerInfoFilename(ctx context.Context, project, repo, arch, pkg string) (string, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return "", err
	}
	s, err := in.Client.PackageContainerInfoFilename(ctx, in.ToInstance(project), repo, arch, pkg)
	in.observe(err)
	return s, err
}

func (f *Fleet) PackageContainerTags(ctx context.Context, project, repo, arch, pkg, filename string) ([]string, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return nil, err
	}
	tags, err := in.Client.PackageContainerTags(ctx, in.ToInstance(project), repo, arch, pkg, filename)
	in.observe(err)
	return tags, err
}

func (f *Fleet) PackageBinaries(ctx context.Context, project, repo, arch, pkg string) ([]string, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return nil, err
	}
	out, err := in.Client.PackageBinaries(ctx, in.ToInstance(project), repo, arch, pkg)
	in.observe(err)
	return out, err
}

func (f *Fleet) RepoBinaryVersions(ctx context.Context, project, repo, arch string) (map[string]string, error) {
	in, err := f.ownerFor(ctx, project, repo, "")
	if err != nil {
		return nil, err
	}
	out, err := in.Client.RepoBinaryVersions(ctx, in.ToInstance(project), repo, arch)
	in.observe(err)
	return out, err
}

func (f *Fleet) BuildLog(ctx context.Context, project, repo, arch, pkg string, tailBytes int) (string, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return "", err
	}
	out, err := in.Client.BuildLog(ctx, in.ToInstance(project), repo, arch, pkg, tailBytes)
	in.observe(err)
	return out, err
}

func (f *Fleet) PackageHistory(ctx context.Context, project, repo, arch, pkg string) ([]HistoryEntry, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return nil, err
	}
	out, err := in.Client.PackageHistory(ctx, in.ToInstance(project), repo, arch, pkg)
	in.observe(err)
	return out, err
}

func (f *Fleet) BuildDepInfo(ctx context.Context, project, repo, arch string) ([]DepInfo, error) {
	in, err := f.ownerFor(ctx, project, repo, "")
	if err != nil {
		return nil, err
	}
	out, err := in.Client.BuildDepInfo(ctx, in.ToInstance(project), repo, arch)
	in.observe(err)
	return out, err
}

func (f *Fleet) ProjectRepoArchs(ctx context.Context, project, repo string) ([]string, error) {
	in, err := f.ownerFor(ctx, project, repo, "")
	if err != nil {
		return nil, err
	}
	out, err := in.Client.ProjectRepoArchs(ctx, in.ToInstance(project), repo)
	in.observe(err)
	return out, err
}

func (f *Fleet) ProjectRepoPackages(ctx context.Context, project, repo, arch string) ([]string, error) {
	in, err := f.ownerFor(ctx, project, repo, "")
	if err != nil {
		return nil, err
	}
	out, err := in.Client.ProjectRepoPackages(ctx, in.ToInstance(project), repo, arch)
	in.observe(err)
	return out, err
}

// ── first hosting instance wins ──

// PackageIsContainer reports true as soon as any host says so. The client
// maps 404 to (false, nil), so a false answer may only mean "not hosted
// there": it cannot outrank a real failure on another host, which is
// returned so callers retry rather than settle on "not a container".
func (f *Fleet) PackageIsContainer(ctx context.Context, project, pkg string) (bool, error) {
	var errs firstHostErrors
	for _, in := range f.hosts(project) {
		v, err := in.Client.PackageIsContainer(ctx, in.ToInstance(project), pkg)
		in.observe(err)
		if err == nil && v {
			return true, nil
		}
		errs.add(err)
	}
	return false, errs.real
}

// firstHostErrors keeps the first real (non-404) failure and the last 404
// across a first-host-wins loop, so a real failure on one host is never
// masked by another host answering 404.
type firstHostErrors struct{ real, notFound error }

func (e *firstHostErrors) add(err error) {
	switch {
	case err == nil:
	case IsNotFound(err):
		e.notFound = err
	case e.real == nil:
		e.real = err
	}
}

func (e *firstHostErrors) err() error {
	if e.real != nil {
		return e.real
	}
	return e.notFound
}

// PackageVersionResult returns the first non-empty versrel in config order.
// AggregateVersionResult resolves the versrel of an aggregate package by
// following its _aggregate link on the same host: the source project named
// in the file is already an instance-local name, so it is queried directly.
// Returns "" with nil error when the package is not an aggregate or the
// source has no built target.
func (f *Fleet) AggregateVersionResult(ctx context.Context, project, pkg string) (string, error) {
	var errs firstHostErrors
	for _, in := range f.hosts(project) {
		srcProject, srcPkg, ok, err := in.Client.PackageAggregateSource(ctx, in.ToInstance(project), pkg)
		in.observe(err)
		if err != nil {
			errs.add(err)
			continue
		}
		if !ok {
			continue
		}
		v, err := in.Client.PackageVersionResult(ctx, srcProject, srcPkg)
		in.observe(err)
		if err != nil {
			errs.add(err)
			continue
		}
		return v, nil
	}
	return "", errs.real
}

func (f *Fleet) PackageVersionResult(ctx context.Context, project, pkg string) (string, error) {
	var lastErr error
	succeeded := false
	for _, in := range f.hosts(project) {
		v, err := in.Client.PackageVersionResult(ctx, in.ToInstance(project), pkg)
		in.observe(err)
		if err != nil {
			lastErr = err
			continue
		}
		succeeded = true
		if v != "" {
			return v, nil
		}
	}
	if succeeded {
		return "", nil
	}
	return "", lastErr
}

func (f *Fleet) SourceHistory(ctx context.Context, project, pkg string) ([]SourceCommit, error) {
	var errs firstHostErrors
	for _, in := range f.hosts(project) {
		v, err := in.Client.SourceHistory(ctx, in.ToInstance(project), pkg)
		in.observe(err)
		if err == nil {
			return v, nil
		}
		errs.add(err)
	}
	return nil, errs.err()
}

// ── publish flags ──

// ProjectPublishFlags returns flags whose Publishes(repo) follows the repo
// owner's instance. Any non-404 failure errors (callers then treat flags
// as unknown and keep polling).
func (f *Fleet) ProjectPublishFlags(ctx context.Context, project string) (PublishFlags, error) {
	hs := f.hosts(project)
	if len(hs) == 1 {
		fl, err := hs[0].Client.ProjectPublishFlags(ctx, hs[0].ToInstance(project))
		hs[0].observe(err)
		return fl, err
	}
	ok, failed, err := settle(fanOut(hs, func(in *Instance) (PublishFlags, error) {
		return in.Client.ProjectPublishFlags(ctx, in.ToInstance(project))
	}))
	if err != nil {
		return PublishFlags{}, err
	}
	if len(failed) > 0 {
		return PublishFlags{}, fmt.Errorf("publish flags unavailable on %v", failed)
	}
	delegate := make(map[string]PublishFlags, len(ok))
	for _, r := range ok {
		delegate[r.inst.Slug] = r.val
	}
	return PublishFlags{
		delegate: delegate,
		owner: func(repo string) string {
			if in := f.Owner(project, repo); in != nil {
				return in.Slug
			}
			return ""
		},
	}, nil
}

// EvictPublishFlags drops project's cached flags on every instance.
func (f *Fleet) EvictPublishFlags(project string) {
	for _, in := range f.instances {
		if in.Client != nil {
			in.Client.EvictPublishFlags(in.ToInstance(project))
		}
	}
}

// ── metrics ──

// MetricsSnapshot sums per-operation request counts across instances.
func (f *Fleet) MetricsSnapshot() map[string]int64 {
	out := map[string]int64{}
	for _, in := range f.instances {
		if in.Client == nil {
			continue
		}
		for k, v := range in.Client.MetricsSnapshot() {
			out[k] += v
		}
	}
	return out
}

// LimiterStats sums limiter gauges; enabled when any instance limits.
func (f *Fleet) LimiterStats() LimiterStats {
	var out LimiterStats
	for _, in := range f.instances {
		if in.Client == nil {
			continue
		}
		ls := in.Client.LimiterStats()
		if !ls.Enabled {
			continue
		}
		out.Enabled = true
		out.Budget += ls.Budget
		out.Remaining += ls.Remaining
		out.Waits += ls.Waits
	}
	return out
}

func (f *Fleet) RatePerSecond() float64 {
	var total float64
	for _, in := range f.instances {
		if in.Client != nil {
			total += in.Client.RatePerSecond()
		}
	}
	return total
}

// InstanceStatus is one instance's request totals, limiter and health.
type InstanceStatus struct {
	Name    string       `json:"name"`
	Slug    string       `json:"slug"`
	Total   int64        `json:"total"`
	ReqPerS float64      `json:"req_per_s"`
	Limiter LimiterStats `json:"limiter"`
	Health  Health       `json:"health"`
}

func (f *Fleet) Statuses() []InstanceStatus {
	out := make([]InstanceStatus, 0, len(f.instances))
	for _, in := range f.instances {
		st := InstanceStatus{Name: in.Name, Slug: in.Slug, Health: in.Health()}
		if in.Client != nil {
			for _, v := range in.Client.MetricsSnapshot() {
				st.Total += v
			}
			st.ReqPerS = in.Client.RatePerSecond()
			st.Limiter = in.Client.LimiterStats()
		}
		out = append(out, st)
	}
	return out
}

// ── health watch ──

func (f *Fleet) healthBaseline() map[string]bool {
	last := make(map[string]bool, len(f.instances))
	for _, in := range f.instances {
		last[in.Slug] = in.Health().OK
	}
	return last
}

func (f *Fleet) checkHealth(last map[string]bool, notify func(slug string, h Health)) {
	for _, in := range f.instances {
		h := in.Health()
		if h.OK != last[in.Slug] {
			last[in.Slug] = h.OK
			notify(in.Slug, h)
		}
	}
}

// RunHealthWatch checks every instance's health each interval and calls
// notify when an instance's OK flips. Blocks until ctx is cancelled.
func (f *Fleet) RunHealthWatch(ctx context.Context, every time.Duration, notify func(slug string, h Health)) {
	last := f.healthBaseline()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.checkHealth(last, notify)
		}
	}
}
