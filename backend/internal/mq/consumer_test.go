package mq

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/workingset"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestMergePackageTargetPreservesDetailsForRepeatedState(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	existing := &model.Package{
		Project:      "PR:pr-33:ppg:17",
		Name:         "pg_tde",
		Tags:         []string{"ppg", "pr"},
		RollupState:  model.RollupFinished,
		OKTargets:    0,
		TotalTargets: 1,
		Targets: []model.Target{
			{Repo: "images", Arch: "x86_64", State: "finished", Details: "succeeded"},
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, existing, time.Now().UTC()); err != nil {
		t.Fatalf("upsert existing package: %v", err)
	}

	fleet := obs.SingleFleet(nil, "isv:percona")
	consumer := &Consumer{db: db, inst: fleet.Default(), fleet: fleet, prefix: "opensuse.obs"}
	merged := consumer.mergePackageTarget(mqMessage{
		Project: "PR:pr-33:ppg:17",
		Package: "pg_tde",
		Repo:    "images",
		Arch:    "x86_64",
	}, model.RollupFinished)

	if len(merged.Targets) != 1 {
		t.Fatalf("expected one target, got %d", len(merged.Targets))
	}
	if merged.Targets[0].Details != "succeeded" {
		t.Fatalf("expected details to be preserved, got %q", merged.Targets[0].Details)
	}
}

func TestMQStateToRollupUnchangedIsFinished(t *testing.T) {
	if got := mqStateToRollup("package.build_unchanged"); got != model.RollupFinished {
		t.Fatalf("build_unchanged → %s, want finished", got)
	}
}

