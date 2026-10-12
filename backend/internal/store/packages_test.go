package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	_ "modernc.org/sqlite"
)

func TestUpsertQueryPackage(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	p := &model.Package{
		Project:      "ppg:17",
		Name:         "pg_tde",
		RollupState:  model.RollupFailed,
		OKTargets:    4,
		TotalTargets: 6,
		Trigger: &model.Trigger{
			What: "openssl 3.2.1 → 3.2.2",
			Kind: "dependency bump",
			At:   now,
		},
		Targets: []model.Target{
			{Repo: "EL_9", Arch: "x86_64", State: "succeeded"},
			{Repo: "Debian_12", Arch: "x86_64", State: "failed"},
		},
		UpdatedAt: now,
	}

	if err := UpsertPackageState(db, p, p.UpdatedAt); err != nil {
		t.Fatal(err)
	}

	pkgs, err := QueryPackages(db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1, got %d", len(pkgs))
	}
	got := pkgs[0]
	if got.Name != "pg_tde" {
		t.Errorf("name: got %q", got.Name)
	}
	if got.RollupState != model.RollupFailed {
		t.Errorf("rollup_state: got %q", got.RollupState)
	}
	if got.Trigger == nil || got.Trigger.Kind != "dependency bump" {
		t.Errorf("trigger: got %+v", got.Trigger)
	}
	if len(got.Targets) != 2 {
		t.Errorf("targets: got %d", len(got.Targets))
	}
}

func TestUpsertUpdatesExisting(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	p := &model.Package{
		Project: "ppg:17", Name: "pg_tde",
		RollupState: model.RollupFailed,
		Targets:     []model.Target{}, UpdatedAt: now,
	}
	UpsertPackageState(db, p, p.UpdatedAt)

	p.RollupState = model.RollupSucceeded
	UpsertPackageState(db, p, p.UpdatedAt)

	pkgs, _ := QueryPackages(db, "")
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 after update, got %d", len(pkgs))
	}
	if pkgs[0].RollupState != model.RollupSucceeded {
		t.Errorf("expected succeeded after update, got %q", pkgs[0].RollupState)
	}
}

func TestGetActivePackages(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	trueVal := true
	falseVal := false

	// Published + is_container detected + settled: excluded (worker has nothing left to poll).
	publishedContainer := &model.Package{
		Project: "isv:percona", Name: "pkg-container-done",
		RollupState: model.RollupPublished, OKTargets: 1, TotalTargets: 1,
		IsContainer: &trueVal, Settled: true,
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt: now,
	}
	if err := UpsertPackageState(db, publishedContainer, publishedContainer.UpdatedAt); err != nil {
		t.Fatal(err)
	}

	// Published + is_container=false + settled: also excluded.
	publishedNonContainer := &model.Package{
		Project: "isv:percona", Name: "pkg-dep-done",
		RollupState: model.RollupPublished, OKTargets: 1, TotalTargets: 1,
		IsContainer: &falseVal, Settled: true,
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt: now,
	}
	if err := UpsertPackageState(db, publishedNonContainer, publishedNonContainer.UpdatedAt); err != nil {
		t.Fatal(err)
	}

	// Succeeded + is_container IS NULL + not settled: included so PackageTypeTask can detect it.
	succeededUndetected := &model.Package{
		Project: "isv:percona", Name: "pkg-ok",
		RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt: now,
	}
	if err := UpsertPackageState(db, succeededUndetected, succeededUndetected.UpdatedAt); err != nil {
		t.Fatal(err)
	}

	// Failing package: always included.
	failing := &model.Package{
		Project: "isv:percona", Name: "pkg-fail",
		RollupState: model.RollupFailed, OKTargets: 0, TotalTargets: 1,
		Targets:   []model.Target{{Repo: "repo", Arch: "x86_64", State: "failed"}},
		UpdatedAt: now,
	}
	if err := UpsertPackageState(db, failing, failing.UpdatedAt); err != nil {
		t.Fatal(err)
	}

	pkgs, err := GetActivePackages(db)
	if err != nil {
		t.Fatal(err)
	}

	names := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		names[p.Name] = true
	}
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 active packages (pkg-ok, pkg-fail), got %d: %v", len(pkgs), names)
	}
	if !names["pkg-ok"] {
		t.Error("expected pkg-ok (succeeded but undetected) to be included")
	}
	if !names["pkg-fail"] {
		t.Error("expected pkg-fail (failing) to be included")
	}
	if names["pkg-container-done"] {
		t.Error("expected pkg-container-done (settled) to be excluded")
	}
	if names["pkg-dep-done"] {
		t.Error("expected pkg-dep-done (settled) to be excluded")
	}
}

