package cve_test

import (
	"context"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/cve"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

func TestSchedulerEnqueuesUnscannedOnStartup(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert a published container package.
	isContainer := true
	pkg := &model.Package{
		Project:       "isv:percona:ppg:17:containers:ubi9",
		Name:          "mypkg",
		RollupState:   model.RollupPublished,
		IsContainer:   &isContainer,
		ContainerTags: []string{"1.0-1"},
		Targets:       []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:     time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, time.Now().UTC()); err != nil {
		t.Fatal("setup: upsert package:", err)
	}

	enqueued := make(chan cve.ScanRequest, 10)
	h := hub.New()
	scanner := cve.NewScanner(db, h, 0, cve.WithEnqueueFn(func(req cve.ScanRequest) {
		enqueued <- req
	}))

	sched := cve.NewNightlyScheduler(db, scanner)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	go sched.Run(ctx)
	<-ctx.Done()

	if len(enqueued) == 0 {
		t.Fatal("expected scheduler to enqueue the unscanned package on startup")
	}
	req := <-enqueued
	if req.Package != "mypkg" {
		t.Errorf("unexpected package: %q", req.Package)
	}
	if req.PrimaryTag != "1.0-1" {
		t.Errorf("expected PrimaryTag %q, got %q", "1.0-1", req.PrimaryTag)
	}
}

func TestSchedulerSkipsAlreadyScanned(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	isContainer := true
	pkg := &model.Package{
		Project:       "isv:percona",
		Name:          "scanned-pkg",
		RollupState:   model.RollupPublished,
		IsContainer:   &isContainer,
		ContainerTags: []string{"2.0"},
		UpdatedAt:     time.Now().UTC(),
	}
	if err := store.UpsertPackageState(db, pkg, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Pre-populate a cve_scan row to simulate "already scanned".
	if err := store.UpsertCveScan(db, "isv:percona", "scanned-pkg", model.CveScan{
		Arch: "x86_64", ImageRef: "reg/img:2.0", ScannedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	enqueued := make(chan cve.ScanRequest, 10)
	h := hub.New()
	scanner := cve.NewScanner(db, h, 0, cve.WithEnqueueFn(func(req cve.ScanRequest) {
		enqueued <- req
	}))

	sched := cve.NewNightlyScheduler(db, scanner)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go sched.Run(ctx)
	<-ctx.Done()

	if len(enqueued) > 0 {
		t.Errorf("expected already-scanned package to be skipped, but got %d enqueued", len(enqueued))
	}
}

func TestRunQueuesUnclassifiedAtStartup(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	putPackage(t, db, "isv:percona", "old", model.RollupPublished, "images")
	putFindingScan(t, db, "isv:percona", "old", "images", "")

	enqueued := make(chan cve.ScanRequest, 10)
	scanner := cve.NewScanner(db, hub.New(), 0, cve.WithEnqueueFn(func(req cve.ScanRequest) {
		enqueued <- req
	}))
	sched := cve.NewNightlyScheduler(db, scanner)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go sched.Run(ctx)
	<-ctx.Done()

	if len(enqueued) != 1 {
		t.Fatalf("enqueued %d requests, want 1", len(enqueued))
	}
	if req := <-enqueued; req.Package != "old" {
		t.Errorf("unexpected package %q", req.Package)
	}
}
