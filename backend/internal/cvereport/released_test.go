package cvereport

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

const (
	relProj = "ppg:releases:18:containers"
	stgProj = "ppg:staging:18:containers"
)

func relFindings() []model.CveFinding {
	return []model.CveFinding{
		{ID: "CVE-A", PkgName: "openssl", InstalledVersion: "1", FixedVersion: "2", Severity: "HIGH", Title: "a",
			Class: "os-pkgs", Vendor: "Red Hat, Inc."},
		{ID: "CVE-B", PkgName: "stdlib", InstalledVersion: "1.24", FixedVersion: "1.25", Severity: "CRITICAL", Title: "b",
			Class: "lang-pkgs", Type: "gobinary", Path: "usr/bin/etcd", Owner: "etcd"},
		{ID: "CVE-C", PkgName: "gnutls", InstalledVersion: "1", FixedVersion: "2", Severity: "HIGH", Title: "c",
			Class: "os-pkgs", Vendor: "Red Hat, Inc."},
	}
}

func relScans() []model.ImageCveScan {
	return []model.ImageCveScan{
		scan(relProj, "pg", "ubi9", "x86_64", relFindings()...),
		scan(relProj, "pg", "ubi9", "aarch64", relFindings()...),
	}
}

var testVersions = map[string]string{
	relProj + "/pg": "18.4-1-1.1",
	stgProj + "/pg": "18.4-1-2.34",
}

func statusOf(im ReleasedImage) map[string]ReleasedCVE {
	m := map[string]ReleasedCVE{}
	for _, c := range im.CVEs {
		m[c.ID] = c
	}
	return m
}

func TestReleasedFixedAndOpen(t *testing.T) {
	b := relFindings()[1]
	got := BuildReleased(relScans(), []model.ImageCveScan{
		scan(stgProj, "pg", "ubi9", "x86_64", b),
		scan(stgProj, "pg", "ubi9", "aarch64", b),
	}, testVersions)
	if len(got) != 1 {
		t.Fatalf("images = %d", len(got))
	}
	im := got[0]
	if im.Total != 3 || im.FixedInStaging != 2 || im.StagingProject != stgProj ||
		im.Version != "18.4-1-1.1" || im.StagingVersion != "18.4-1-2.34" {
		t.Fatalf("image = %+v", im)
	}
	m := statusOf(im)
	if m["CVE-A"].Status != "fixed" || m["CVE-C"].Status != "fixed" {
		t.Errorf("A/C = %+v %+v", m["CVE-A"], m["CVE-C"])
	}
	if m["CVE-B"].Status != "open" || m["CVE-B"].StagingRowKey != "ours|golang" {
		t.Errorf("B = %+v", m["CVE-B"])
	}
	if m["CVE-A"].StagingRowKey != "" {
		t.Errorf("fixed CVE carries a row key: %+v", m["CVE-A"])
	}
}

func TestReleasedOpenOnOneArch(t *testing.T) {
	a := relFindings()[0]
	got := BuildReleased(relScans(), []model.ImageCveScan{
		scan(stgProj, "pg", "ubi9", "x86_64"),
		scan(stgProj, "pg", "ubi9", "aarch64", a),
	}, testVersions)
	m := statusOf(got[0])
	if m["CVE-A"].Status != "open" || m["CVE-A"].StagingRowKey != "base_os|openssl" {
		t.Errorf("A = %+v", m["CVE-A"])
	}
	if m["CVE-C"].Status != "fixed" {
		t.Errorf("C = %+v", m["CVE-C"])
	}
}

func TestReleasedPartialCounterpart(t *testing.T) {
	got := BuildReleased(relScans(), []model.ImageCveScan{
		scan(stgProj, "pg", "ubi9", "x86_64"),
	}, testVersions)
	im := got[0]
	if im.FixedInStaging != 3 {
		t.Fatalf("fixed = %d", im.FixedInStaging)
	}
	for _, c := range im.CVEs {
		if c.Status != "fixed" {
			t.Errorf("%s = %s", c.ID, c.Status)
		}
	}
}

func TestReleasedNoStaging(t *testing.T) {
	got := BuildReleased(relScans(), nil, testVersions)
	im := got[0]
	if im.FixedInStaging != 0 || im.StagingVersion != "" || im.Total != 3 || im.StagingProject != stgProj {
		t.Fatalf("image = %+v", im)
	}
	for _, c := range im.CVEs {
		if c.Status != "no_staging" {
			t.Errorf("%s = %s", c.ID, c.Status)
		}
	}
}

func TestReleasedSkipsCleanAndSorts(t *testing.T) {
	if got := BuildReleased(nil, nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("empty = %#v", got)
	}
	f := relFindings()
	released := []model.ImageCveScan{
		scan("ppg:releases:18:containers", "zz", "ubi9", "x86_64", f[0]),
		scan("ppg:releases:18:containers", "clean", "ubi9", "x86_64"),
		scan("ppg:releases:17:containers", "pg", "ubi9", "x86_64", f...),
		scan("ppg:releases:18:containers", "pg", "ubi9", "x86_64", f[0]),
		scan("ppg:releases:18:containers", "pg", "ubi8", "x86_64", f[0]),
	}
	staging := []model.ImageCveScan{
		scan("ppg:staging:17:containers", "pg", "ubi9", "x86_64", f[1], f[0]),
	}
	got := BuildReleased(released, staging, nil)
	var order []string
	for _, im := range got {
		order = append(order, im.Project+"/"+im.Package+"/"+im.Repo)
	}
	want := []string{
		"ppg:releases:17:containers/pg/ubi9",
		"ppg:releases:18:containers/pg/ubi8",
		"ppg:releases:18:containers/pg/ubi9",
		"ppg:releases:18:containers/zz/ubi9",
	}
	if len(order) != len(want) {
		t.Fatalf("order = %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v", order)
		}
	}
	// open first, then no_staging, then fixed; CRITICAL before HIGH; then id.
	var ids []string
	for _, c := range got[0].CVEs {
		ids = append(ids, c.ID+":"+c.Status)
	}
	wantIDs := []string{"CVE-B:open", "CVE-A:open", "CVE-C:fixed"}
	if len(ids) != 3 || ids[0] != wantIDs[0] || ids[1] != wantIDs[1] || ids[2] != wantIDs[2] {
		t.Fatalf("cves = %v", ids)
	}
}

func TestBuildReport(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.FixedZone("x", 3600))
	b := relFindings()[1]
	r := Build([]model.ImageCveScan{scan(stgProj, "pg", "ubi9", "x86_64", b)}, relScans(), testVersions, now)
	if r.GeneratedAt != "2026-11-17T11:00:00Z" {
		t.Errorf("generated_at = %q", r.GeneratedAt)
	}
	if len(r.Staging.Ours) != 1 || len(r.Released) != 1 {
		t.Errorf("report = %+v", r)
	}
	if _, err := json.Marshal(r); err != nil {
		t.Fatal(err)
	}
}
