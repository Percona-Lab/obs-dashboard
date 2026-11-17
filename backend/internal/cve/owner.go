package cve

import (
	"path"
	"regexp"
	"strings"

	"github.com/percona/obs-dashboard/internal/cvereport"
)

// rpmInfo is one RPM from trivy's package list.
type rpmInfo struct{ Name, Version, Src, Vendor string }

var pythonPrefix = regexp.MustCompile(`python(\d+(\.\d+)?)?`)

// normLangName reduces a Python distribution or RPM name to a comparable stem:
// lowercase, without a "percona-" prefix, "pythonX.Y" markers or separators.
func normLangName(s string) string {
	s = strings.TrimPrefix(strings.ToLower(s), "percona-")
	s = pythonPrefix.ReplaceAllString(s, "")
	return strings.NewReplacer("-", "", "_", "", ".", "").Replace(s)
}

// soleSource returns the one distinct source package among srcs, or "" when
// there are none or several.
func soleSource(srcs map[string]bool) string {
	if len(srcs) != 1 {
		return ""
	}
	for s := range srcs {
		return s
	}
	return ""
}

// goBinaryOwner returns the source package of the team's RPM that ships the
// Go binary at p, when exactly one distinct source package matches.
func goBinaryOwner(p string, rpms []rpmInfo) string {
	base := path.Base(p)
	srcs := map[string]bool{}
	for _, r := range rpms {
		if cvereport.OursVendor(r.Vendor) && (r.Name == base || r.Name == "percona-"+base) {
			srcs[r.Src] = true
		}
	}
	return soleSource(srcs)
}

// pythonOwner returns the source package of the team's RPM that provides the
// Python package name at version, when exactly one distinct source package matches.
func pythonOwner(name, version string, rpms []rpmInfo) string {
	want := normLangName(name)
	srcs := map[string]bool{}
	for _, r := range rpms {
		if cvereport.OursVendor(r.Vendor) && r.Version == version && normLangName(r.Name) == want {
			srcs[r.Src] = true
		}
	}
	return soleSource(srcs)
}
