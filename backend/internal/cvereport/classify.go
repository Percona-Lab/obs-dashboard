// Package cvereport turns stored CVE scans into the rows shown in the CVEs tab.
package cvereport

import (
	"strings"

	"github.com/percona/obs-dashboard/internal/model"
)

// Lists a row can belong to.
const (
	ListOurs         = "ours"
	ListBaseOS       = "base_os"
	ListUnclassified = "unclassified"
)

// Kinds of row.
const (
	KindGoRuntime    = "go_runtime"
	KindBundled      = "bundled_library"
	KindOBSPackage   = "obs_package"
	KindBaseOS       = "base_os"
	KindUnclassified = "unclassified"
)

const (
	classLangPkgs = "lang-pkgs"
	typeGoBinary  = "gobinary"
	goStdlib      = "stdlib"
	goRuntimePkg  = "golang"
)

// Classify places one finding. name is the package to bump (or the file
// path / package name when no owner is known). Vendor rule: Percona or obs://
// is ours; Red Hat, Inc. or Fedora Project is base OS; anything else is
// unclassified.
func Classify(f model.CveFinding) (list, name, kind string) {
	if f.Class == classLangPkgs {
		return classifyLang(f)
	}
	name = f.Src
	if name == "" {
		name = f.PkgName
	}
	if f.Class != "os-pkgs" {
		return ListUnclassified, name, KindUnclassified
	}
	switch {
	case strings.HasPrefix(f.Vendor, "Percona") || strings.HasPrefix(f.Vendor, "obs://"):
		return ListOurs, name, KindOBSPackage
	case f.Vendor == "Red Hat, Inc." || f.Vendor == "Fedora Project":
		return ListBaseOS, name, KindBaseOS
	}
	return ListUnclassified, name, KindUnclassified
}

func classifyLang(f model.CveFinding) (string, string, string) {
	if f.Type == typeGoBinary && f.PkgName == goStdlib {
		return ListOurs, goRuntimePkg, KindGoRuntime
	}
	switch {
	case f.Owner != "":
		return ListOurs, f.Owner, KindBundled
	case f.Type == typeGoBinary:
		return ListOurs, f.Path, KindBundled
	}
	return ListOurs, f.PkgName + " (" + f.Path + ")", KindBundled
}

// RowKey is "<list>|<name>".
func RowKey(list, name string) string { return list + "|" + name }
