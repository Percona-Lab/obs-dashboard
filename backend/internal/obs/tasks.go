package obs

import (
	"context"
	"log/slog"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// withRetry calls fn up to maxAttempts times, sleeping an exponentially growing
// delay between failures. It stops immediately if the context is cancelled.
func withRetry(ctx context.Context, maxAttempts int, base time.Duration, fn func() error) error {
	delay := base
	var err error
	for attempt := range maxAttempts {
		err = fn()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if attempt < maxAttempts-1 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return err
			}
			delay *= 2
		}
	}
	return err
}

// blockedByTTL bounds BlockedBy staleness: while a target stays blocked, the
// blocker list evolves as dependencies finish, so a cached reason is refreshed
// once it is older than this. Constant by design (YAGNI on config).
const blockedByTTL = 5 * time.Minute

// BuildStateTask refreshes the package's targets, rollup state, and counts
// by fetching current build results from OBS for the specific package.
type BuildStateTask struct{}

func (t BuildStateTask) Run(ctx context.Context, client *Fleet, pkg *model.Package, env *Env) error {
	var results []PackageBuildState
	var failed []string
	if env != nil && env.BuildStates != nil {
		results = env.BuildStates
		failed = env.FailedInstances
	} else {
		var err error
		results, failed, err = client.PackageBuildResults(ctx, pkg.Project, pkg.Name)
		if err != nil {
			return err
		}
	}
	results = CarryForward(results, pkg.Targets, failed, pkg.Project, pkg.Name)
	down := make(map[string]bool, len(failed))
	for _, s := range failed {
		down[s] = true
	}
	updated := buildPackage(pkg.Project, pkg.Name, pkg.Tags, results)
	// Preserve per-target enrichment from prior passes only while the target's
	// state is unchanged; a state transition leaves the fields at their zero
	// values, forcing the downstream tasks to refetch. Also compute
	// TargetsStable: true only when the previous pass had the same target set
	// with identical states — all cold-start paths (no previous targets, MQ
	// replace, restart) yield false so downstream tasks fetch conservatively.
	stable := pkg.CacheWarm && len(pkg.Targets) > 0 && len(pkg.Targets) == len(updated.Targets)
	for i := range updated.Targets {
		matched := false
		for _, old := range pkg.Targets {
			if old.Repo == updated.Targets[i].Repo && old.Arch == updated.Targets[i].Arch {
				matched = true
				if old.State == updated.Targets[i].State {
					updated.Targets[i].BlockedBy = old.BlockedBy
					updated.Targets[i].BuildReason = old.BuildReason
					updated.Targets[i].BuildReasonPackages = old.BuildReasonPackages
					updated.Targets[i].BlockedByFetchedAt = old.BlockedByFetchedAt
					if down[updated.Targets[i].Instance] {
						// Carried forward: the instance is unreachable, so its
						// publish state can't be re-read this pass.
						updated.Targets[i].Published = old.Published
					}
				} else {
					stable = false
				}
				break
			}
		}
		if !matched {
			stable = false
		}
	}
	pkg.Targets = updated.Targets
	pkg.RollupState = updated.RollupState
	pkg.OKTargets = updated.OKTargets
	pkg.TotalTargets = updated.TotalTargets
	pkg.UpdatedAt = updated.UpdatedAt
	pkg.TargetsStable = stable
	pkg.CacheWarm = true
	return nil
}

// PublishStateTask sets Target.Published = true for succeeded targets whose
// repo state is "published" according to the OBS _result?view=status endpoint.
type PublishStateTask struct{}

func (t PublishStateTask) Run(ctx context.Context, client *Fleet, pkg *model.Package, env *Env) error {
	hasCandidate := false
	for _, target := range pkg.Targets {
		if target.State == "succeeded" && !target.Published {
			hasCandidate = true
			break
		}
	}
	if !hasCandidate {
		return nil
	}

	// Skip targets whose repo never publishes: their repo state stays
	// "unpublished" forever, so checking is futile. On flags error the zero
	// value publishes everything → conservative check (same as before).
	flags, _ := client.ProjectPublishFlags(ctx, pkg.Project)
	needsCheck := false
	for _, target := range pkg.Targets {
		if target.State == "succeeded" && !target.Published && flags.Publishes(target.Repo) {
			needsCheck = true
			break
		}
	}
	if !needsCheck {
		return nil
	}

	var states map[string]string
	if env != nil && env.RepoStates != nil {
		states = env.RepoStates
	} else {
		var err error
		states, err = client.RepoPublishStates(ctx, pkg.Project, pkg.Name)
		if err != nil {
			slog.Warn("obs: repo publish states", "pkg", pkg.Name, "err", err)
			return nil
		}
	}

	for i, target := range pkg.Targets {
		if target.State == "succeeded" && !target.Published {
			if states[target.Repo+"/"+target.Arch] == "published" {
				pkg.Targets[i].Published = true
			}
		}
	}

	// Promote to published when all active (non-skipped) targets are published.
	allPublished := true
	activeCount := 0
	for _, target := range pkg.Targets {
		switch target.State {
		case "disabled", "excluded", "locked":
			continue
		}
		activeCount++
		if target.State != "succeeded" || !target.Published {
			allPublished = false
			break
		}
	}
	if allPublished && activeCount > 0 {
		pkg.RollupState = model.RollupPublished
	}
	return nil
}

