package cve

import "testing"

func TestNormLangName(t *testing.T) {
	for in, want := range map[string]string{
		"dnspython":           "dns",
		"python3.12-dns":      "dns",
		"python-dateutil":     "dateutil",
		"python3.12-dateutil": "dateutil",
		"systemd-python":      "systemd",
		"python3-ydiff":       "ydiff",
		"psycopg2":            "psycopg2",
		"python3.12-psycopg2": "psycopg2",
		"percona-patroni":     "patroni",
	} {
		if got := normLangName(in); got != want {
			t.Errorf("normLangName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveOwnerAmbiguous(t *testing.T) {
	rpms := []rpmInfo{
		{Name: "gosu", Version: "1", Src: "a", Vendor: "Percona, LLC"},
		{Name: "percona-gosu", Version: "1", Src: "b", Vendor: "Percona, LLC"},
		{Name: "python3-dns", Version: "2.9.0", Src: "a", Vendor: "Percona, LLC"},
		{Name: "python3.12-dns", Version: "2.9.0", Src: "b", Vendor: "Percona, LLC"},
	}
	if got := goBinaryOwner("usr/sbin/gosu", rpms); got != "" {
		t.Errorf("goBinaryOwner = %q, want empty", got)
	}
	if got := pythonOwner("dnspython", "2.9.0", rpms); got != "" {
		t.Errorf("pythonOwner = %q, want empty", got)
	}
}

func TestResolveOwnerIgnoresBaseOS(t *testing.T) {
	rpms := []rpmInfo{{Name: "gosu", Version: "1", Src: "gosu", Vendor: "Red Hat, Inc."}}
	if got := goBinaryOwner("usr/sbin/gosu", rpms); got != "" {
		t.Errorf("goBinaryOwner = %q, want empty", got)
	}
}
