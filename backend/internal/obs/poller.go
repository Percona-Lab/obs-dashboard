package obs

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/workingset"
)

// PollGate lets the poller pause while no dashboard tab is watching.
// See internal/presence. A nil gate means "always poll".
type PollGate interface {
	Active() bool
	Subscribe() <-chan struct{}
}

// Poller periodically fetches OBS build results and reconciles them with the store.
type Poller struct {
	client   *Fleet
	db       *sql.DB
	interval time.Duration
	hub      *hubpkg.Hub
	ws       *workingset.WorkingSet
	gate     PollGate
	// releaseTagsAt records, per release project, when its published
	// containers were last queued for a tag refresh. In memory only, so a
	// restart refreshes every release project on the first tick.
	releaseTagsAt map[string]time.Time
}

// releaseTagsInterval is how often a published release container's tags are
// re-read. A new release into the same project changes no build state, so
// without this (and the MQ repo.published signal) the stored version would
// stay at the first one seen.
const releaseTagsInterval = 24 * time.Hour

func NewPoller(client *Fleet, db *sql.DB, interval time.Duration, h *hubpkg.Hub, ws *workingset.WorkingSet, gate PollGate) *Poller {
	return &Poller{client: client, db: db, interval: interval, hub: h, ws: ws, gate: gate, releaseTagsAt: map[string]time.Time{}}
}

// Run blocks until ctx is cancelled. It ticks immediately on first call
// (the presence gate's boot grace covers this window), skips interval
// ticks while the gate is idle, and ticks immediately on idle→active.
func (p *Poller) Run(ctx context.Context) {
	var wake <-chan struct{}
	if p.gate != nil {
		wake = p.gate.Subscribe()
	}
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	p.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if p.gate == nil || p.gate.Active() {
				p.tick(ctx)
			}
		case <-wake:
			p.tick(ctx)
		}
	}
}

// fetchProjectResults fetches one project's build results outside the
// background rate limiter: the discovery pass is bounded (one call per live
// project per interval) and must not queue behind working-set traffic.
func (p *Poller) fetchProjectResults(ctx context.Context, project string) ([]PackageBuildState, map[string]string, []string, error) {
	return p.client.BuildResults(Interactive(ctx), project)
}

