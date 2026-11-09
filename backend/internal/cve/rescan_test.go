package cve_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/cve"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

func putPackage(t *testing.T, db *sql.DB, project, name string, state model.RollupState, repos ...string) {
	t.Helper()
	isContainer := true
	var targets []model.Target
	for _, r := range repos {
		targets = append(targets, model.Target{Repo: r, Arch: "x86_64", State: "succeeded"})
	}
	pkg := &model.Package{
		Project: project, Name: name, RollupState: state, IsContainer: &isContainer,
		ContainerTags: []string{"1.0"}, Targets: targets, UpdatedAt: time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func putScan(t *testing.T, db *sql.DB, project, name, repo string, high int) {
	t.Helper()
	if err := store.UpsertCveScan(db, project, name, model.CveScan{
		Repo: repo, Arch: "x86_64", ImageRef: "ref", ScannedAt: time.Now().UTC(), HighCount: high,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRescanVulnerableQueuesOnlyVulnerableRepos(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	putPackage(t, db, "p", "vuln", model.RollupPublished, "images", "clean")
	putScan(t, db, "p", "vuln", "images", 2)
	putScan(t, db, "p", "vuln", "clean", 0)
	putPackage(t, db, "p", "building", model.RollupBuilding, "images")
	putScan(t, db, "p", "building", "images", 1)
	putScan(t, db, "p", "gone", "images", 1) // scan row without a package
	putPackage(t, db, "p", "ok", model.RollupPublished, "images")
	putScan(t, db, "p", "ok", "images", 0)

	got := make(chan cve.ScanRequest, 10)
	s := cve.NewScanner(db, hub.New(), 0, cve.WithEnqueueFn(func(r cve.ScanRequest) { got <- r }))
	n, err := s.RescanVulnerable()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("queued %d images, want 1", n)
	}
	select {
	case req := <-got:
		if req.Package != "vuln" || req.PrimaryTag != "1.0" {
			t.Errorf("unexpected request %+v", req)
		}
		if len(req.Targets) != 1 || req.Targets[0].Repo != "images" {
			t.Errorf("targets = %+v, want only repo images", req.Targets)
		}
	case <-time.After(time.Second):
		t.Fatal("no request enqueued")
	}
	select {
	case req := <-got:
		t.Errorf("unexpected extra request %+v", req)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRescanVulnerableSkipsPendingUntilScanned(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	putPackage(t, db, "p", "vuln", model.RollupPublished, "images")
	putScan(t, db, "p", "vuln", "images", 2)

	release := make(chan struct{})
	scanned := make(chan struct{}, 1)
	s := cve.NewScanner(db, hub.New(), 1, cve.WithExecFn(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-release
		select {
		case scanned <- struct{}{}:
		default:
		}
		return []byte(trivyVulnJSON), nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	if n, err := s.RescanVulnerable(); err != nil || n != 1 {
		t.Fatalf("first rescan: n=%d err=%v, want 1", n, err)
	}
	if n, err := s.RescanVulnerable(); err != nil || n != 0 {
		t.Fatalf("second rescan while pending: n=%d err=%v, want 0", n, err)
	}
	close(release)
	<-scanned
	// The pending mark clears once the worker finishes the package.
	deadline := time.Now().Add(2 * time.Second)
	for {
		n, err := s.RescanVulnerable()
		if err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rescan still skipped after the scan finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
