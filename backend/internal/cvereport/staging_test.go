package cvereport

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func scan(project, pkg, repo, arch string, fs ...model.CveFinding) model.ImageCveScan {
	return model.ImageCveScan{Project: project, Package: pkg, CveScan: model.CveScan{Repo: repo, Arch: arch, Findings: fs}}
}

func rhel(id, pkg, sev string) model.CveFinding {
	return model.CveFinding{ID: id, PkgName: pkg, InstalledVersion: "1", FixedVersion: "2", Severity: sev, Title: "t",
		Class: "os-pkgs", Vendor: "Red Hat, Inc.", Src: "openssl"}
}

func TestBuildStagingOneRowAcrossImages(t *testing.T) {
	fs := []model.CveFinding{rhel("CVE-1", "openssl-libs", "HIGH"), rhel("CVE-1", "openssl", "HIGH")}
	s := BuildStaging([]model.ImageCveScan{
		scan("ppg:staging:18:containers", "pg", "ubi9", "x86_64", fs...),
		scan("ppg:staging:18:containers", "pg", "ubi9", "aarch64", fs...),
		scan("ppg:staging:17:containers", "pg", "ubi9", "x86_64", fs...),
	})
	if len(s.BaseOS) != 1 {
		t.Fatalf("base os rows = %d", len(s.BaseOS))
	}
	r := s.BaseOS[0]
	if r.Key != "base_os|openssl" || r.CVEs != 1 || len(r.Entries) != 2 || len(r.Fixes) != 2 || len(r.Images) != 2 {
		t.Fatalf("row = %+v", r)
	}
	if r.Entries[0].SeenKey != "CVE-1|openssl|" || r.Entries[1].SeenKey != "CVE-1|openssl-libs|" {
		t.Errorf("seen keys = %q %q", r.Entries[0].SeenKey, r.Entries[1].SeenKey)
	}
	var found bool
	for _, im := range r.Images {
		if im.Project == "ppg:staging:18:containers" {
			found = true
			if !reflect.DeepEqual(im.Arches, []string{"aarch64", "x86_64"}) {
				t.Errorf("arches = %v", im.Arches)
			}
		}
	}
	if !found {
		t.Error("18 image missing")
	}
	if r.Images[0].Project != "ppg:staging:17:containers" {
		t.Errorf("images not sorted: %+v", r.Images)
	}
	if s.CVEs != 1 || s.Images != 2 {
		t.Errorf("CVEs=%d Images=%d", s.CVEs, s.Images)
	}
}

func TestBuildStagingGolangRow(t *testing.T) {
	g := func(path string) model.CveFinding {
		return model.CveFinding{ID: "CVE-9", PkgName: "stdlib", InstalledVersion: "1.21", FixedVersion: "1.22", Severity: "HIGH",
			Class: "lang-pkgs", Type: "gobinary", Path: path}
	}
	s := BuildStaging([]model.ImageCveScan{scan("ppg:staging:18:containers", "pg", "ubi9", "x86_64", g("usr/bin/etcd"), g("usr/sbin/gosu"))})
	if len(s.Ours) != 1 {
		t.Fatalf("ours = %d", len(s.Ours))
	}
	r := s.Ours[0]
	if r.Key != "ours|golang" || r.Kind != KindGoRuntime || len(r.Entries) != 2 || r.Entries[0].Path == r.Entries[1].Path {
		t.Errorf("row = %+v", r)
	}
}

func TestBuildStagingOrdering(t *testing.T) {
	mk := func(name, sev string) model.CveFinding {
		return model.CveFinding{ID: "CVE-" + name, PkgName: name, Severity: sev, Class: "os-pkgs", Vendor: "Percona, LLC", Src: name}
	}
	var scans []model.ImageCveScan
	for _, p := range []string{"p1", "p2", "p3"} {
		scans = append(scans, scan("ppg:staging:"+p, "pg", "ubi9", "x86_64", mk("a", "HIGH"), mk("c", "HIGH")))
	}
	scans = append(scans, scan("ppg:staging:p1", "pg", "ubi9", "x86_64", mk("b", "CRITICAL")))
	s := BuildStaging(scans)
	var names []string
	for _, r := range s.Ours {
		names = append(names, r.Name)
	}
	if !reflect.DeepEqual(names, []string{"b", "a", "c"}) {
		t.Errorf("order = %v", names)
	}
	if s.Ours[0].Worst != "CRITICAL" {
		t.Errorf("worst = %q", s.Ours[0].Worst)
	}
}

func TestBuildStagingUnclassifiedVendor(t *testing.T) {
	f := model.CveFinding{ID: "CVE-5", PkgName: "foo", Severity: "HIGH", Class: "os-pkgs", Vendor: "Acme", Src: "foo"}
	s := BuildStaging([]model.ImageCveScan{
		scan("ppg:staging:18", "pg", "ubi9", "x86_64", f),
		scan("ppg:staging:18", "clean", "ubi9", "x86_64"),
	})
	if len(s.Unclassified) != 1 || s.Unclassified[0].Vendor != "Acme" || s.Images != 1 {
		t.Fatalf("got %+v", s)
	}

	e := BuildStaging(nil)
	b, _ := json.Marshal(e)
	want := `{"ours":[],"base_os":[],"unclassified":[],"cves":0,"images":0}`
	if string(b) != want {
		t.Errorf("empty json = %s", b)
	}
}
