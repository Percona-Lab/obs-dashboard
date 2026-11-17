package cve_test

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/cve"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

const trivyCleanJSON = `{"SchemaVersion":2,"Results":[]}`
const trivyVulnJSON = `{"SchemaVersion":2,"Results":[{"Vulnerabilities":[
	{"VulnerabilityID":"CVE-2024-1","PkgName":"libssl3","InstalledVersion":"3.1.0","FixedVersion":"3.1.1","Severity":"CRITICAL","Title":"overflow"},
	{"VulnerabilityID":"CVE-2024-2","PkgName":"libz","InstalledVersion":"1.2.11","FixedVersion":"1.2.12","Severity":"HIGH","Title":"zlib bug"}
]}]}`

func TestSucceededTargets(t *testing.T) {
	targets := []model.Target{
		{Arch: "x86_64", State: "succeeded"},
		{Arch: "aarch64", State: "building"},
		{Arch: "s390x", State: "succeeded"},
	}
	got := cve.SucceededTargets(targets)
	if len(got) != 2 {
		t.Errorf("expected 2, got %d", len(got))
	}
}

func TestScannerEnqueueDropsWhenFull(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := hub.New()

	blocked := make(chan struct{})
	s := cve.NewScanner(db, h, 1, cve.WithExecFn(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-blocked
		return []byte(trivyCleanJSON), nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	req := cve.ScanRequest{
		Project: "p", Package: "pkg", PrimaryTag: "1.0",
		Targets: []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
	}
	for i := 0; i < 102; i++ {
		s.Enqueue(req)
	}
	close(blocked)
}

func TestScannerParsesTrivyVulns(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := hub.New()

	execDone := make(chan struct{})
	s := cve.NewScanner(db, h, 1, cve.WithExecFn(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		defer close(execDone)
		return []byte(trivyVulnJSON), nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	req := cve.ScanRequest{
		Project: "proj", Package: "mypkg", PrimaryTag: "1.0",
		Targets: []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
	}
	s.Enqueue(req)

	select {
	case <-execDone:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish trivy exec in time")
	}
	// Give the worker a moment to complete upsert after exec returns.
	time.Sleep(50 * time.Millisecond)

	scans, err := store.QueryCveScans(db, "proj", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if len(scans) != 1 {
		t.Fatalf("expected 1 scan, got %d", len(scans))
	}
	if scans[0].CriticalCount != 1 || scans[0].HighCount != 1 {
		t.Errorf("wrong counts: critical=%d high=%d", scans[0].CriticalCount, scans[0].HighCount)
	}
}

func TestScanPackageBuildsPerTargetImageRef(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := hub.New()

	var mu sync.Mutex
	var refs []string
	s := cve.NewScanner(db, h, 1, cve.WithExecFn(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		mu.Lock()
		refs = append(refs, args[len(args)-1]) // trivy image ref is the last arg
		mu.Unlock()
		return []byte(trivyCleanJSON), nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	s.Enqueue(cve.ScanRequest{
		Project: "isv:percona:ppg:staging:17:containers", Package: "pg", PrimaryTag: "17.5-1",
		Targets: []model.Target{
			{Repo: "ubi8", Arch: "x86_64", State: "succeeded"},
			{Repo: "ubi9", Arch: "x86_64", State: "succeeded"},
		},
	})

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(refs)
		mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected 2 image refs, got %d", n)
		case <-time.After(20 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := map[string]bool{
		"registry.opensuse.org/isv/percona/ppg/staging/17/containers/ubi8/pg:17.5-1": false,
		"registry.opensuse.org/isv/percona/ppg/staging/17/containers/ubi9/pg:17.5-1": false,
	}
	for _, r := range refs {
		if _, ok := want[r]; !ok {
			t.Fatalf("unexpected image ref %q", r)
		}
		want[r] = true
	}
	for r, seen := range want {
		if !seen {
			t.Fatalf("missing image ref %q", r)
		}
	}
}

func TestWithEnqueueFn(t *testing.T) {
	db, _ := store.Open(":memory:")
	defer db.Close()
	h := hub.New()

	received := make(chan cve.ScanRequest, 1)
	s := cve.NewScanner(db, h, 0, cve.WithEnqueueFn(func(r cve.ScanRequest) {
		received <- r
	}))

	req := cve.ScanRequest{Project: "p", Package: "pkg"}
	s.Enqueue(req)

	select {
	case got := <-received:
		if got.Package != "pkg" {
			t.Errorf("expected pkg, got %q", got.Package)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("enqueueFn was not called")
	}
}

type stubURLs struct{}

func (stubURLs) ImageBase(slug, project, repo, name string) string {
	return "reg." + slug + "/" + project + "/" + repo + "/" + name
}
func (stubURLs) PackageURL(slug, project, pkg string) string {
	return "https://" + slug + "/" + project + "/" + pkg
}

func TestScannerUsesTargetInstanceURLs(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var gotRef string
	exec := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		gotRef = args[len(args)-1]
		return []byte(`{"Results":[]}`), nil
	}
	s := cve.NewScanner(db, hub.New(), 1, cve.WithExecFn(exec), cve.WithURLs(stubURLs{}))
	s.ScanNow(context.Background(), cve.ScanRequest{
		Project: "ppg:17:containers", Package: "pg", PrimaryTag: "17.5",
		Targets: []model.Target{{Repo: "ubi9", Arch: "x86_64", State: "succeeded", Instance: "percona"}},
	})
	if gotRef != "reg.percona/ppg:17:containers/ubi9/pg:17.5" {
		t.Errorf("image ref = %q", gotRef)
	}
}

func TestParseTrivyOutputClassificationFields(t *testing.T) {
	data, err := os.ReadFile("testdata/trivy-listall.json")
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := cve.NewScanner(db, hub.New(), 1, cve.WithExecFn(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return data, nil
	}))
	s.ScanNow(context.Background(), cve.ScanRequest{
		Project: "proj", Package: "mypkg", PrimaryTag: "1.0",
		Targets: []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
	})
	scans, err := store.QueryCveScans(db, "proj", "mypkg")
	if err != nil || len(scans) != 1 {
		t.Fatalf("scans=%d err=%v", len(scans), err)
	}
	scan := scans[0]
	if scan.CriticalCount != 2 || scan.HighCount != 6 {
		t.Errorf("counts: critical=%d high=%d", scan.CriticalCount, scan.HighCount)
	}
	byID := map[string]model.CveFinding{}
	for _, f := range scan.Findings {
		byID[f.ID] = f
	}
	py := "usr/lib/python3.12/site-packages/dnspython-2.9.0.dist-info/METADATA"
	want := []struct{ id, class, typ, path, src, vendor, owner string }{
		{"CVE-2026-0001", "os-pkgs", "redhat", "", "krb5", "Red Hat, Inc.", ""},
		{"CVE-2026-0002", "os-pkgs", "redhat", "", "percona-pgbouncer", "Percona, LLC", ""},
		{"CVE-2026-0003", "os-pkgs", "redhat", "", "", "", ""},
		{"CVE-2026-0004", "lang-pkgs", "gobinary", "usr/sbin/gosu", "", "", "gosu"},
		{"CVE-2026-0005", "lang-pkgs", "gobinary", "usr/sbin/gosu", "", "", "gosu"},
		{"CVE-2026-0006", "lang-pkgs", "gobinary", "usr/local/bin/mystery", "", "", ""},
		{"CVE-2026-0007", "lang-pkgs", "python-pkg", py, "", "", "python3.12-dns"},
		{"CVE-2026-0008", "lang-pkgs", "python-pkg", "opt/app/requests-2.0.0.dist-info/METADATA", "", "", ""},
	}
	for _, w := range want {
		f, ok := byID[w.id]
		if !ok {
			t.Errorf("%s missing", w.id)
			continue
		}
		got := [6]string{f.Class, f.Type, f.Path, f.Src, f.Vendor, f.Owner}
		exp := [6]string{w.class, w.typ, w.path, w.src, w.vendor, w.owner}
		if got != exp {
			t.Errorf("%s: got %q want %q", w.id, got, exp)
		}
	}
}

func TestRunTrivyListsAllPackages(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var args []string
	s := cve.NewScanner(db, hub.New(), 1, cve.WithExecFn(func(ctx context.Context, name string, a ...string) ([]byte, error) {
		args = a
		return []byte(trivyCleanJSON), nil
	}))
	s.ScanNow(context.Background(), cve.ScanRequest{
		Project: "proj", Package: "mypkg", PrimaryTag: "1.0",
		Targets: []model.Target{{Repo: "images", Arch: "x86_64", State: "succeeded"}},
	})
	if !slices.Contains(args, "--list-all-pkgs") {
		t.Errorf("args missing --list-all-pkgs: %v", args)
	}
}
