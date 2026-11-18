package worker

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/percona/obs-dashboard/internal/cve"
	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/workingset"
)

// Task is implemented by types that enrich a package's state from OBS.
// Implementations live in obs/tasks.go to avoid circular imports.
type Task interface {
	Run(ctx context.Context, client *obs.Fleet, pkg *model.Package, env *obs.Env) error
}

type Pool struct {
	size         int
	devTasks     []Task
	releaseTasks []Task
	client       *obs.Fleet
	db           *sql.DB
	hub          *hubpkg.Hub
	ws           *workingset.WorkingSet
	scanner      *cve.Scanner
}

func NewPool(size int, devTasks, releaseTasks []Task, client *obs.Fleet, db *sql.DB, hub *hubpkg.Hub, ws *workingset.WorkingSet, scanner *cve.Scanner) *Pool {
	return &Pool{size: size, devTasks: devTasks, releaseTasks: releaseTasks,
		client: client, db: db, hub: hub, ws: ws, scanner: scanner}
}

func (p *Pool) Start(ctx context.Context) {
	for i := 0; i < p.size; i++ {
		go p.run(ctx)
	}
}

func (p *Pool) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-p.ws.Dispatch():
			if !ok {
				return
			}
			p.ProcessJob(ctx, job)
		}
	}
}

// ProcessJob runs the task chain for every package in job. For project-fetch
// jobs it makes one project-level _result call and injects the response into
// each package's pass; if that fetch fails, every package falls back to
// per-package fetches. Exported for testing.
func (p *Pool) ProcessJob(ctx context.Context, job workingset.Job) {
	var envs map[string]*obs.Env
	if job.ProjectFetch && p.client != nil {
		results, repoStates, failed, err := p.client.BuildResults(ctx, job.Project)
		if err != nil {
			slog.Warn("worker: project batch fetch, falling back to per-package",
				"project", job.Project, "err", err)
		} else {
			byPkg := make(map[string][]obs.PackageBuildState)
			for _, r := range results {
				byPkg[r.Package] = append(byPkg[r.Package], r)
			}
			envs = make(map[string]*obs.Env, len(job.Pkgs))
			for _, pkg := range job.Pkgs {
				if states, ok := byPkg[pkg.Name]; ok {
					envs[pkg.Name] = &obs.Env{BuildStates: states, RepoStates: repoStates, FailedInstances: failed}
				}
			}
			// A package absent from byPkg but with targets on a failed instance
			// must still get an env so its carry-forward happens.
			for _, pkg := range job.Pkgs {
				if _, ok := envs[pkg.Name]; !ok && len(failed) > 0 {
					envs[pkg.Name] = &obs.Env{BuildStates: []obs.PackageBuildState{}, RepoStates: repoStates, FailedInstances: failed}
				}
			}
		}
	}
	for _, pkg := range job.Pkgs {
		key := pkg.Project + "/" + pkg.Name
		if ctx.Err() != nil {
			p.ws.Done(key, false) // shutdown: release in-flight without processing
			continue
		}
		changed := p.ProcessOnce(ctx, pkg, envs[pkg.Name])
		p.ws.Done(key, changed)
	}
}