func (p *Poller) tick(ctx context.Context) {
	projects, answered, err := p.client.Discover(ctx)
	if err != nil {
		slog.Error("poller: discover projects", "err", err)
		return
	}

	liveProjects := make(map[string]bool, len(projects))
	for _, proj := range projects {
		liveProjects[proj] = true
	}

	existing, err := store.QueryPackages(p.db, "")
	if err != nil {
		slog.Error("poller: query packages", "err", err)
		return
	}
	byKey := make(map[string]*model.Package, len(existing))
	for _, pkg := range existing {
		byKey[pkg.Project+"/"+pkg.Name] = pkg
	}

	for _, project := range projects {
		if ctx.Err() != nil {
			return
		}
		kind := Classify(project)
		if kind == KindUnknown {
			continue
		}

		results, repoStates, failed, err := p.fetchProjectResults(ctx, project)
		if err != nil {
			slog.Warn("poller: build results", "project", project, "err", err)
			continue
		}
		failed = withUndiscovered(failed, answered, results, p.client.Instances())
		down := make(map[string]bool, len(failed))
		for _, s := range failed {
			down[s] = true
		}

		byPkg := map[string][]PackageBuildState{}
		for _, r := range results {
			byPkg[r.Package] = append(byPkg[r.Package], r)
		}
		// Packages whose targets live on an unreachable instance stay in the
		// pass so their targets are carried forward rather than GC'd.
		for _, stored := range existing {
			if stored.Project != project {
				continue
			}
			if _, ok := byPkg[stored.Name]; ok {
				continue
			}
			for _, t := range stored.Targets {
				if down[t.Instance] {
					byPkg[stored.Name] = nil
					break
				}
			}
		}

		refreshReleaseTags := kind == KindRelease && time.Since(p.releaseTagsAt[project]) >= releaseTagsInterval
		if refreshReleaseTags {
			p.releaseTagsAt[project] = time.Now()
		}

		tags := ProjectTags(project)
		for pkgName, targets := range byPkg {
			key := project + "/" + pkgName
			prev := byKey[key]
			var prevTargets []model.Target
			if prev != nil {
				prevTargets = prev.Targets
			}
			pkg := buildPackage(project, pkgName, tags, CarryForward(targets, prevTargets, failed, project, pkgName))
			pkg.IsRelease = kind == KindRelease
			preservePackageEnrichment(prev, pkg)

			// Preserve published state: OBS build results only return "succeeded",
			// never "published". Without this guard the poller would flip a published
			// package back to succeeded every tick, causing a succeeded↔published
			// oscillation and spurious SSE broadcasts.
			if prev != nil && prev.RollupState == model.RollupPublished &&
				pkg.RollupState == model.RollupSucceeded && !targetsChanged(prev, pkg) {
				pkg.RollupState = model.RollupPublished
			}

			rollupChanged := prev == nil || prev.RollupState != pkg.RollupState
			tagsChanged := prev != nil && len(prev.Tags) != len(pkg.Tags)

			if kind.IsRealTime() {
				if rollupChanged || targetsChanged(prev, pkg) || tagsChanged {
					if err := store.UpsertPackageState(p.db, pkg, time.Now().UTC()); err != nil {
						slog.Error("poller: upsert package", "pkg", pkgName, "err", err)
						continue
					}
					p.hub.Notify(hubpkg.PackageUpdate(pkg))
					p.ws.Add(pkg)
				} else if awaitingPublishReady(prev, repoStates) {
					p.ws.Add(prev)
				}
			} else {
				// Release project: upsert silently — no SSE broadcast, no events.
				// Reset rollup to building if target set changed on an already-published package.
				if prev != nil && prev.RollupState == model.RollupPublished && targetsChanged(prev, pkg) {
					pkg.RollupState = model.RollupBuilding
				}
				if rollupChanged || targetsChanged(prev, pkg) || tagsChanged {
					if err := store.UpsertPackageState(p.db, pkg, time.Now().UTC()); err != nil {
						slog.Error("poller: upsert release package", "pkg", pkgName, "err", err)
						continue
					}
				}
				// Add to working set only if there is work remaining, or the
				// container's tags are due for their periodic re-read.
				isContainer := pkg.IsContainer != nil && *pkg.IsContainer
				containerNeedsTags := isContainer && (len(pkg.ContainerTags) == 0 || refreshReleaseTags)
				if pkg.RollupState != model.RollupPublished || pkg.IsContainer == nil || containerNeedsTags {
					p.ws.Add(pkg)
				}
			}
		}

		// Garbage-collect packages removed from this project in OBS.
		for _, stored := range existing {
			if stored.Project != project {
				continue
			}
			if _, live := byPkg[stored.Name]; !live {
				slog.Info("poller: removing stale package", "project", project, "pkg", stored.Name)
				if err := store.DeletePackage(p.db, project, stored.Name); err != nil {
					slog.Error("poller: delete stale package", "project", project, "pkg", stored.Name, "err", err)
				}
				p.ws.Remove(project + "/" + stored.Name)
			}
		}
	}

	// Garbage-collect packages for projects no longer in OBS — only when
	// every instance that owned them answered discovery.
	storedProjects := make(map[string]bool)
	for _, pkg := range existing {
		storedProjects[pkg.Project] = true
	}
	for proj := range storedProjects {
		if liveProjects[proj] || !confirmedGone(existing, proj, answered, p.client.Instances()) {
			continue
		}
		slog.Info("poller: removing packages for deleted project", "project", proj)
		if err := store.DeletePackagesByProject(p.db, proj); err != nil {
			slog.Error("poller: delete packages", "project", proj, "err", err)
		}
		p.client.EvictPublishFlags(proj)
		for _, pkg := range existing {
			if pkg.Project == proj {
				p.ws.Remove(proj + "/" + pkg.Name)
			}
		}
	}
}