func TestGetFinishedPackagesByProject(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)

	// GetFinishedPackagesByProject returns succeeded packages (for publish re-check).
	pkgSucceeded1 := &model.Package{
		Project: "ppg:17", Name: "postgres17",
		RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
		Targets:   []model.Target{{Repo: "Percona-PPG-17", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt: now,
	}
	pkgSucceeded2 := &model.Package{
		Project: "ppg:17", Name: "pgaudit17",
		RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
		Targets:   []model.Target{{Repo: "Percona-PPG-17", Arch: "aarch64", State: "succeeded"}},
		UpdatedAt: now,
	}
	pkgBuilding := &model.Package{
		Project: "ppg:17", Name: "pg_stat_monitor",
		RollupState: model.RollupBuilding, OKTargets: 0, TotalTargets: 1,
		Targets:   []model.Target{{Repo: "Percona-PPG-17", Arch: "x86_64", State: "building"}},
		UpdatedAt: now,
	}
	pkgOtherProject := &model.Package{
		Project: "ppg:16", Name: "postgres16",
		RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
		Targets:   []model.Target{{Repo: "Percona-PPG-16", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt: now,
	}
	for _, pkg := range []*model.Package{pkgSucceeded1, pkgSucceeded2, pkgBuilding, pkgOtherProject} {
		if err := UpsertPackageState(db, pkg, pkg.UpdatedAt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := GetFinishedPackagesByProject(db, "ppg:17")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 succeeded packages, got %d", len(got))
	}
	names := map[string]bool{}
	for _, p := range got {
		names[p.Name] = true
		if p.RollupState != model.RollupSucceeded {
			t.Errorf("package %s: want RollupSucceeded, got %s", p.Name, p.RollupState)
		}
		if p.Project != "ppg:17" {
			t.Errorf("package %s: wrong project %s", p.Name, p.Project)
		}
	}
	if !names["postgres17"] || !names["pgaudit17"] {
		t.Errorf("wrong packages returned: %v", names)
	}

	// Empty result case
	got2, err := GetFinishedPackagesByProject(db, "ppg:99")
	if err != nil {
		t.Fatalf("unexpected error on empty: %v", err)
	}
	if len(got2) != 0 {
		t.Errorf("want 0 packages for unknown project, got %d", len(got2))
	}
}

func boolPtr(b bool) *bool { return &b }

func TestQueryBuildPackages(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	insert := func(project, name string) {
		db.Exec(`INSERT INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, updated_at)
            VALUES (?, ?, 'building', 0, 0, '[]', ?)`, project, name, now)
	}
	insert("ppg:staging:17", "pg_tde")
	insert("ppg:staging:17:containers:ubi9", "pg_container")
	insert("ppg:staging:17:extras", "extras_pkg")
	insert("ppg:devel:17", "devel_pkg")
	insert("ppg:common", "common_pkg")
	insert("ppg:common:deps", "common_deps_pkg")
	insert("common", "global_common")
	insert("ppg:releases:17", "release_pkg")

	pkgs, err := QueryBuildPackages(db, "ppg", "staging", "17")
	if err != nil {
		t.Fatal(err)
	}

	names := make(map[string]bool)
	for _, p := range pkgs {
		names[p.Name] = true
	}
	// The staging:17 subtree (incl. subprojects — the client filters extras
	// into its own version entry) plus both shared common projects.
	for _, want := range []string{"pg_tde", "pg_container", "extras_pkg", "common_pkg", "common_deps_pkg", "global_common"} {
		if !names[want] {
			t.Errorf("missing expected package %q", want)
		}
	}
	for _, unwanted := range []string{"devel_pkg", "release_pkg"} {
		if names[unwanted] {
			t.Errorf("%s should not appear in staging build packages", unwanted)
		}
	}

	// Version-less fetch scopes to the whole tier plus BOTH shared common
	// subtrees. Regression guard: the board only ever issues the "_" (All)
	// query, so ppg:common[:deps...] must appear here too.
	all, err := QueryBuildPackages(db, "ppg", "devel", "_")
	if err != nil {
		t.Fatal(err)
	}
	allNames := make(map[string]bool)
	for _, p := range all {
		allNames[p.Name] = true
	}
	for _, want := range []string{"devel_pkg", "common_pkg", "common_deps_pkg", "global_common"} {
		if !allNames[want] {
			t.Errorf("devel _ fetch: missing expected package %q", want)
		}
	}
	for _, unwanted := range []string{"pg_tde", "release_pkg"} {
		if allNames[unwanted] {
			t.Errorf("devel _ fetch: %s should not appear", unwanted)
		}
	}
}

func TestQueryPRBuildPackagesCoversWholePR(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	insert := func(project, name string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, updated_at)
            VALUES (?, ?, 'building', 0, 0, '[]', ?)`, project, name, now); err != nil {
			t.Fatal(err)
		}
	}
	insert("PR:pr-104:ppg:17", "pg_tde")
	insert("PR:pr-104:ppg:17:containers:ubi9", "pg_container")
	insert("PR:pr-104:common", "common_pkg")
	insert("PR:pr-104:common:deps", "common_dep")
	insert("PR:pr-104:other:17", "other_pkg")            // now part of the PR
	insert("PR:pr-105:common", "other_pr_common")        // different PR
	insert("PR:pr-200:common:deps:build", "common_only") // common-only PR
	insert("PR:pr-10:common", "pr10_pkg")                // digit-prefix boundary

	pkgs, err := QueryPRBuildPackages(db, "pr-104")
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, p := range pkgs {
		names[p.Name] = true
	}
	for _, want := range []string{"pg_tde", "pg_container", "common_pkg", "common_dep", "other_pkg"} {
		if !names[want] {
			t.Errorf("pr-104: missing expected package %q", want)
		}
	}
	for _, unwanted := range []string{"other_pr_common", "common_only", "pr10_pkg"} {
		if names[unwanted] {
			t.Errorf("pr-104: unexpected package %q from another PR", unwanted)
		}
	}

	// Common-only PR must be reachable.
	onlyCommon, err := QueryPRBuildPackages(db, "pr-200")
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyCommon) != 1 || onlyCommon[0].Name != "common_only" {
		t.Errorf("pr-200 (common-only): got %d packages, want exactly [common_only]", len(onlyCommon))
	}
}

func TestQueryPRDistinctReposCoversWholePR(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	insert := func(project, name, targets string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, updated_at)
            VALUES (?, ?, 'building', 0, 0, ?, ?)`, project, name, targets, now); err != nil {
			t.Fatal(err)
		}
	}
	insert("PR:pr-104:ppg:17", "pg_tde", `[{"repo":"EL_9"}]`)
	insert("PR:pr-104:common", "common_pkg", `[{"repo":"Debian_12"}]`)
	insert("PR:pr-104:ppg:18", "pg_tde18", `[{"repo":"EL_8"}]`) // now included
	insert("PR:pr-105:ppg:17", "other", `[{"repo":"EL_7"}]`)    // different PR

	repos, err := QueryPRDistinctRepos(db, "pr-104")
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool)
	for _, repo := range repos {
		got[repo] = true
	}
	for _, want := range []string{"EL_9", "Debian_12", "EL_8"} {
		if !got[want] {
			t.Errorf("missing expected repo %q", want)
		}
	}
	if got["EL_7"] {
		t.Errorf("unexpected repo EL_7 from another PR")
	}
}

func TestQueryReleasePackages(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	db.Exec(`INSERT INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, updated_at, is_release)
        VALUES ('ppg:releases:17', 'pg_tde', 'building', 0, 0, '[]', ?, 1)`, now)
	db.Exec(`INSERT INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, updated_at)
        VALUES ('ppg:17', 'pg_tde_dev', 'building', 0, 0, '[]', ?)`, now)

	pkgs, err := QueryReleasePackages(db, "ppg:releases")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 release package, got %d", len(pkgs))
	}
	if pkgs[0].Name != "pg_tde" {
		t.Errorf("expected pg_tde, got %q", pkgs[0].Name)
	}
	if !pkgs[0].IsRelease {
		t.Error("IsRelease should be true")
	}
}