// ProcessOnce runs the task chain for pkg (devTasks or releaseTasks based on
// pkg.IsRelease), upserts to DB, emits build events and SSE for real-time
// packages only, and removes pkg from the working set when rollup reaches
// published. Returns true when the pass observed a rollup or target build
// state change — the scheduler uses this to reset or grow the poll backoff.
// Exported for testing.
func (p *Pool) ProcessOnce(ctx context.Context, pkg *model.Package, env *obs.Env) bool {
	// Capture now before tasks run so state_changed_at reflects when the state
	// was observed, not when the (potentially slow) task chain finished.
	now := time.Now().UTC()

	// Snapshot rollup state before tasks run so we can detect the transition.
	prevRollupState := pkg.RollupState
	prevContainerTags := slices.Clone(pkg.ContainerTags)

	// Snapshot target state before task chain.
	// BuildReasonPackages is a slice field — deep copy to avoid aliasing.
	oldTargets := make([]model.Target, len(pkg.Targets))
	for i, t := range pkg.Targets {
		c := t
		if len(t.BuildReasonPackages) > 0 {
			c.BuildReasonPackages = append([]string(nil), t.BuildReasonPackages...)
		}
		oldTargets[i] = c
	}

	tasks := p.devTasks
	if pkg.IsRelease {
		tasks = p.releaseTasks
	}
	for _, t := range tasks {
		if err := t.Run(ctx, p.client, pkg, env); err != nil {
			slog.Warn("worker: task error",
				"task", fmt.Sprintf("%T", t),
				"pkg", pkg.Project+"/"+pkg.Name,
				"err", err)
		}
	}

	changed := prevRollupState != pkg.RollupState ||
		targetStatesChanged(oldTargets, pkg.Targets)

	// flagsKnown gates parking below: on a fetch error the zero-value flags
	// treat every repo as publishing, which blocks Settled — and parking
	// instead would strand an all-succeeded package in a never-publishing
	// repo with no wake source. Keep polling until the flags resolve
	// (success is cached per project). A nil client (tests, no OBS) has no
	// flags to fetch; the zero value decides as before.
	var flags obs.PublishFlags
	flagsKnown := p.client == nil
	if p.client != nil {
		if f, err := p.client.ProjectPublishFlags(ctx, pkg.Project); err == nil {
			flags = f
			flagsKnown = true
		}
	}
	pkg.Settled = obs.Settled(pkg, flags) && pkg.IsContainer != nil

	if err := store.UpsertPackageState(p.db, pkg, now); err != nil {
		slog.Error("worker: upsert package state", "pkg", pkg.Project+"/"+pkg.Name, "err", err)
	}

	if !pkg.IsRelease {
		p.hub.Notify(hubpkg.PackageUpdate(pkg))
		p.emitBuildEvents(pkg, oldTargets, now, flags)
	}

	// Enqueue a CVE scan on two conditions:
	// 1. Normal path: rollup just transitioned to published (dev containers, PR containers).
	// 2. Release path: package was already published but its container tags changed.
	//    Release containers arrive with tags missing and ContainerTagsTask fills them in;
	//    later a new release replaces them. Neither triggers condition 1, and the
	//    stored scan would otherwise describe the previous image until the nightly rescan.
	tagsChanged := !slices.Equal(prevContainerTags, pkg.ContainerTags)
	if p.scanner != nil &&
		pkg.IsContainer != nil && *pkg.IsContainer &&
		pkg.RollupState == model.RollupPublished &&
		(prevRollupState != model.RollupPublished || tagsChanged) &&
		len(pkg.ContainerTags) > 0 {
		p.scanner.Enqueue(cve.ScanRequest{
			Project:    pkg.Project,
			Package:    pkg.Name,
			Tags:       pkg.Tags,
			PrimaryTag: pkg.ContainerTags[len(pkg.ContainerTags)-1],
			Targets:    cve.SucceededTargets(pkg.Targets),
		})
	}

	// Remove from the working set when there is nothing left to poll for:
	// settled (terminal) or parked (waiting only on completions that MQ
	// announces — build results or repo publication — with the poller as
	// fallback). Parking requires known publish flags (see flagsKnown above);
	// it is in-memory only — settled stays 0, so a restart re-seeds the
	// package and re-parks it.
	if pkg.Settled || (flagsKnown && pkg.IsContainer != nil && obs.Parkable(pkg)) {
		p.ws.Remove(pkg.Project + "/" + pkg.Name)
	}
	return changed
}

// targetStatesChanged reports whether any target's build state differs
// between two snapshots, or the target set itself changed. Details and
// enrichment fields are deliberately ignored: backoff cares about build
// progress, not metadata refinement.
func targetStatesChanged(old, new []model.Target) bool {
	if len(old) != len(new) {
		return true
	}
	prev := make(map[string]string, len(old))
	for _, t := range old {
		prev[t.Repo+"/"+t.Arch] = t.State
	}
	for _, t := range new {
		s, ok := prev[t.Repo+"/"+t.Arch]
		if !ok || s != t.State {
			return true
		}
	}
	return false
}

