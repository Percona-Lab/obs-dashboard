package mq

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/workingset"
	amqp "github.com/rabbitmq/amqp091-go"
)

// mqMessage is the JSON structure of OBS MQ events.
// Fields are a union of all event payloads; unused fields are zero for any given event type.
type mqMessage struct {
	Project  string `json:"project"`
	Package  string `json:"package"`
	Repo     string `json:"repository"` // package.* events
	RepoName string `json:"repo"`       // repo.* events (repo.published payload: project, repo, buildid)
	Arch     string `json:"arch"`
	Reason   string `json:"reason"`
	Sender   string `json:"sender"`
	User     string `json:"user"`
	Comment  string `json:"comment"`
}

// Consumer subscribes to one OBS instance's AMQP bus and updates the store
// on build events.
type Consumer struct {
	inst   *obs.Instance
	fleet  *obs.Fleet
	prefix string
	db     *sql.DB
	hub    *hubpkg.Hub
	ws     *workingset.WorkingSet
}

func NewConsumer(inst *obs.Instance, fleet *obs.Fleet, db *sql.DB, h *hubpkg.Hub, ws *workingset.WorkingSet) *Consumer {
	return &Consumer{inst: inst, fleet: fleet, prefix: inst.MQRoutingPrefix, db: db, hub: h, ws: ws}
}

// appendEvent writes evt to the store and notifies SSE clients.
func (c *Consumer) appendEvent(evt *model.Event) {
	if err := store.AppendEvent(c.db, evt); err != nil {
		slog.Error("mq: append event", "err", err)
		return
	}
	c.hub.Notify(hubpkg.NewEvent(evt))
}

// upsertPackage writes pkg to the store and notifies SSE clients.
func (c *Consumer) upsertPackage(pkg *model.Package) error {
	if err := store.UpsertPackageState(c.db, pkg, time.Now().UTC()); err != nil {
		return err
	}
	c.hub.Notify(hubpkg.PackageUpdate(pkg))
	return nil
}

// Run blocks until ctx is cancelled, reconnecting on errors with exponential back-off.
func (c *Consumer) Run(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for ctx.Err() == nil {
		if err := c.run(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("mq: disconnected, reconnecting", "err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			next := backoff * 2
			if next > maxBackoff {
				next = maxBackoff
			}
			backoff = next
		} else {
			backoff = time.Second
		}
	}
}

func (c *Consumer) run(ctx context.Context) error {
	conn, err := amqp.Dial(c.inst.MQURL)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("channel: %w", err)
	}
	defer ch.Close()

	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		return fmt.Errorf("queue declare: %w", err)
	}

	for _, key := range []string{
		c.prefix + ".package.#",
		c.prefix + ".repo.published",
		c.prefix + ".project.#",
	} {
		if err := ch.QueueBind(q.Name, key, c.inst.MQExchange, false, nil); err != nil {
			return fmt.Errorf("queue bind %s: %w", key, err)
		}
	}

	msgs, err := ch.Consume(q.Name, "", true, true, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	connClose := conn.NotifyClose(make(chan *amqp.Error, 1))
	slog.Info("mq: connected", "exchange", c.inst.MQExchange, "instance", c.inst.Slug)
	c.inst.SetMQConnected(true)
	defer c.inst.SetMQConnected(false)

	for {
		select {
		case <-ctx.Done():
			return nil
		case mqErr := <-connClose:
			if mqErr != nil {
				return fmt.Errorf("connection closed: %w", mqErr)
			}
			return nil
		case msg, ok := <-msgs:
			if !ok {
				return fmt.Errorf("channel closed")
			}
			c.handle(ctx, msg)
		}
	}
}