// build_unchanged must wake the working set: the build completed (with an
// identical result), which un-parks a package waiting on MQ completions.
func TestBuildUnchangedWakesWorkingSet(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ws := workingset.New(4, 30*time.Second, 5*time.Minute, 4)
	h := hubpkg.New()
	inst := obs.NewInstance(obs.InstanceInfo{Name: "openSUSE", Slug: "opensuse", Root: "isv:percona", WebURL: "https://build.opensuse.org", MQRoutingPrefix: "opensuse.obs"}, nil)
	fleet := obs.NewFleet(inst)
	c := NewConsumer(fleet.Default(), fleet, db, h, ws)

	// Seed a stored package with a building target (as if parked).
	pkg := &model.Package{
		Project: "ppg:17", Name: "pkg-a",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "repo", Arch: "x86_64", State: "building", BuildReason: "meta change"}},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{
		"project": "isv:percona:ppg:17", "package": "pkg-a",
		"repository": "repo", "arch": "x86_64",
	})
	c.handle(context.Background(), amqp.Delivery{
		RoutingKey: "opensuse.obs.package.build_unchanged",
		Body:       body,
	})

	select {
	case job := <-ws.Dispatch():
		got := job.Pkgs[0]
		if got.Name != "pkg-a" {
			t.Fatalf("dispatched %q, want pkg-a", got.Name)
		}
		for _, tgt := range got.Targets {
			if tgt.Repo == "repo" && tgt.Arch == "x86_64" && tgt.State != "finished" {
				t.Fatalf("merged target state = %q, want finished", tgt.State)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("build_unchanged did not signal the working set")
	}
}

// repo.published must wake only packages actually waiting on that repo's
// publication: at least one succeeded-unpublished target in the event's repo.
// Note the payload key is "repo" (unlike package events' "repository").
func TestRepoPublishedWakesOnlyMatchingRepo(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ws := workingset.New(4, 30*time.Second, 5*time.Minute, 4)
	inst := obs.NewInstance(obs.InstanceInfo{Name: "openSUSE", Slug: "opensuse", Root: "isv:percona", WebURL: "https://build.opensuse.org", MQRoutingPrefix: "opensuse.obs"}, nil)
	fleet := obs.NewFleet(inst)
	c := NewConsumer(fleet.Default(), fleet, db, hubpkg.New(), ws)

	seed := func(name, repo string, published bool) {
		pkg := &model.Package{
			Project: "ppg:17", Name: name,
			RollupState: model.RollupSucceeded,
			Targets:     []model.Target{{Repo: repo, Arch: "x86_64", State: "succeeded", Published: published}},
			UpdatedAt:   time.Now().UTC(),
		}
		if err := store.UpsertPackageState(db, pkg, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	seed("waiting-a", "repo-a", false) // in the published repo → must wake
	seed("waiting-b", "repo-b", false) // other repo → must not wake
	seed("done-a", "repo-a", true)     // already observed published → must not wake

	body, _ := json.Marshal(map[string]string{
		"project": "isv:percona:ppg:17", "repo": "repo-a",
	})
	c.handle(context.Background(), amqp.Delivery{
		RoutingKey: "opensuse.obs.repo.published",
		Body:       body,
	})

	select {
	case job := <-ws.Dispatch():
		if got := job.Pkgs[0]; got.Name != "waiting-a" {
			t.Fatalf("dispatched %q, want waiting-a", got.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("repo.published did not wake the awaiting package")
	}
	select {
	case job := <-ws.Dispatch():
		t.Fatalf("unexpected extra dispatch: %s", job.Pkgs[0].Name)
	case <-time.After(100 * time.Millisecond):
	}
}

// repo.published on a release project can mean a new release, which changes
// no build state: the project's containers must be woken to re-read their
// tags, and only the containers.
func TestRepoPublishedWakesReleaseContainers(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ws := workingset.New(4, 30*time.Second, 5*time.Minute, 4)
	inst := obs.NewInstance(obs.InstanceInfo{Name: "openSUSE", Slug: "opensuse", Root: "isv:percona", WebURL: "https://build.opensuse.org", MQRoutingPrefix: "opensuse.obs"}, nil)
	fleet := obs.NewFleet(inst)
	c := NewConsumer(fleet.Default(), fleet, db, hubpkg.New(), ws)

	now := time.Now().UTC()
	yes, no := true, false
	for _, pkg := range []*model.Package{
		{Project: "ppg:releases:14:containers", Name: "img", IsRelease: true, IsContainer: &yes,
			RollupState: model.RollupPublished, ContainerTags: []string{"14.24-1", "14"}, UpdatedAt: now},
		{Project: "ppg:releases:14:containers", Name: "rpm", IsRelease: true, IsContainer: &no,
			RollupState: model.RollupPublished, UpdatedAt: now},
		{Project: "ppg:releases:15:containers", Name: "other", IsRelease: true, IsContainer: &yes,
			RollupState: model.RollupPublished, ContainerTags: []string{"15.19-1", "15"}, UpdatedAt: now},
	} {
		if err := store.UpsertPackageState(db, pkg, now); err != nil {
			t.Fatal(err)
		}
	}

	body, _ := json.Marshal(map[string]string{
		"project": "isv:percona:ppg:releases:14:containers", "repo": "ubi9",
	})
	c.handle(context.Background(), amqp.Delivery{RoutingKey: "opensuse.obs.repo.published", Body: body})

	select {
	case job := <-ws.Dispatch():
		if got := job.Pkgs[0]; got.Name != "img" {
			t.Fatalf("dispatched %q, want img", got.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("repo.published did not wake the release container")
	}
	select {
	case job := <-ws.Dispatch():
		t.Fatalf("unexpected extra dispatch: %s", job.Pkgs[0].Name)
	case <-time.After(100 * time.Millisecond):
	}
}

func twoInstanceFleet() (*obs.Fleet, *obs.Instance, *obs.Instance) {
	x := obs.NewInstance(obs.InstanceInfo{Name: "openSUSE", Slug: "opensuse", Root: "isv:percona", WebURL: "https://build.opensuse.org", MQRoutingPrefix: "opensuse.obs"}, nil)
	y := obs.NewInstance(obs.InstanceInfo{Name: "Percona", Slug: "percona", Root: "percona", WebURL: "https://obs.example.com", MQRoutingPrefix: "percona.obs"}, nil)
	return obs.NewFleet(x, y), x, y
}

func TestConsumerCustomPrefixStampsInstance(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fleet, _, y := twoInstanceFleet()
	ws := workingset.New(64, time.Minute, time.Minute, 4)
	c := NewConsumer(y, fleet, db, hubpkg.New(), ws)

	body, _ := json.Marshal(map[string]string{"project": "percona:ppg:17", "package": "pg", "repository": "Debian_12", "arch": "aarch64"})
	c.handle(context.Background(), amqp.Delivery{RoutingKey: "percona.obs.package.build_fail", Body: body})

	// y is a non-identity instance: "percona:ppg:17" is stored as "ppg:17".
	got, err := store.GetPackage(db, "ppg:17", "pg")
	if err != nil || got == nil {
		t.Fatalf("package not stored under its logical name (err=%v)", err)
	}
	if got.Targets[0].Instance != "percona" {
		t.Errorf("target instance = %q", got.Targets[0].Instance)
	}
	if fleet.Owner("ppg:17", "Debian_12") == nil {
		t.Error("repo owner not recorded")
	}
}

func TestConsumerDropsOutOfRootProjects(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fleet, _, y := twoInstanceFleet()
	c := NewConsumer(y, fleet, db, hubpkg.New(), workingset.New(64, time.Minute, time.Minute, 4))
	body, _ := json.Marshal(map[string]string{"project": "home:someone:ppg", "package": "pg", "repository": "R", "arch": "a"})
	c.handle(context.Background(), amqp.Delivery{RoutingKey: "percona.obs.package.build_fail", Body: body})
	pkgs, _ := store.QueryPackages(db, "")
	if len(pkgs) != 0 {
		t.Fatalf("out-of-root project stored: %+v", pkgs)
	}
}

func TestConsumerProjectDeleteIsInstanceScoped(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fleet, x, y := twoInstanceFleet()
	logical, _ := y.ToLogical("percona:ppg:17") // "ppg:17", hosted by both instances
	fleet.AddMember(x.Slug, logical)
	fleet.AddMember(y.Slug, logical)
	now := time.Now().UTC()
	pkg := &model.Package{Project: logical, Name: "pg", RollupState: model.RollupFailed, UpdatedAt: now,
		Targets: []model.Target{
			{Repo: "RHEL_9", Arch: "x86_64", State: "succeeded", Instance: "opensuse"},
			{Repo: "Debian_12", Arch: "aarch64", State: "failed", Instance: "percona"},
		}}
	if err := store.UpsertPackageState(db, pkg, now); err != nil {
		t.Fatal(err)
	}
	c := NewConsumer(y, fleet, db, hubpkg.New(), workingset.New(64, time.Minute, time.Minute, 4))
	body, _ := json.Marshal(map[string]string{"project": "percona:ppg:17"})
	c.handle(context.Background(), amqp.Delivery{RoutingKey: "percona.obs.project.delete", Body: body})

	got, _ := store.GetPackage(db, logical, "pg")
	if got == nil || len(got.Targets) != 1 || got.Targets[0].Instance != "opensuse" {
		t.Fatalf("only percona's targets should go: %+v", got)
	}
}

// With root-free names QueryPackages("ppg:17") also returns ppg:17:containers
// packages; the merge must match the exact project.
func TestMergePackageTargetExactProject(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	other := &model.Package{Project: "ppg:17:containers", Name: "pg", RollupState: model.RollupSucceeded, UpdatedAt: now,
		Targets: []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded", Instance: "opensuse"}}}
	if err := store.UpsertPackageState(db, other, now); err != nil {
		t.Fatal(err)
	}
	fleet := obs.SingleFleet(nil, "isv:percona")
	consumer := &Consumer{db: db, inst: fleet.Default(), fleet: fleet, prefix: "opensuse.obs"}
	merged := consumer.mergePackageTarget(mqMessage{Project: "ppg:17", Package: "pg", Repo: "RHEL_9", Arch: "x86_64"}, model.RollupBuilding)
	if len(merged.Targets) != 1 || merged.Targets[0].Repo != "RHEL_9" {
		t.Errorf("merged targets from a different project: %+v", merged.Targets)
	}
}