// BinariesCheckTask is used for release packages. It calls RepoPublishStates
// to detect when all repos have published binaries, then promotes rollup to
// RollupPublished. Unlike PublishStateTask it does not require targets to be
// in "succeeded" state first — release packages use OBS repo state directly.
type BinariesCheckTask struct{}

func (t BinariesCheckTask) Run(ctx context.Context, client *Fleet, pkg *model.Package, env *Env) error {
	if len(pkg.Targets) == 0 {
		return nil
	}
	var states map[string]string
	if env != nil && env.RepoStates != nil {
		states = env.RepoStates
	} else {
		var err error
		states, err = client.RepoPublishStates(ctx, pkg.Project, pkg.Name)
		if err != nil {
			slog.Warn("obs: binaries check repo states", "pkg", pkg.Name, "err", err)
			return nil
		}
	}

	for i, target := range pkg.Targets {
		if states[target.Repo+"/"+target.Arch] == "published" {
			pkg.Targets[i].Published = true
		}
	}

	// Promote to published when all active targets have binaries published.
	allPublished := true
	activeCount := 0
	for _, target := range pkg.Targets {
		switch target.State {
		case "disabled", "excluded", "locked":
			continue
		}
		activeCount++
		if !target.Published {
			allPublished = false
			break
		}
	}
	if allPublished && activeCount > 0 {
		pkg.RollupState = model.RollupPublished
	}
	return nil
}

// BlockedReasonTask populates BlockedBy on blocked targets.
type BlockedReasonTask struct{}

func (t BlockedReasonTask) Run(ctx context.Context, client *Fleet, pkg *model.Package, env *Env) error {
	needsFetch := false
	for _, target := range pkg.Targets {
		if target.State != "blocked" {
			continue
		}
		if target.BlockedBy == "" || time.Since(target.BlockedByFetchedAt) > blockedByTTL {
			needsFetch = true
			break
		}
	}
	if !needsFetch {
		return nil
	}

	reasons, err := client.PackageBlockedReasons(ctx, pkg.Project, pkg.Name)
	if err != nil {
		slog.Warn("obs: blocked reasons", "pkg", pkg.Name, "err", err)
		return nil
	}
	now := time.Now().UTC()
	for i, target := range pkg.Targets {
		if target.State != "blocked" {
			continue
		}
		reason := reasons[target.Repo+"/"+target.Arch]
		pkg.Targets[i].BlockedBy = reason
		if reason != "" {
			// Stamp only on value receipt: a blocked target whose details OBS
			// hasn't produced yet keeps retrying every pass (today's freshness).
			pkg.Targets[i].BlockedByFetchedAt = now
		}
	}
	return nil
}

// BuildReasonTask fetches the build trigger reason for non-succeeded targets.
type BuildReasonTask struct{}

func (t BuildReasonTask) Run(ctx context.Context, client *Fleet, pkg *model.Package, env *Env) error {
	if pkg.TargetsStable {
		// Negative-result caching: under stable targets every non-succeeded
		// target was already queried in this exact state — populated reasons
		// are current, and empty ones (e.g. unresolvable targets, which OBS
		// has no reason for) will stay empty until a state transition flips
		// TargetsStable off and refetches.
		return nil
	}
	for i, target := range pkg.Targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if target.State == "succeeded" {
			continue
		}
		if target.BuildReason != "" {
			// Cached for this build cycle: BuildStateTask wipes BuildReason on
			// any state transition, so a populated value is current. Targets
			// whose OBS _reason is legitimately empty keep fetching (no regression).
			continue
		}
		var result BuildReasonResult
		err := withRetry(ctx, 3, time.Second, func() error {
			var e error
			result, e = client.PackageBuildReason(ctx, pkg.Project, target.Repo, target.Arch, pkg.Name)
			return e
		})
		if err != nil {
			slog.Warn("obs: build reason",
				"pkg", pkg.Name,
				"repo", target.Repo,
				"arch", target.Arch,
				"err", err)
			continue
		}
		pkg.Targets[i].BuildReason = result.Explain
		pkg.Targets[i].BuildReasonPackages = result.Packages
	}
	return nil
}

// PackageTypeTask detects whether a package produces a container image by
// inspecting its source files. Sets pkg.IsContainer accordingly.
// Errors are logged and treated as non-fatal to preserve the existing value.
type PackageTypeTask struct{}

func (t PackageTypeTask) Run(ctx context.Context, client *Fleet, pkg *model.Package, env *Env) error {
	if pkg.IsContainer != nil {
		return nil
	}
	isContainer, err := client.PackageIsContainer(ctx, pkg.Project, pkg.Name)
	if err != nil {
		slog.Warn("obs: package type detection", "pkg", pkg.Name, "err", err)
		return nil
	}
	pkg.IsContainer = &isContainer
	return nil
}