// withUndiscovered adds to failed every instance that did not answer
// discovery this tick and returned no build states for the project. Such an
// instance's membership may be stale or empty (e.g. down since startup), so
// its absence from results must not read as "packages removed": its targets
// are carried forward and its packages are exempt from stale-package GC.
func withUndiscovered(failed []string, answered map[string]bool, results []PackageBuildState, all []*Instance) []string {
	if len(answered) >= len(all) {
		return failed
	}
	seen := make(map[string]bool, len(failed)+len(all))
	for _, s := range failed {
		seen[s] = true
	}
	for _, r := range results {
		seen[r.Instance] = true
	}
	out := append([]string(nil), failed...)
	for _, in := range all {
		if !answered[in.Slug] && !seen[in.Slug] {
			out = append(out, in.Slug)
		}
	}
	return out
}

// confirmedGone reports whether every instance that owned a target of
// project answered discovery. Unstamped targets and target-less packages
// require every instance to have answered.
func confirmedGone(existing []*model.Package, project string, answered map[string]bool, all []*Instance) bool {
	allAnswered := len(answered) >= len(all)
	for _, pkg := range existing {
		if pkg.Project != project {
			continue
		}
		if len(pkg.Targets) == 0 && !allAnswered {
			return false
		}
		for _, t := range pkg.Targets {
			if t.Instance == "" {
				if !allAnswered {
					return false
				}
				continue
			}
			if !answered[t.Instance] {
				return false
			}
		}
	}
	return true
}

func preservePackageEnrichment(prev, next *model.Package) {
	if prev == nil || next == nil {
		return
	}
	if next.IsContainer == nil {
		next.IsContainer = prev.IsContainer
	}
	if next.Version == "" {
		next.Version = prev.Version
	}
	if len(next.ContainerTags) == 0 {
		next.ContainerTags = prev.ContainerTags
	}
	if next.Trigger == nil {
		next.Trigger = prev.Trigger
	}
	// For containers whose builds are intentionally disabled (e.g. release
	// containers), the poller's buildPackage returns no active targets because
	// skipState filters them out. Preserve whatever targets ContainerTagsTask
	// stored so that arch info survives across poll ticks.
	if len(next.Targets) == 0 && len(prev.Targets) > 0 {
		allDisabled := true
		for _, t := range prev.Targets {
			if t.State != "disabled" {
				allDisabled = false
				break
			}
		}
		if allDisabled {
			next.Targets = prev.Targets
		}
	}
	if len(prev.Tags) > 0 {
		seen := make(map[string]bool, len(next.Tags)+len(prev.Tags))
		for _, tag := range next.Tags {
			seen[tag] = true
		}
		for _, tag := range prev.Tags {
			if !seen[tag] {
				next.Tags = append(next.Tags, tag)
			}
		}
	}

	prevTargets := make(map[string]model.Target, len(prev.Targets))
	for _, target := range prev.Targets {
		prevTargets[target.Repo+"/"+target.Arch] = target
	}
	for i := range next.Targets {
		prevTarget, ok := prevTargets[next.Targets[i].Repo+"/"+next.Targets[i].Arch]
		if !ok {
			continue
		}
		// Preserve Published unconditionally: a transient blocked (or any other
		// intermediate) state does not remove the published artifact from the OBS
		// repo. Without this, a brief state change resets Published=false and causes
		// a spurious "succeeded" event when the target re-publishes without having
		// actually rebuilt. BuildStateTask in the worker always re-derives Published
		// from fresh OBS data, so this only affects the ProcessOnce before-snapshot.
		if !next.Targets[i].Published && prevTarget.Published {
			next.Targets[i].Published = true
		}
		if prevTarget.State != next.Targets[i].State {
			continue
		}
		if next.Targets[i].Details == "" {
			next.Targets[i].Details = prevTarget.Details
		}
		if next.Targets[i].BlockedBy == "" {
			next.Targets[i].BlockedBy = prevTarget.BlockedBy
		}
		if next.Targets[i].BuildReason == "" {
			next.Targets[i].BuildReason = prevTarget.BuildReason
		}
		if len(next.Targets[i].BuildReasonPackages) == 0 {
			next.Targets[i].BuildReasonPackages = prevTarget.BuildReasonPackages
		}
	}
}

