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