func TestStateTransitionsRecorded(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	pkg := &model.Package{
		Project:     "ppg:17",
		Name:        "pg_tde",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "RockyLinux_9", Arch: "x86_64", State: "building"}},
		UpdatedAt:   time.Now().UTC(),
	}
	now := time.Now().UTC()
	if err := UpsertPackageState(db, pkg, now); err != nil {
		t.Fatal(err)
	}

	// Transition to succeeded.
	pkg.Targets[0].State = "succeeded"
	pkg.RollupState = model.RollupSucceeded
	if err := UpsertPackageState(db, pkg, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM target_state_durations WHERE project = ? AND package = ?`,
		pkg.Project, pkg.Name).Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 duration rows, got %d", count)
	}
	var durMs sql.NullInt64
	db.QueryRow(`SELECT duration_ms FROM target_state_durations WHERE state = 'building' AND exited_at IS NOT NULL`).Scan(&durMs)
	if !durMs.Valid || durMs.Int64 <= 0 {
		t.Error("expected positive duration_ms for closed building entry")
	}
}

func TestQueryPackagesIncludesTargetStartedAt(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	startedAt := time.Now().UTC().Truncate(time.Second)
	pkg := &model.Package{
		Project:     "ppg:17",
		Name:        "pg_tde",
		RollupState: model.RollupBuilding,
		Targets: []model.Target{
			{Repo: "RockyLinux_9", Arch: "x86_64", State: "building"},
			{Repo: "Ubuntu_24.04", Arch: "aarch64", State: "scheduled"},
		},
		UpdatedAt: startedAt,
	}
	if err := UpsertPackageState(db, pkg, startedAt); err != nil {
		t.Fatal(err)
	}

	pkgs, err := QueryPackages(db, "ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	for _, target := range pkgs[0].Targets {
		if target.StartedAt == nil {
			t.Fatalf("target %s/%s missing started_at", target.Repo, target.Arch)
		}
		if !target.StartedAt.Equal(startedAt) {
			t.Errorf("target %s/%s started_at: want %v, got %v",
				target.Repo, target.Arch, startedAt, *target.StartedAt)
		}
	}
}

func TestQueryPackagesIgnoresUnrelatedTargetDurations(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	startedAt := time.Now().UTC().Truncate(time.Second)
	pkg := &model.Package{
		Project:     "ppg:17",
		Name:        "pg_tde",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{{Repo: "RockyLinux_9", Arch: "x86_64", State: "building"}},
		UpdatedAt:   startedAt,
	}
	if err := UpsertPackageState(db, pkg, startedAt); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`
		INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"ppg:18", "unrelated", "Ubuntu_24.04", "aarch64", "building", "not-a-time",
	); err != nil {
		t.Fatal(err)
	}

	pkgs, err := QueryPackages(db, "ppg:17")
	if err != nil {
		t.Fatalf("query should ignore unrelated duration rows: %v", err)
	}
	if len(pkgs) != 1 || len(pkgs[0].Targets) != 1 || pkgs[0].Targets[0].StartedAt == nil {
		t.Fatalf("expected queried target to keep started_at, got %#v", pkgs)
	}
}

