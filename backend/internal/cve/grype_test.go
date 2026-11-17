package cve

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestParseGrypeOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/grype.json")
	if err != nil {
		t.Fatal(err)
	}
	rpms := []rpmInfo{
		{Name: "krb5-libs", Version: "1.21.1", Src: "krb5", Vendor: "Red Hat, Inc."},
		{Name: "gosu", Version: "1.17", Src: "gosu", Vendor: "obs://obs.pg.labs.percona.com/isv:percona:ppg:common:deps"},
	}
	got, err := parseGrypeOutput(data, rpms)
	if err != nil {
		t.Fatal(err)
	}
	g := []string{"grype"}
	want := []model.CveFinding{
		{ID: "CVE-2026-0001", PkgName: "krb5-libs", InstalledVersion: "1.21.1-10.el9_8.1", FixedVersion: "1.21.1-10.el9_9.2", Severity: "HIGH", Class: "os-pkgs", Type: "redhat", Src: "krb5", Vendor: "Red Hat, Inc.", Scanners: g},
		{ID: "CVE-2026-0100", PkgName: "krb5-libs", InstalledVersion: "1.21.1-10.el9_8.1", FixedVersion: "1.21.1-10.el9_9.3", Severity: "HIGH", Class: "os-pkgs", Type: "redhat", Src: "krb5", Vendor: "Red Hat, Inc.", Scanners: g},
		{ID: "CVE-2026-0101", PkgName: "newpkg", InstalledVersion: "2:3.0-1.el10", FixedVersion: "2:3.0-2.el10, 2:3.1-1.el10", Severity: "CRITICAL", Class: "os-pkgs", Type: "redhat", Src: "newsrc", Scanners: g},
		{ID: "CVE-2026-0005", PkgName: "golang.org/x/net", InstalledVersion: "v0.54.0", FixedVersion: "0.55.0", Severity: "HIGH", Class: "lang-pkgs", Type: "gobinary", Path: "usr/sbin/gosu", Owner: "gosu", Scanners: g},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseGrypeOutputInvalidJSON(t *testing.T) {
	_, err := parseGrypeOutput([]byte("not json"), nil)
	if err == nil || !strings.HasPrefix(err.Error(), "parse grype JSON") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseGrypeOutputTypeMapping(t *testing.T) {
	data := []byte(`{"matches":[
{"vulnerability":{"id":"CVE-2026-0200","severity":"High","fix":{"versions":["2.0"]}},"artifact":{"name":"requests","version":"1.0","type":"python","locations":[{"path":"/opt/app/requests-1.0.dist-info/METADATA"}]}},
{"vulnerability":{"id":"CVE-2026-0201","severity":"Critical","fix":{"versions":["4.17.21"]}},"artifact":{"name":"lodash","version":"4.0.0","type":"npm","locations":[{"path":"/app/node_modules/lodash/package.json"}]}}
]}`)
	got, err := parseGrypeOutput(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := []string{"grype"}
	want := []model.CveFinding{
		{ID: "CVE-2026-0200", PkgName: "requests", InstalledVersion: "1.0", FixedVersion: "2.0", Severity: "HIGH", Class: "lang-pkgs", Type: "python-pkg", Path: "opt/app/requests-1.0.dist-info/METADATA", Scanners: g},
		{ID: "CVE-2026-0201", PkgName: "lodash", InstalledVersion: "4.0.0", FixedVersion: "4.17.21", Severity: "CRITICAL", Class: "lang-pkgs", Type: "npm", Path: "app/node_modules/lodash/package.json", Scanners: g},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestMergeFindings(t *testing.T) {
	trivy := []model.CveFinding{
		{ID: "CVE-1", PkgName: "a", Title: "t", Scanners: []string{"trivy"}},
		{ID: "CVE-2", PkgName: "x/net", Path: "usr/bin/one", Scanners: []string{"trivy"}},
	}
	grype := []model.CveFinding{
		{ID: "CVE-1", PkgName: "a", Scanners: []string{"grype"}},
		{ID: "CVE-2", PkgName: "x/net", Path: "usr/bin/two", Scanners: []string{"grype"}},
		{ID: "CVE-1", PkgName: "b", Scanners: []string{"grype"}},
	}
	got := mergeFindings(trivy, grype)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	if got[0].ID != "CVE-1" || got[0].PkgName != "a" || got[0].Title != "t" || !reflect.DeepEqual(got[0].Scanners, []string{"trivy", "grype"}) {
		t.Errorf("got[0] = %+v", got[0])
	}
	if !reflect.DeepEqual(got[1].Scanners, []string{"trivy"}) {
		t.Errorf("got[1] = %+v", got[1])
	}
	for _, f := range got[2:] {
		if !reflect.DeepEqual(f.Scanners, []string{"grype"}) {
			t.Errorf("grype-only finding = %+v", f)
		}
	}
	if got[2].Path != "usr/bin/two" || got[3].PkgName != "b" {
		t.Errorf("order: %+v %+v", got[2], got[3])
	}
}
