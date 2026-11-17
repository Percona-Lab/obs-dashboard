package cvereport

import (
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name               string
		f                  model.CveFinding
		wantList, wantName string
		wantKind           string
	}{
		{"percona vendor", model.CveFinding{Class: "os-pkgs", Vendor: "Percona, LLC", Src: "percona-pgbouncer"}, ListOurs, "percona-pgbouncer", KindOBSPackage},
		{"obs vendor", model.CveFinding{Class: "os-pkgs", Vendor: "obs://obs.pg.labs.percona.com/isv:percona:ppg:common:deps", Src: "curl"}, ListOurs, "curl", KindOBSPackage},
		{"red hat", model.CveFinding{Class: "os-pkgs", Vendor: "Red Hat, Inc.", Src: "openssl", PkgName: "openssl-libs"}, ListBaseOS, "openssl", KindBaseOS},
		{"fedora", model.CveFinding{Class: "os-pkgs", Vendor: "Fedora Project", Src: "x"}, ListBaseOS, "x", KindBaseOS},
		{"unknown vendor", model.CveFinding{Class: "os-pkgs", Vendor: "Acme", Src: "foo"}, ListUnclassified, "foo", KindUnclassified},
		{"no vendor no src", model.CveFinding{Class: "os-pkgs", PkgName: "ghost"}, ListUnclassified, "ghost", KindUnclassified},
		{"old scan", model.CveFinding{PkgName: "openssl"}, ListUnclassified, "openssl", KindUnclassified},
		{"known vendor empty src", model.CveFinding{Class: "os-pkgs", Vendor: "Red Hat, Inc.", PkgName: "glibc"}, ListBaseOS, "glibc", KindBaseOS},
		{"go stdlib", model.CveFinding{Class: "lang-pkgs", Type: "gobinary", PkgName: "stdlib", Path: "usr/bin/etcd", Owner: "etcd"}, ListOurs, "golang", KindGoRuntime},
		{"go library owned", model.CveFinding{Class: "lang-pkgs", Type: "gobinary", PkgName: "golang.org/x/net", Path: "usr/bin/etcd", Owner: "etcd"}, ListOurs, "etcd", KindBundled},
		{"go library unowned", model.CveFinding{Class: "lang-pkgs", Type: "gobinary", PkgName: "golang.org/x/net", Path: "usr/local/bin/mystery"}, ListOurs, "usr/local/bin/mystery", KindBundled},
		{"python owned", model.CveFinding{Class: "lang-pkgs", Type: "python-pkg", PkgName: "dnspython", Path: "usr/lib/python3.12/site-packages/dnspython-2.0.dist-info/METADATA", Owner: "python3.12-dns"}, ListOurs, "python3.12-dns", KindBundled},
		{"python unowned", model.CveFinding{Class: "lang-pkgs", Type: "python-pkg", PkgName: "requests", Path: "opt/app/requests-2.0.0.dist-info/METADATA"}, ListOurs, "requests (opt/app/requests-2.0.0.dist-info/METADATA)", KindBundled},
		{"python unowned no path", model.CveFinding{Class: "lang-pkgs", Type: "python-pkg", PkgName: "requests"}, ListOurs, "requests", KindBundled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, name, kind := Classify(tt.f)
			if list != tt.wantList || name != tt.wantName || kind != tt.wantKind {
				t.Errorf("got (%q,%q,%q), want (%q,%q,%q)", list, name, kind, tt.wantList, tt.wantName, tt.wantKind)
			}
		})
	}
}

func TestRowKey(t *testing.T) {
	if got := RowKey(ListOurs, "etcd"); got != "ours|etcd" {
		t.Errorf("got %q", got)
	}
}

func TestOursVendor(t *testing.T) {
	for v, want := range map[string]bool{"Percona, LLC": true, "obs://x/y": true, "Red Hat, Inc.": false, "": false} {
		if got := OursVendor(v); got != want {
			t.Errorf("OursVendor(%q) = %v", v, got)
		}
	}
}