func TestUpsertPreservesTagsAndIsRelease(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert with tags and is_release set.
	pkg := &model.Package{
		Project:     "ppg:releases:17",
		Name:        "pg_tde",
		Tags:        []string{"ppg", "release"},
		IsRelease:   true,
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{},
		UpdatedAt:   time.Now().UTC(),
	}
	now := time.Now().UTC()
	if err := UpsertPackageState(db, pkg, now); err != nil {
		t.Fatal(err)
	}

	// Upsert again with empty Tags and IsRelease=false (simulating worker update).
	stub := &model.Package{
		Project:     "ppg:releases:17",
		Name:        "pg_tde",
		Tags:        nil,
		IsRelease:   false,
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{},
		UpdatedAt:   time.Now().UTC(),
	}
	if err := UpsertPackageState(db, stub, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	// Tags and is_release should be preserved from the first insert.
	pkgs, err := QueryPackages(db, "ppg:releases:17")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	p := pkgs[0]
	if len(p.Tags) != 2 || p.Tags[0] != "ppg" || p.Tags[1] != "release" {
		t.Errorf("tags not preserved: got %v", p.Tags)
	}
	if !p.IsRelease {
		t.Error("is_release not preserved")
	}
}

func TestContainerTagsRoundtrip(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	p := &model.Package{
		Project:       "ppg:17:containers:ubi9",
		Name:          "percona-distribution-postgresql",
		RollupState:   model.RollupSucceeded,
		IsContainer:   boolPtr(true),
		Version:       "17.4-1-1.7",
		ContainerTags: []string{"17.4-1-1.7", "17.4-1", "17"},
		Targets:       []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:     now,
	}
	if err := UpsertPackageState(db, p, now); err != nil {
		t.Fatal(err)
	}

	pkgs, err := QueryPackages(db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	got := pkgs[0]
	if len(got.ContainerTags) != 3 {
		t.Fatalf("ContainerTags: want 3, got %d: %v", len(got.ContainerTags), got.ContainerTags)
	}
	if got.ContainerTags[0] != "17.4-1-1.7" {
		t.Errorf("ContainerTags[0]: want %q, got %q", "17.4-1-1.7", got.ContainerTags[0])
	}
	if got.ContainerTags[2] != "17" {
		t.Errorf("ContainerTags[2]: want %q, got %q", "17", got.ContainerTags[2])
	}

	// Nil ContainerTags must round-trip as nil (not empty slice)
	p2 := &model.Package{
		Project: "ppg:17", Name: "pg_tde",
		RollupState: model.RollupSucceeded,
		Targets:     []model.Target{}, UpdatedAt: now,
	}
	if err := UpsertPackageState(db, p2, now); err != nil {
		t.Errorf("upsert nil ContainerTags: %v", err)
	}
	pkgs2, _ := QueryPackages(db, "ppg:17")
	for _, pkg := range pkgs2 {
		if pkg.Name == "pg_tde" && pkg.ContainerTags != nil {
			t.Errorf("pg_tde: ContainerTags should be nil, got %v", pkg.ContainerTags)
		}
	}
}

func TestVersionRoundtrip(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	p := &model.Package{
		Project:     "ppg:17",
		Name:        "percona-pg_tde",
		RollupState: model.RollupSucceeded,
		IsContainer: boolPtr(false),
		Version:     "17.5-1",
		Targets:     []model.Target{{Repo: "UBI_9", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   now,
	}
	if err := UpsertPackageState(db, p, now); err != nil {
		t.Fatal(err)
	}
	pkgs, err := QueryPackages(db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	if pkgs[0].Version != "17.5-1" {
		t.Errorf("Version: got %q, want %q", pkgs[0].Version, "17.5-1")
	}
	if pkgs[0].IsContainer != nil && *pkgs[0].IsContainer {
		t.Error("IsContainer: expected false")
	}

	// Container package
	c := &model.Package{
		Project:     "ppg:17:containers",
		Name:        "percona-distribution-postgresql",
		RollupState: model.RollupSucceeded,
		IsContainer: boolPtr(true),
		Version:     "18.4-1-1.7",
		Targets:     []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   now,
	}
	if err := UpsertPackageState(db, c, now); err != nil {
		t.Fatal(err)
	}
	pkgs2, err := QueryPackages(db, "")
	if err != nil {
		t.Fatal(err)
	}
	var found *model.Package
	for _, p := range pkgs2 {
		if p.Name == "percona-distribution-postgresql" {
			found = p
		}
	}
	if found == nil {
		t.Fatal("container package not found")
	}
	if found.Version != "18.4-1-1.7" {
		t.Errorf("Version: got %q, want %q", found.Version, "18.4-1-1.7")
	}
	if found.IsContainer == nil || !*found.IsContainer {
		t.Error("IsContainer: expected true")
	}
}

func TestIsContainerNullableMigration(t *testing.T) {
	// Simulate a database created before is_container became nullable.
	db, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	// Create old schema with NOT NULL constraint.
	if _, err := db.Exec(`CREATE TABLE packages (
		project          TEXT NOT NULL,
		name             TEXT NOT NULL,
		scope            TEXT NOT NULL,
		rollup_state     TEXT NOT NULL,
		ok_targets       INTEGER NOT NULL DEFAULT 0,
		total_targets    INTEGER NOT NULL DEFAULT 0,
		trigger_what     TEXT,
		trigger_kind     TEXT,
		trigger_at       DATETIME,
		targets_json     TEXT NOT NULL DEFAULT '[]',
		updated_at       DATETIME NOT NULL,
		state_changed_at DATETIME,
		is_container     INTEGER NOT NULL DEFAULT 0,
		version          TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (project, name)
	)`); err != nil {
		t.Fatal(err)
	}
	// Insert rows: one non-container (0) and one container (1).
	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO packages (project, name, scope, rollup_state, ok_targets, total_targets, targets_json, updated_at, is_container)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		"p", "rpm", "version", "succeeded", 1, 1, "[]", now, 0,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO packages (project, name, scope, rollup_state, ok_targets, total_targets, targets_json, updated_at, is_container)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		"p", "img", "container", "succeeded", 1, 1, "[]", now, 1,
	); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Re-open via Open() which should apply the migration automatically.
	// We can't use the same in-memory DB after Close(), so use a temp file.
	tmp := t.TempDir() + "/test.db"
	dbOld, err := sql.Open("sqlite", tmp+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	dbOld.SetMaxOpenConns(1)
	if _, err := dbOld.Exec(`CREATE TABLE packages (
		project          TEXT NOT NULL,
		name             TEXT NOT NULL,
		scope            TEXT NOT NULL,
		rollup_state     TEXT NOT NULL,
		ok_targets       INTEGER NOT NULL DEFAULT 0,
		total_targets    INTEGER NOT NULL DEFAULT 0,
		trigger_what     TEXT,
		trigger_kind     TEXT,
		trigger_at       DATETIME,
		targets_json     TEXT NOT NULL DEFAULT '[]',
		updated_at       DATETIME NOT NULL,
		state_changed_at DATETIME,
		is_container     INTEGER NOT NULL DEFAULT 0,
		version          TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (project, name)
	); CREATE TABLE events (
		id TEXT PRIMARY KEY, type TEXT NOT NULL, scope TEXT NOT NULL,
		project TEXT NOT NULL, package TEXT NOT NULL, repo TEXT, arch TEXT,
		what TEXT NOT NULL, why TEXT NOT NULL, url TEXT NOT NULL,
		at DATETIME NOT NULL, version TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := dbOld.Exec(
		`INSERT INTO packages (project, name, scope, rollup_state, ok_targets, total_targets, targets_json, updated_at, is_container)
		 VALUES (?,?,?,?,?,?,?,?,?),
		        (?,?,?,?,?,?,?,?,?)`,
		"p", "rpm", "version", "succeeded", 1, 1, "[]", now, 0,
		"p", "img", "container", "succeeded", 1, 1, "[]", now, 1,
	); err != nil {
		t.Fatal(err)
	}
	dbOld.Close()

	migratedDB, err := Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer migratedDB.Close()

	pkgs, err := QueryPackages(migratedDB, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(pkgs))
	}
	byName := map[string]*model.Package{}
	for _, p := range pkgs {
		byName[p.Name] = p
	}

	if byName["rpm"].IsContainer != nil {
		t.Errorf("rpm: is_container should be NULL after migration (was 0=default), got %v", byName["rpm"].IsContainer)
	}
	if byName["img"].IsContainer == nil || !*byName["img"].IsContainer {
		t.Errorf("img: is_container should be true after migration, got %v", byName["img"].IsContainer)
	}

	// Verify we can now insert a package with NULL is_container.
	newPkg := &model.Package{
		Project: "p", Name: "new", RollupState: "building",
		Targets: []model.Target{}, UpdatedAt: now,
	}
	if err := UpsertPackageState(migratedDB, newPkg, now); err != nil {
		t.Errorf("upsert with nil IsContainer failed: %v", err)
	}
}

func TestUpsertContainerTagInjection(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	trueVal := true
	now := time.Now().UTC().Truncate(time.Second)
	p := &model.Package{
		Project:     "ppg:17:containers:ubi9",
		Name:        "percona-postgresql17",
		Tags:        []string{"ppg"},
		IsContainer: &trueVal,
		RollupState: model.RollupSucceeded,
		Targets:     []model.Target{},
		UpdatedAt:   now,
	}
	if err := UpsertPackageState(db, p, now); err != nil {
		t.Fatal(err)
	}

	pkgs, err := QueryPackages(db, "ppg:17:containers:ubi9")
	if err != nil || len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d (err: %v)", len(pkgs), err)
	}
	tags := pkgs[0].Tags
	found := false
	for _, tg := range tags {
		if tg == "container" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected container tag in %v", tags)
	}
}

func TestStateChangedAt(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	t0 := time.Now().UTC().Truncate(time.Second)
	t1 := t0.Add(5 * time.Minute)
	t2 := t0.Add(10 * time.Minute)

	p := &model.Package{
		Project: "ppg:17", Name: "pg_tde",
		RollupState: model.RollupBuilding,
		Targets:     []model.Target{}, UpdatedAt: t0,
	}

	// First insert: state_changed_at must be set to t0.
	if err := UpsertPackageState(db, p, t0); err != nil {
		t.Fatal(err)
	}
	pkgs, err := QueryPackages(db, "ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if pkgs[0].StateChangedAt == nil {
		t.Fatal("state_changed_at should be set on first insert")
	}
	if !pkgs[0].StateChangedAt.Equal(t0) {
		t.Errorf("first insert: want state_changed_at=%v, got %v", t0, *pkgs[0].StateChangedAt)
	}

	// Same-state upsert: state_changed_at must not change.
	p.UpdatedAt = t1
	if err := UpsertPackageState(db, p, t1); err != nil {
		t.Fatal(err)
	}
	pkgs, err = QueryPackages(db, "ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if pkgs[0].StateChangedAt == nil || !pkgs[0].StateChangedAt.Equal(t0) {
		t.Errorf("same-state upsert: want state_changed_at=%v, got %v", t0, pkgs[0].StateChangedAt)
	}

	// State-change upsert: state_changed_at must update to t2.
	p.RollupState = model.RollupSucceeded
	p.UpdatedAt = t2
	if err := UpsertPackageState(db, p, t2); err != nil {
		t.Fatal(err)
	}
	pkgs, err = QueryPackages(db, "ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if pkgs[0].StateChangedAt == nil || !pkgs[0].StateChangedAt.Equal(t2) {
		t.Errorf("state-change upsert: want state_changed_at=%v, got %v", t2, pkgs[0].StateChangedAt)
	}
}

func TestSettledPersistedAndScanned(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := &model.Package{
		Project: "ppg:17", Name: "pkg-a",
		RollupState: model.RollupSucceeded, Settled: true,
		Targets:   []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt: time.Now().UTC(),
	}
	if err := UpsertPackageState(db, p, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, err := QueryPackages(db, "ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Settled {
		t.Fatalf("settled not persisted/scanned: %+v", got)
	}
}

func TestGetActivePackagesExcludesSettled(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	active := &model.Package{Project: "p", Name: "active", RollupState: model.RollupSucceeded, Settled: false, UpdatedAt: now}
	settled := &model.Package{Project: "p", Name: "settled", RollupState: model.RollupSucceeded, Settled: true, UpdatedAt: now}
	if err := UpsertPackageState(db, active, now); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPackageState(db, settled, now); err != nil {
		t.Fatal(err)
	}
	got, err := GetActivePackages(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "active" {
		t.Fatalf("GetActivePackages = %+v, want only 'active'", got)
	}
}

// seedCveRows inserts one cve_scans row and one cve_periods row for (project, pkg).
func seedCveRows(t *testing.T, db *sql.DB, project, pkg string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO cve_scans
		(project, package, arch, image_ref, scanned_at, critical_count, high_count, findings_json, cve_since)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		project, pkg, "x86_64", "ref", now.Format(time.RFC3339),
		1, 2, "[]", now.Add(-24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cve_periods (project, package, arch, cve_since, clean_since)
		VALUES (?,?,?,?,?)`,
		project, pkg, "x86_64",
		now.Add(-72*time.Hour).Format(time.RFC3339Nano), now.Add(-48*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func countCveRows(t *testing.T, db *sql.DB, project string) (scans, periods int) {
	t.Helper()
	if err := db.QueryRow(`SELECT COUNT(*) FROM cve_scans WHERE project = ?`, project).Scan(&scans); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM cve_periods WHERE project = ?`, project).Scan(&periods); err != nil {
		t.Fatal(err)
	}
	return scans, periods
}

// Deleting a package must remove its CVE rows — otherwise deleted PR projects'
// images linger in the Overview CVE table.
func TestDeletePackageRemovesCveRows(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	proj := "PR:pr-9:ppg:18:containers:ubi9"
	seedCveRows(t, db, proj, "img-a")
	seedCveRows(t, db, proj, "img-b")

	if err := DeletePackage(db, proj, "img-a"); err != nil {
		t.Fatal(err)
	}
	scans, periods := countCveRows(t, db, proj)
	if scans != 1 || periods != 1 {
		t.Fatalf("expected only img-b rows to remain, got scans=%d periods=%d", scans, periods)
	}
}

func TestDeletePackagesByProjectRemovesCveRows(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	proj := "PR:pr-9:ppg:18:containers:ubi9"
	other := "ppg:18:containers:ubi9"
	seedCveRows(t, db, proj, "img-a")
	seedCveRows(t, db, other, "img-keep")

	if err := DeletePackagesByProject(db, proj); err != nil {
		t.Fatal(err)
	}
	scans, periods := countCveRows(t, db, proj)
	if scans != 0 || periods != 0 {
		t.Fatalf("deleted project still has cve rows: scans=%d periods=%d", scans, periods)
	}
	scans, periods = countCveRows(t, db, other)
	if scans != 1 || periods != 1 {
		t.Fatalf("unrelated project's cve rows were removed: scans=%d periods=%d", scans, periods)
	}
}

func TestQueryStaleBlockedTargets(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	old := now.Add(-45 * time.Minute)
	fresh := now.Add(-5 * time.Minute)

	insertPkg := func(project, name string, isRelease int) {
		if _, err := db.Exec(`INSERT INTO packages (project, name, rollup_state, ok_targets, total_targets, targets_json, is_release, updated_at)
			VALUES (?, ?, 'blocked', 0, 0, '[]', ?, ?)`, project, name, isRelease, now); err != nil {
			t.Fatal(err)
		}
	}
	insertDur := func(project, pkg, repo, arch, state string, enteredAt time.Time, exited bool) {
		// Seed timestamps exactly as the production write path
		// (recordStateTransitions) does: RFC3339Nano strings in UTC.
		exitedAt := any(nil)
		if exited {
			exitedAt = now.UTC().Format(time.RFC3339Nano)
		}
		if _, err := db.Exec(`INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at, exited_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, project, pkg, repo, arch, state,
			enteredAt.UTC().Format(time.RFC3339Nano), exitedAt); err != nil {
			t.Fatal(err)
		}
	}

	insertPkg("ppg:devel:17", "stuck_pkg", 0)
	insertDur("ppg:devel:17", "stuck_pkg", "Fedora_42", "x86_64", "blocked", old, false)

	insertPkg("ppg:staging:17", "fresh_pkg", 0)
	insertDur("ppg:staging:17", "fresh_pkg", "Fedora_42", "x86_64", "blocked", fresh, false)

	insertPkg("ppg:devel:16", "building_pkg", 0)
	insertDur("ppg:devel:16", "building_pkg", "Fedora_42", "x86_64", "building", old, false)

	insertPkg("ppg:devel:15", "ended_pkg", 0)
	insertDur("ppg:devel:15", "ended_pkg", "Fedora_42", "x86_64", "blocked", old, true)

	insertPkg("ppg:releases:17", "release_pkg", 1)
	insertDur("ppg:releases:17", "release_pkg", "Fedora_42", "x86_64", "blocked", old, false)

	got, err := QueryStaleBlockedTargets(db, now.Add(-30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly [stuck_pkg], got %d rows: %+v", len(got), got)
	}
	bt := got[0]
	if bt.Project != "ppg:devel:17" || bt.Package != "stuck_pkg" ||
		bt.Repo != "Fedora_42" || bt.Arch != "x86_64" {
		t.Fatalf("unexpected target: %+v", bt)
	}
	if !bt.EnteredAt.Equal(old) {
		t.Fatalf("EnteredAt = %v, want %v", bt.EnteredAt, old)
	}
}

// Rows orphaned before the delete-path fix are swept on startup.
func TestCleanupOrphanedCveRows(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()

	// Live package with CVE rows — must survive.
	live := &model.Package{Project: "ppg:18:containers:ubi9", Name: "img-live",
		RollupState: model.RollupPublished, UpdatedAt: now}
	if err := UpsertPackageState(db, live, now); err != nil {
		t.Fatal(err)
	}
	seedCveRows(t, db, live.Project, live.Name)
	// Orphan: no packages row.
	seedCveRows(t, db, "PR:pr-999:ppg:17:containers:ubi9", "img-gone")

	if err := cleanupOrphanedCveRows(db); err != nil {
		t.Fatal(err)
	}
	scans, periods := countCveRows(t, db, "PR:pr-999:ppg:17:containers:ubi9")
	if scans != 0 || periods != 0 {
		t.Fatalf("orphaned cve rows survived cleanup: scans=%d periods=%d", scans, periods)
	}
	scans, periods = countCveRows(t, db, live.Project)
	if scans != 1 || periods != 1 {
		t.Fatalf("live package's cve rows were removed: scans=%d periods=%d", scans, periods)
	}
}

// Startup backfill: settled, built, non-container packages with no version
// (aggregates resolved by VersionTask after the fact) are re-seeded once.
func TestGetVersionlessBuiltPackages(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	f, tr := false, true
	pkgs := []*model.Package{
		{Project: "p", Name: "aggregate", RollupState: model.RollupPublished, Settled: true, IsContainer: &f, UpdatedAt: now},
		{Project: "p", Name: "built-succeeded", RollupState: model.RollupSucceeded, Settled: true, IsContainer: &f, UpdatedAt: now},
		{Project: "p", Name: "has-version", RollupState: model.RollupPublished, Settled: true, IsContainer: &f, Version: "1.0-1", UpdatedAt: now},
		{Project: "p", Name: "container", RollupState: model.RollupPublished, Settled: true, IsContainer: &tr, UpdatedAt: now},
		{Project: "p", Name: "failed", RollupState: model.RollupFailed, Settled: true, IsContainer: &f, UpdatedAt: now},
		{Project: "p", Name: "unsettled", RollupState: model.RollupPublished, Settled: false, IsContainer: &f, UpdatedAt: now},
	}
	for _, p := range pkgs {
		if err := UpsertPackageState(db, p, now); err != nil {
			t.Fatal(err)
		}
	}
	got, err := GetVersionlessBuiltPackages(db)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	want := []string{"aggregate", "built-succeeded"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Fatalf("GetVersionlessBuiltPackages = %v, want %v", names, want)
	}
}
