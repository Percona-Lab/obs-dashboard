package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
)

func TestBuildReleasePackageArtifactsUsesDistributableMTime(t *testing.T) {
	items := []obs.BinaryArtifact{
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "openSUSE_Tumbleweed",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd-3.5.30-2.1.x86_64.rpm",
			Size:     10,
			MTime:    1779201973,
			BuiltAt:  time.Unix(1779201973, 0).UTC(),
		},
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "openSUSE_Tumbleweed",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd-debugsource-3.5.30-2.1.x86_64.rpm",
			Size:     20,
			MTime:    1779202000,
			BuiltAt:  time.Unix(1779202000, 0).UTC(),
		},
	}

	artifacts := buildReleasePackageArtifacts(items, nil) // nil versions → empty Version
	if len(artifacts) != 1 {
		t.Fatalf("expected one package artifact, got %d", len(artifacts))
	}
	if len(artifacts[0].Binaries) != 1 {
		t.Fatalf("expected one distributable binary, got %d", len(artifacts[0].Binaries))
	}
	if artifacts[0].BuiltAt != "2026-05-19T14:46:13Z" {
		t.Fatalf("BuiltAt = %q", artifacts[0].BuiltAt)
	}
	if artifacts[0].Binaries[0].MTime != 1779201973 {
		t.Fatalf("binary MTime = %d", artifacts[0].Binaries[0].MTime)
	}
}

func TestBuildReleasePackageArtifactsVersion(t *testing.T) {
	items := []obs.BinaryArtifact{
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "openSUSE_Tumbleweed",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd-3.5.30-2.1.x86_64.rpm",
			Size:     10,
			MTime:    1779201973,
			BuiltAt:  time.Unix(1779201973, 0).UTC(),
		},
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "Ubuntu_24.04",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd_3.5.30-2ubuntu1_amd64.deb",
			Size:     10,
			MTime:    1779201973,
			BuiltAt:  time.Unix(1779201973, 0).UTC(),
		},
	}

	versions := map[string]string{
		"openSUSE_Tumbleweed\x00x86_64\x00etcd.rpm": "3.5.30-2.1",
		// Ubuntu_24.04 intentionally absent — Version should stay ""
	}

	artifacts := buildReleasePackageArtifacts(items, versions)

	var openSUSE, ubuntu *ReleasePackageArtifact
	for i := range artifacts {
		switch artifacts[i].Repo {
		case "openSUSE_Tumbleweed":
			openSUSE = &artifacts[i]
		case "Ubuntu_24.04":
			ubuntu = &artifacts[i]
		}
	}

	if openSUSE == nil {
		t.Fatal("openSUSE artifact missing")
	}
	if openSUSE.Version != "3.5.30-2.1" {
		t.Errorf("openSUSE Version = %q, want '3.5.30-2.1'", openSUSE.Version)
	}
	if ubuntu == nil {
		t.Fatal("Ubuntu artifact missing")
	}
	if ubuntu.Version != "" {
		t.Errorf("Ubuntu Version = %q, want ''", ubuntu.Version)
	}
}