// emitBuildEvents compares oldTargets with pkg.Targets and appends one event
// per target for each meaningful state transition, implementing a per-target
// build event state machine.
func (p *Pool) emitBuildEvents(pkg *model.Package, oldTargets []model.Target, now time.Time, flags obs.PublishFlags) {
	oldByKey := make(map[string]model.Target, len(oldTargets))
	for _, t := range oldTargets {
		oldByKey[t.Repo+"/"+t.Arch] = t
	}

	for _, t := range pkg.Targets {
		key := t.Repo + "/" + t.Arch
		old := oldByKey[key]
		inst := p.client.InstanceOrDefault(t.Instance)

		// build_started: BuildReason newly appeared, or target re-builds after failure.
		// The second condition handles rebuilds where OBS reuses the same BuildReason,
		// so old.BuildReason is non-empty but a new build cycle has clearly started.
		isNewBuildCycle := (old.BuildReason == "" && t.BuildReason != "") ||
			(old.State == "failed" && t.State != "failed" && t.BuildReason != "")
		if isNewBuildCycle {
			why := t.BuildReason
			if len(t.BuildReasonPackages) > 0 {
				why += ": " + strings.Join(t.BuildReasonPackages, ", ")
			}
			p.appendEvent(&model.Event{
				ID:       "evt_" + ulid.Make().String(),
				Type:     model.EventBuildStarted,
				Tags:     pkg.Tags,
				Project:  pkg.Project,
				Package:  pkg.Name,
				Repo:     t.Repo,
				Arch:     t.Arch,
				Instance: t.Instance,
				What:     fmt.Sprintf("%s build started", pkg.Name),
				Why:      why,
				URL:      inst.LiveLogURL(pkg.Project, pkg.Name, t.Repo, t.Arch),
				At:       now,
			})
		}

		// Intermediate states — only after build_started (guard: BuildReason present).
		if t.BuildReason != "" {
			if old.State != "blocked" && t.State == "blocked" {
				p.appendEvent(&model.Event{
					ID:       "evt_" + ulid.Make().String(),
					Type:     model.EventBlocked,
					Tags:     pkg.Tags,
					Project:  pkg.Project,
					Package:  pkg.Name,
					Repo:     t.Repo,
					Arch:     t.Arch,
					Instance: t.Instance,
					What:     fmt.Sprintf("%s blocked", pkg.Name),
					Why:      t.BlockedBy,
					URL:      inst.PackageURL(pkg.Project, pkg.Name),
					At:       now,
				})
			}
			if old.State != "unresolvable" && t.State == "unresolvable" {
				p.appendEvent(&model.Event{
					ID:       "evt_" + ulid.Make().String(),
					Type:     model.EventUnresolvable,
					Tags:     pkg.Tags,
					Project:  pkg.Project,
					Package:  pkg.Name,
					Repo:     t.Repo,
					Arch:     t.Arch,
					Instance: t.Instance,
					What:     fmt.Sprintf("%s unresolvable", pkg.Name),
					Why:      t.Details,
					URL:      inst.PackageURL(pkg.Project, pkg.Name),
					At:       now,
				})
			}
			if old.State != "broken" && t.State == "broken" {
				p.appendEvent(&model.Event{
					ID:       "evt_" + ulid.Make().String(),
					Type:     model.EventBroken,
					Tags:     pkg.Tags,
					Project:  pkg.Project,
					Package:  pkg.Name,
					Repo:     t.Repo,
					Arch:     t.Arch,
					Instance: t.Instance,
					What:     fmt.Sprintf("%s broken", pkg.Name),
					Why:      t.Details,
					URL:      inst.PackageURL(pkg.Project, pkg.Name),
					At:       now,
				})
			}
		}

		// succeeded: publication is the real terminal success signal for
		// publishing repos; for repos that never publish, the build-state
		// transition is the only terminal moment there is.
		publishedNow := !old.Published && t.Published
		completedNonPublishing := old.State != "succeeded" && t.State == "succeeded" &&
			!flags.Publishes(t.Repo)
		if publishedNow || completedNonPublishing {
			p.appendEvent(&model.Event{
				ID:       "evt_" + ulid.Make().String(),
				Type:     model.EventSucceeded,
				Tags:     pkg.Tags,
				Project:  pkg.Project,
				Package:  pkg.Name,
				Repo:     t.Repo,
				Arch:     t.Arch,
				Instance: t.Instance,
				What:     fmt.Sprintf("%s succeeded", pkg.Name),
				Why:      "",
				Version:  pkg.Version,
				URL:      inst.PackageURL(pkg.Project, pkg.Name),
				At:       now,
			})
		}

		// failed: terminal state — OBS's unambiguous build failure, no BuildReason required.
		if old.State != "failed" && t.State == "failed" {
			p.appendEvent(&model.Event{
				ID:       "evt_" + ulid.Make().String(),
				Type:     model.EventFailed,
				Tags:     pkg.Tags,
				Project:  pkg.Project,
				Package:  pkg.Name,
				Repo:     t.Repo,
				Arch:     t.Arch,
				Instance: t.Instance,
				What:     fmt.Sprintf("%s failed", pkg.Name),
				Why:      "",
				URL:      inst.PackageURL(pkg.Project, pkg.Name),
				At:       now,
			})
		}
	}
}

func (p *Pool) appendEvent(evt *model.Event) {
	if p.db == nil {
		return
	}
	if err := store.AppendEvent(p.db, evt); err != nil {
		slog.Error("worker: append event", "err", err)
		return
	}
	p.hub.Notify(hubpkg.NewEvent(evt))
}