func (c *Consumer) handle(ctx context.Context, msg amqp.Delivery) {
	var m mqMessage
	if err := json.Unmarshal(msg.Body, &m); err != nil {
		slog.Debug("mq: unparseable message", "err", err)
		return
	}

	logical, ok := c.inst.ToLogical(m.Project)
	if !ok {
		return
	}
	m.Project = logical

	var payload any
	if err := json.Unmarshal(msg.Body, &payload); err != nil {
		slog.Debug("mq: received raw message", "key", msg.RoutingKey, "message", string(msg.Body), "err", err)
	} else {
		slog.Debug("mq: received raw message", "key", msg.RoutingKey, "payload", payload)
	}

	kind := obs.Classify(m.Project)

	key := strings.TrimPrefix(msg.RoutingKey, c.prefix+".")
	switch {
	case key == "repo.published":
		// Release projects: BinariesCheckTask handles publish detection. A
		// publish there can be a new release, which changes no build state,
		// so re-read the tags of the project's containers.
		if kind == obs.KindRelease {
			containers, err := store.QueryContainersByProject(c.db, m.Project)
			if err != nil {
				slog.Warn("mq: get release containers for publish signal", "project", m.Project, "err", err)
				return
			}
			for _, pkg := range containers {
				c.ws.Signal(pkg)
			}
			return
		}
		finished, err := store.GetFinishedPackagesByProject(c.db, m.Project)
		if err != nil {
			slog.Warn("mq: get finished packages for publish signal", "project", m.Project, "err", err)
		} else {
			for _, pkg := range finished {
				if awaitingPublishIn(pkg, m.RepoName) {
					c.ws.Signal(pkg)
				}
			}
		}

	case key == "project.create":
		if kind == obs.KindRelease {
			return // release project creation is handled by the poller
		}
		c.fleet.AddMember(c.inst.Slug, m.Project)
		c.appendEvent(&model.Event{
			ID:       "evt_" + ulid.Make().String(),
			Type:     model.EventCreated,
			Tags:     obs.ProjectTags(m.Project),
			Project:  m.Project,
			What:     fmt.Sprintf("project %s created", m.Project),
			Why:      m.Sender,
			URL:      c.inst.ProjectURL(m.Project),
			At:       time.Now().UTC(),
			Instance: c.inst.Slug,
		})

	case key == "project.delete":
		c.fleet.RemoveMember(c.inst.Slug, m.Project)
		if c.inst.Client != nil {
			c.inst.Client.EvictPublishFlags(c.inst.ToInstance(m.Project))
		}
		c.deleteProjectShare(m.Project)
		c.appendEvent(&model.Event{
			ID:       "evt_" + ulid.Make().String(),
			Type:     model.EventDeleted,
			Tags:     obs.ProjectTags(m.Project),
			Project:  m.Project,
			What:     fmt.Sprintf("project %s deleted", m.Project),
			Why:      m.Comment,
			URL:      c.inst.ProjectURL(m.Project),
			At:       time.Now().UTC(),
			Instance: c.inst.Slug,
		})

	case key == "package.create":
		if kind == obs.KindRelease {
			return // release packages are discovered by the poller
		}
		c.appendEvent(&model.Event{
			ID:       "evt_" + ulid.Make().String(),
			Type:     model.EventCreated,
			Tags:     obs.ProjectTags(m.Project),
			Project:  m.Project,
			Package:  m.Package,
			What:     fmt.Sprintf("package %s created", m.Package),
			Why:      m.Sender,
			URL:      c.inst.PackageURL(m.Project, m.Package),
			At:       time.Now().UTC(),
			Instance: c.inst.Slug,
		})
		stub := &model.Package{
			Project: m.Project,
			Name:    m.Package,
			Tags:    obs.ProjectTags(m.Project),
		}
		c.ws.Signal(stub)

	case key == "package.delete":
		c.deletePackageShare(m.Project, m.Package)
		c.appendEvent(&model.Event{
			ID:       "evt_" + ulid.Make().String(),
			Type:     model.EventDeleted,
			Tags:     obs.ProjectTags(m.Project),
			Project:  m.Project,
			Package:  m.Package,
			What:     fmt.Sprintf("package %s deleted", m.Package),
			Why:      m.Sender,
			URL:      c.inst.PackageURL(m.Project, m.Package),
			At:       time.Now().UTC(),
			Instance: c.inst.Slug,
		})

	case isPackageBuildEvent(key):
		if kind == obs.KindRelease {
			return // release build events are ignored; poller owns release state
		}
		rollup := mqStateToRollup(key)
		pkg := c.mergePackageTarget(m, rollup)
		if err := c.upsertPackage(pkg); err != nil {
			slog.Error("mq: upsert package", "err", err)
			return
		}
		if pkg.RollupState != model.RollupSucceeded {
			c.ws.Signal(pkg)
		}
	}
}