// targetsChanged returns true when any individual target state or detail differs
// between the stored package and the freshly-polled one. This catches transient
// state changes (e.g. succeeded→finished→succeeded) and late OBS details (e.g.
// finished with outcome succeeded) that don't alter the rollup.
func targetsChanged(prev *model.Package, next *model.Package) bool {
	if prev == nil {
		return true
	}
	if len(prev.Targets) != len(next.Targets) {
		return true
	}
	prevTargets := make(map[string]model.Target, len(prev.Targets))
	for _, t := range prev.Targets {
		prevTargets[t.Repo+"/"+t.Arch] = t
	}
	for _, t := range next.Targets {
		prevTarget, ok := prevTargets[t.Repo+"/"+t.Arch]
		if !ok || prevTarget.State != t.State || prevTarget.Details != t.Details {
			return true
		}
	}
	return false
}

// PRNumber extracts the PR number from a PR project path.
// Returns "" if the project is not a PR project.
// Example: "PR:pr-42:ppg17" → "42"
func PRNumber(project string) string {
	parts := strings.Split(project, ":")
	for i, p := range parts {
		if strings.EqualFold(p, "PR") && i+1 < len(parts) {
			prSegment := parts[i+1]
			return strings.TrimPrefix(strings.ToLower(prSegment), "pr-")
		}
	}
	return ""
}

// skipState returns true for OBS states that represent a build being intentionally
// off and should not contribute to the rollup or target counts.
func skipState(state string) bool {
	switch state {
	case "disabled", "excluded", "locked":
		return true
	}
	return false
}

// awaitingPublishReady reports whether stored has a succeeded target not yet
// observed as published whose repo·arch has reached the "published" state in
// the current _result response. Publication is invisible to the build-state
// diff (OBS reports "succeeded" forever), so this is the loss-tolerant
// fallback that re-adds parked awaiting-publish packages when the
// repo.published MQ event was missed — or never fired ("nothing changed"
// publish runs skip it). The wake pass lets PublishStateTask confirm and
// promote.
func awaitingPublishReady(stored *model.Package, repoStates map[string]string) bool {
	if stored == nil {
		return false
	}
	for _, t := range stored.Targets {
		if t.State == "succeeded" && !t.Published && repoStates[t.Repo+"/"+t.Arch] == "published" {
			return true
		}
	}
	return false
}

// buildPackage aggregates target states into a Package with worst-case rollup.
// Targets with state disabled/excluded/locked are silently dropped.
func buildPackage(project, name string, tags []string, targets []PackageBuildState) *model.Package {
	// Precedence from worst to best. finished/scheduled are transient in-progress
	// states and must appear before succeeded so they are not silently ignored.
	stateOrder := []model.RollupState{
		model.RollupBroken, model.RollupFailed, model.RollupUnresolvable,
		model.RollupBlocked, model.RollupBuilding, model.RollupFinished,
		model.RollupScheduled, model.RollupSucceeded,
	}
	stateSet := map[string]bool{}
	var active []PackageBuildState
	for _, t := range targets {
		if skipState(t.State) {
			continue
		}
		active = append(active, t)
		stateSet[t.State] = true
	}

	rollup := model.RollupSucceeded
	for _, s := range stateOrder {
		if stateSet[string(s)] {
			rollup = s
			break
		}
	}

	ok := 0
	mTargets := make([]model.Target, len(active))
	for i, t := range active {
		mTargets[i] = model.Target{Repo: t.Repo, Arch: t.Arch, State: t.State, Details: t.Details, Instance: t.Instance}
		if t.State == "succeeded" {
			ok++
		}
	}

	return &model.Package{
		Project:      project,
		Name:         name,
		Tags:         tags,
		RollupState:  rollup,
		OKTargets:    ok,
		TotalTargets: len(active),
		Targets:      mTargets,
		UpdatedAt:    time.Now().UTC(),
	}
}