func TestBinaryBaseName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"postgresql16-16.4-2.3.x86_64.rpm", "postgresql16.rpm"},
		{"postgresql16-devel-16.4-2.3.x86_64.rpm", "postgresql16-devel.rpm"},
		{"perl-YAML-LibYAML-0.88-1.1.noarch.rpm", "perl-YAML-LibYAML.rpm"},
		{"etcd-3.5.30-2.1.x86_64.rpm", "etcd.rpm"},
		{"etcd_3.5.30-2ubuntu1_amd64.deb", "etcd.deb"},
		{"postgresql-16_16.4-2ubuntu1_amd64.deb", "postgresql-16.deb"},
		{"something.containerinfo", "something.containerinfo"},
	}
	for _, c := range cases {
		got := binaryBaseName(c.in)
		if got != c.want {
			t.Errorf("binaryBaseName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBaseOSFromRepo(t *testing.T) {
	cases := map[string]string{
		"ubi8": "UBI 8", "ubi9": "UBI 9", "ubi10": "UBI 10", "ubi11": "UBI 11",
		"ubi": "", "ubix": "", "ubi10-minimal": "",
		"noble": "Ubuntu 24.04 Noble", "bookworm": "Debian 12 Bookworm",
		"images": "", "": "", "weird": "",
	}
	for repo, want := range cases {
		if got := baseOSFromRepo(repo); got != want {
			t.Errorf("baseOSFromRepo(%q) = %q, want %q", repo, got, want)
		}
	}
}

func TestDeriveBaseOS(t *testing.T) {
	// New layout: the repo carries the base image.
	if got := deriveBaseOS("isv:percona:ppg:staging:17:containers", "ubi9"); got != "UBI 9" {
		t.Errorf("new layout: got %q, want UBI 9", got)
	}
	// New layout with a UBI version the code has never seen before.
	if got := deriveBaseOS("PR:pr-101:ppg:staging:17:containers", "ubi10"); got != "UBI 10" {
		t.Errorf("ubi10: got %q, want UBI 10", got)
	}
	// Old layout: repo is "images", base image in the project name.
	if got := deriveBaseOS("isv:percona:ppg:17:containers:ubi8", "images"); got != "UBI 8" {
		t.Errorf("old layout: got %q, want UBI 8", got)
	}
	// Unrecognisable → project-name fallback returns the project string.
	if got := deriveBaseOS("isv:percona:ppg:weird", "images"); got != "isv:percona:ppg:weird" {
		t.Errorf("fallback: got %q, want the project string", got)
	}
}

func TestBuildReleaseContainerArtifactsPerRepo(t *testing.T) {
	// Tags endpoint 404s → tags stay empty (tolerated); we assert keying,
	// base OS, registry, and repo per artifact.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	client := obs.SingleFleet(obs.NewClient(srv.URL, "u", "p"), "isv:percona")

	binaries := []obs.BinaryArtifact{
		{Project: "isv:percona:ppg:staging:17:containers", Package: "pg", Repo: "ubi8", Arch: "x86_64", Filename: "pg.containerinfo"},
		{Project: "isv:percona:ppg:staging:17:containers", Package: "pg", Repo: "ubi9", Arch: "x86_64", Filename: "pg.containerinfo"},
	}
	out := buildReleaseContainerArtifacts(context.Background(), client, binaries)
	if len(out) != 2 {
		t.Fatalf("got %d artifacts, want 2 (ubi8 + ubi9)", len(out))
	}
	byOS := map[string]ReleaseContainerArtifact{}
	for _, a := range out {
		byOS[a.BaseOS] = a
	}
	if byOS["UBI 8"].Registry != "registry.opensuse.org/isv/percona/ppg/staging/17/containers/ubi8/pg" {
		t.Errorf("ubi8 registry = %q", byOS["UBI 8"].Registry)
	}
	if byOS["UBI 9"].Registry != "registry.opensuse.org/isv/percona/ppg/staging/17/containers/ubi9/pg" {
		t.Errorf("ubi9 registry = %q", byOS["UBI 9"].Registry)
	}
	if byOS["UBI 8"].Repo != "ubi8" || byOS["UBI 9"].Repo != "ubi9" {
		t.Errorf("repo not set per artifact: %+v", out)
	}
}

func TestBuildReleaseTarballArtifacts(t *testing.T) {
	base := time.Date(2026, 9, 17, 5, 0, 0, 0, time.UTC)
	proj := "isv:percona:ppg:releases:17:tarballs"
	binaries := []obs.BinaryArtifact{
		{Project: proj, Package: "percona-postgresql-tarball", Repo: "ssl3", Arch: "x86_64", Filename: "percona-postgresql-17.11-ssl3-linux-x86_64.tar.gz", MTime: base.Unix(), BuiltAt: base},
		{Project: proj, Package: "percona-postgresql-tarball", Repo: "ssl3", Arch: "aarch64", Filename: "percona-postgresql-17.11-ssl3-linux-aarch64.tar.gz", MTime: base.Unix(), BuiltAt: base},
		{Project: proj, Package: "percona-postgresql-tarball", Repo: "ssl1.1", Arch: "x86_64", Filename: "percona-postgresql-17.11-ssl1.1-linux-x86_64.tar.gz", MTime: base.Unix(), BuiltAt: base},
		// RockyLinux build inside :tarballs — NOT a tarball, must be excluded.
		{Project: proj, Package: "percona-psql", Repo: "RockyLinux_9", Arch: "x86_64", Filename: "percona-psql.rpm", MTime: base.Unix(), BuiltAt: base},
		// .repo metadata under an ssl repo — not a .tar.gz, must be excluded.
		{Project: proj, Package: "percona-postgresql-tarball", Repo: "ssl3", Arch: "x86_64", Filename: "isv:percona:ppg:releases:17:tarballs.repo", MTime: base.Unix(), BuiltAt: base},
	}

	out := buildReleaseTarballArtifacts(binaries)

	if len(out) != 3 {
		t.Fatalf("want 3 tarball artifacts, got %d: %+v", len(out), out)
	}
	for _, a := range out {
		if !strings.HasPrefix(a.Repo, "ssl") {
			t.Errorf("non-ssl repo leaked: %q", a.Repo)
		}
		if a.Name != "percona-postgresql-tarball" {
			t.Errorf("unexpected name %q", a.Name)
		}
		if a.BuiltAt == "" {
			t.Errorf("built_at not set for %s/%s", a.Repo, a.Arch)
		}
		if a.Version != "17.11" {
			t.Errorf("version not parsed for %s/%s: got %q", a.Repo, a.Arch, a.Version)
		}
	}
	// Sorted by repo then name then arch → ssl1.1 first.
	if out[0].Repo != "ssl1.1" {
		t.Errorf("want ssl1.1 first, got %q", out[0].Repo)
	}
}

func TestTarballFileVersion(t *testing.T) {
	cases := []struct {
		filename, repo, arch, want string
	}{
		{"percona-postgresql-18.6-ssl3-linux-x86_64.tar.gz", "ssl3", "x86_64", "18.6"},
		{"percona-postgresql-17.11-ssl1.1-linux-aarch64.tar.gz", "ssl1.1", "aarch64", "17.11"},
		{"percona-postgresql-16.15-ssl3.5-linux-x86_64.tar.gz", "ssl3.5", "x86_64", "16.15"},
		// Wrong shape / metadata → no version.
		{"isv:percona:ppg:releases:17:tarballs.repo", "ssl3", "x86_64", ""},
		{"percona-postgresql-tarball.tar.gz", "ssl3", "x86_64", ""},
	}
	for _, c := range cases {
		if got := tarballFileVersion(c.filename, c.repo, c.arch); got != c.want {
			t.Errorf("tarballFileVersion(%q,%q,%q) = %q, want %q", c.filename, c.repo, c.arch, got, c.want)
		}
	}
}

func TestAttachReleaseCveScansFiltersByRepo(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	seed := func(repo string, crit int) {
		if err := store.UpsertCveScan(db, "isv:percona:ppg:staging:17:containers", "pg",
			model.CveScan{Repo: repo, Arch: "x86_64", ImageRef: "r", ScannedAt: now, CriticalCount: crit}); err != nil {
			t.Fatal(err)
		}
	}
	seed("ubi8", 0)
	seed("ubi9", 5)

	images := []ReleaseContainerArtifact{
		{Project: "isv:percona:ppg:staging:17:containers", ImageName: "pg", Repo: "ubi8"},
		{Project: "isv:percona:ppg:staging:17:containers", ImageName: "pg", Repo: "ubi9"},
	}
	attachReleaseCveScans(db, images)
	for _, img := range images {
		if len(img.CveScans) != 1 || img.CveScans[0].Repo != img.Repo {
			t.Fatalf("%s: got %d scans (want 1 for its own repo): %+v", img.Repo, len(img.CveScans), img.CveScans)
		}
	}
	// ubi9 carries the CVE, ubi8 is clean.
	for _, img := range images {
		if img.Repo == "ubi9" && img.CveScans[0].CriticalCount != 5 {
			t.Fatalf("ubi9 critical = %d, want 5", img.CveScans[0].CriticalCount)
		}
		if img.Repo == "ubi8" && img.CveScans[0].CriticalCount != 0 {
			t.Fatalf("ubi8 critical = %d, want 0", img.CveScans[0].CriticalCount)
		}
	}
}

func TestSubprojectCandidatesIncludesFlatProject(t *testing.T) {
	base := "isv:percona:ppg:releases:18"

	eq := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	// New flat layout: SearchProjects finds nothing (its XPath appends ':'),
	// yet the flat :containers project must still be a candidate.
	if got := subprojectCandidates(base, "containers", nil); !eq(got, []string{base + ":containers"}) {
		t.Fatalf("flat layout: got %v", got)
	}

	// Old nested layout: flat first, then the nested project; a duplicate of the
	// flat project in the search results is deduped.
	got := subprojectCandidates(base, "containers", []string{
		base + ":containers",
		base + ":containers:ubi9",
	})
	if !eq(got, []string{base + ":containers", base + ":containers:ubi9"}) {
		t.Fatalf("nested layout: got %v", got)
	}

	// Tarballs flat project present even with an empty search result.
	if got := subprojectCandidates(base, "tarballs", nil); !eq(got, []string{base + ":tarballs"}) {
		t.Fatalf("tarballs flat: got %v", got)
	}
}

func TestBaseOSLess(t *testing.T) {
	labels := []string{"Ubuntu 24.04 Noble", "UBI 10", "custom", "UBI 9", "Debian 12 Bookworm", "UBI 8"}
	sort.Slice(labels, func(i, j int) bool { return baseOSLess(labels[i], labels[j]) })
	want := []string{"UBI 8", "UBI 9", "UBI 10", "Debian 12 Bookworm", "Ubuntu 24.04 Noble", "custom"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Errorf("order = %v, want %v", labels, want)
	}
}

func TestBuildReleaseContainerArtifactsOrdersUBIByVersion(t *testing.T) {
	var bins []obs.BinaryArtifact
	for _, repo := range []string{"ubi10", "ubi8", "ubi9"} {
		bins = append(bins, obs.BinaryArtifact{Project: "ppg:releases:17:containers", Repo: repo, Arch: "x86_64",
			Package: "pg", Filename: "pg.containerinfo"})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // no tags needed for ordering
	}))
	defer srv.Close()
	client := obs.SingleFleet(obs.NewClient(srv.URL, "u", "p"), "isv:percona")
	got := buildReleaseContainerArtifacts(context.Background(), client, bins)
	var order []string
	for _, a := range got {
		order = append(order, a.BaseOS)
	}
	if strings.Join(order, "|") != "UBI 8|UBI 9|UBI 10" {
		t.Errorf("release container order = %v, want UBI 8, UBI 9, UBI 10", order)
	}
}