// mergePackageTarget reads the existing package from the store (if any), updates
// the (repo, arch) target with the new state, then recalculates OKTargets,
// TotalTargets, and RollupState from the full merged target list.
func (c *Consumer) mergePackageTarget(m mqMessage, newState model.RollupState) *model.Package {
	targets := []model.Target{{Repo: m.Repo, Arch: m.Arch, State: string(newState), Instance: c.inst.Slug}}
	var existingPkg *model.Package

	existing, err := store.QueryPackages(c.db, m.Project)
	if err != nil {
		slog.Warn("mq: could not read existing package, creating fresh", "err", err)
	} else {
		for _, p := range existing {
			// QueryPackages matches by prefix: ppg:17 also returns ppg:17:containers.
			if p.Project == m.Project && p.Name == m.Package {
				existingPkg = p
				// Found existing package — merge the updated target.
				merged := make([]model.Target, 0, len(p.Targets))
				found := false
				for _, t := range p.Targets {
					if t.Repo == m.Repo && t.Arch == m.Arch {
						next := model.Target{Repo: m.Repo, Arch: m.Arch, State: string(newState), Instance: c.inst.Slug}
						if t.State == string(newState) {
							next.Details = t.Details
							next.BlockedBy = t.BlockedBy
							next.BuildReason = t.BuildReason
							next.BuildReasonPackages = t.BuildReasonPackages
							next.Published = t.Published
						}
						merged = append(merged, next)
						found = true
					} else {
						merged = append(merged, t)
					}
				}
				if !found {
					merged = append(merged, model.Target{Repo: m.Repo, Arch: m.Arch, State: string(newState), Instance: c.inst.Slug})
				}
				targets = merged
				break
			}
		}
	}

	if c.fleet != nil {
		c.fleet.SetOwner(m.Project, m.Repo, c.inst.Slug)
	}

	// Recalculate rollup and counts from the full target list.
	worst := model.RollupSucceeded
	okCount := 0
	for _, t := range targets {
		s := model.RollupState(t.State)
		if s.Severity() > worst.Severity() {
			worst = s
		}
		if s == model.RollupSucceeded {
			okCount++
		}
	}

	var trigger *model.Trigger
	if m.Reason != "" {
		trigger = &model.Trigger{What: m.Reason, Kind: "obs", At: time.Now().UTC()}
	}

	return &model.Package{
		Project:       m.Project,
		Name:          m.Package,
		Tags:          packageTags(m.Project, existingPkg),
		RollupState:   worst,
		OKTargets:     okCount,
		TotalTargets:  len(targets),
		IsContainer:   existingContainer(existingPkg),
		Version:       existingVersion(existingPkg),
		ContainerTags: existingContainerTags(existingPkg),
		Trigger:       trigger,
		Targets:       targets,
		UpdatedAt:     time.Now().UTC(),
	}
}

func packageTags(project string, existing *model.Package) []string {
	tags := obs.ProjectTags(project)
	if existing == nil {
		return tags
	}
	seen := make(map[string]bool, len(tags)+len(existing.Tags))
	for _, tag := range tags {
		seen[tag] = true
	}
	for _, tag := range existing.Tags {
		if !seen[tag] {
			tags = append(tags, tag)
		}
	}
	return tags
}

func existingContainer(existing *model.Package) *bool {
	if existing == nil {
		return nil
	}
	return existing.IsContainer
}

func existingVersion(existing *model.Package) string {
	if existing == nil {
		return ""
	}
	return existing.Version
}

func existingContainerTags(existing *model.Package) []string {
	if existing == nil {
		return nil
	}
	return existing.ContainerTags
}

// deleteProjectShare removes this instance's share of project. When no
// instance hosts the project any more, every row goes (events, durations,
// CVE rows included); otherwise only this instance's targets are dropped.
func (c *Consumer) deleteProjectShare(project string) {
	if !c.fleet.IsHosted(project) {
		if err := store.DeletePackagesByProject(c.db, project); err != nil {
			slog.Error("mq: delete packages for project", "project", project, "err", err)
		}
		return
	}
	pkgs, err := store.QueryProjectPackages(c.db, project)
	if err != nil {
		slog.Error("mq: query project packages", "project", project, "err", err)
		return
	}
	for _, pkg := range pkgs {
		c.dropShare(pkg)
	}
}

func (c *Consumer) deletePackageShare(project, name string) {
	pkg, err := store.GetPackage(c.db, project, name)
	if err != nil || pkg == nil {
		return
	}
	c.dropShare(pkg)
}

func (c *Consumer) dropShare(pkg *model.Package) {
	rest := obs.WithoutInstance(pkg, c.inst.Slug)
	if len(rest.Targets) == 0 {
		if err := store.DeletePackage(c.db, pkg.Project, pkg.Name); err != nil {
			slog.Error("mq: delete package", "project", pkg.Project, "package", pkg.Name, "err", err)
		}
		c.ws.Remove(pkg.Project + "/" + pkg.Name)
		return
	}
	if err := c.upsertPackage(rest); err != nil {
		slog.Error("mq: upsert package share", "err", err)
		return
	}
	c.ws.Signal(rest)
}

func isPackageBuildEvent(key string) bool {
	return key == "package.build_success" ||
		key == "package.build_fail" ||
		key == "package.build_unchanged"
}

func mqStateToRollup(key string) model.RollupState {
	switch key {
	case "package.build_success":
		return model.RollupFinished
	case "package.build_fail":
		return model.RollupFailed
	default:
		// build_unchanged: the build completed with an identical result. Treat
		// it as finished so the signaled worker pass derives the real terminal
		// state (and its events) from OBS instead of jumping to succeeded.
		return model.RollupFinished
	}
}

// awaitingPublishIn reports whether pkg has a succeeded target in repo that
// has not been observed as published yet — the packages a repo.published
// event for that repo can actually progress. Packages waiting on other repos
// (or already fully observed) are skipped to avoid needless chain passes.
// An empty repo (malformed event) matches nothing; the poller fallback
// covers that case.
func awaitingPublishIn(pkg *model.Package, repo string) bool {
	for _, t := range pkg.Targets {
		if t.Repo == repo && t.State == "succeeded" && !t.Published {
			return true
		}
	}
	return false
}