// VersionTask fetches the latest versrel (e.g. "17.5-1") for RPM/DEB packages
// from the OBS _result?view=versrel endpoint. Skipped for confirmed container
// packages (which get their version from ContainerTagsTask instead).
// When IsContainer is nil (not yet detected), we run anyway — it is safe because
// the OBS endpoint returns an empty string for containers and the task is a no-op.
type VersionTask struct{}

func (t VersionTask) Run(ctx context.Context, client *Fleet, pkg *model.Package, env *Env) error {
	if pkg.IsContainer != nil && *pkg.IsContainer {
		return nil
	}
	if pkg.TargetsStable {
		// versrel only changes when a new build lands, which always transitions
		// target states. This also negative-caches never-built packages (empty
		// versrel) — they refetch only on a state transition.
		return nil
	}
	versrel, err := client.PackageVersionResult(ctx, pkg.Project, pkg.Name)
	if err != nil {
		slog.Warn("obs: version result", "pkg", pkg.Name, "err", err)
		return nil
	}
	if versrel == "" {
		// Aggregates (_aggregate links to e.g. ppg:common:deps) report no
		// versrel of their own; take the source package's instead.
		versrel, err = client.AggregateVersionResult(ctx, pkg.Project, pkg.Name)
		if err != nil {
			slog.Warn("obs: aggregate version result", "pkg", pkg.Name, "err", err)
			return nil
		}
	}
	if versrel == "" || versrel == pkg.Version {
		return nil
	}
	pkg.Version = versrel
	return nil
}

// ContainerTagsTask fetches all image tags (e.g. ["18.4-1-1.7", "18.4-1"])
// from the .containerinfo binary artifact. Skipped for non-container packages
// and packages with no targets. Sets pkg.Version to the first tag and
// pkg.ContainerTags to the full list.
type ContainerTagsTask struct{}

func (t ContainerTagsTask) Run(ctx context.Context, client *Fleet, pkg *model.Package, env *Env) error {
	if pkg.IsContainer == nil || !*pkg.IsContainer {
		return nil
	}
	if len(pkg.ContainerTags) > 0 && pkg.TargetsStable {
		// Image tags only change when a new build lands (same invariant as
		// versrel). The release chain has no BuildStateTask so TargetsStable is
		// never set there — release containers keep fetching, same as today.
		return nil
	}
	targets := pkg.Targets
	// Release containers have all builds intentionally disabled (to prevent
	// spurious rebuilds). OBS returns only "disabled" statuses, which the poller
	// filters out, leaving pkg.Targets empty. Fall back to querying OBS directly
	// so we can still discover the available repos/arches and fetch container tags.
	if len(targets) == 0 && pkg.IsRelease {
		results, _, err := client.PackageBuildResults(ctx, pkg.Project, pkg.Name)
		if err != nil {
			slog.Warn("obs: container tags: query release targets", "pkg", pkg.Name, "err", err)
			return nil
		}
		// Every repo of a release container is a base-image repo: the
		// legacy "images" repo or, in the per-base-image layout, ubi8/ubi9/….
		// Excluded repo/arch combinations never produce an image, so skip them.
		for _, r := range results {
			if r.State != "excluded" {
				targets = append(targets, model.Target{Repo: r.Repo, Arch: r.Arch, State: r.State, Instance: r.Instance})
			}
		}
		if len(targets) > 0 {
			// Persist the arch info so the CVE scanner has targets to iterate over
			// even though the build is disabled.
			pkg.Targets = targets
		}
	}
	if len(targets) == 0 {
		return nil
	}
	target := firstSucceededTarget(targets)
	filename, err := client.PackageContainerInfoFilename(ctx, pkg.Project, target.Repo, target.Arch, pkg.Name)
	if err != nil {
		slog.Warn("obs: container info filename", "pkg", pkg.Name, "err", err)
		return nil
	}
	if filename == "" {
		return nil
	}
	tags, err := client.PackageContainerTags(ctx, pkg.Project, target.Repo, target.Arch, pkg.Name, filename)
	if err != nil {
		slog.Warn("obs: container tags", "pkg", pkg.Name, "err", err)
		return nil
	}
	if len(tags) == 0 {
		return nil
	}
	if tags[0] != pkg.Version {
		pkg.Version = tags[0]
	}
	pkg.ContainerTags = tags
	// Promote release containers to published once their tags are confirmed.
	// OBS marks release builds as "disabled" (builds are frozen after release),
	// so they never naturally transition to "published" via the poller.
	if pkg.IsRelease && pkg.RollupState != model.RollupPublished {
		pkg.RollupState = model.RollupPublished
	}
	return nil
}

func firstSucceededTarget(targets []model.Target) model.Target {
	for _, t := range targets {
		if t.State == "succeeded" {
			return t
		}
	}
	return targets[0]
}
