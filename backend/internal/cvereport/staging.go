package cvereport

import (
	"sort"

	"github.com/percona/obs-dashboard/internal/model"
)

type Fix struct {
	Pkg       string `json:"pkg"`
	Installed string `json:"installed"`
	Fixed     string `json:"fixed"`
}

type Entry struct {
	ID       string `json:"id"`
	Pkg      string `json:"pkg"`
	Path     string `json:"path,omitempty"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	SeenKey  string `json:"seen_key"` // "<id>|<pkg>|<path>"
}

type ImageRef struct {
	Project string   `json:"project"`
	Package string   `json:"package"`
	Repo    string   `json:"repo"`
	Arches  []string `json:"arches"`
}

type Row struct {
	Key     string     `json:"key"`
	Name    string     `json:"name"`
	Kind    string     `json:"kind"`
	Vendor  string     `json:"vendor,omitempty"` // unclassified RPMs only
	Worst   string     `json:"worst"`            // "CRITICAL" | "HIGH"
	CVEs    int        `json:"cves"`             // distinct CVE ids
	Fixes   []Fix      `json:"fixes"`
	Entries []Entry    `json:"entries"`
	Images  []ImageRef `json:"images"`
}

type Staging struct {
	Ours         []Row `json:"ours"`
	BaseOS       []Row `json:"base_os"`
	Unclassified []Row `json:"unclassified"`
	CVEs         int   `json:"cves"`   // distinct CVE ids across all rows
	Images       int   `json:"images"` // distinct images with at least one finding
}

type imageKey struct{ project, pkg, repo string }

// rowAcc accumulates one row while scans are walked.
type rowAcc struct {
	list    string
	row     Row
	cves    map[string]bool
	fixes   map[Fix]bool
	entries map[string]Entry
	arches  map[imageKey]map[string]bool
}

// BuildStaging aggregates staging scans into one row per package to bump,
// across images and arches.
func BuildStaging(scans []model.ImageCveScan) Staging {
	accs := map[string]*rowAcc{}
	allCVEs := map[string]bool{}
	allImages := map[imageKey]bool{}

	for _, sc := range scans {
		ik := imageKey{sc.Project, sc.Package, sc.Repo}
		for _, f := range sc.Findings {
			list, name, kind := Classify(f)
			key := RowKey(list, name)
			a := accs[key]
			if a == nil {
				a = &rowAcc{
					list:    list,
					row:     Row{Key: key, Name: name, Kind: kind, Worst: "HIGH"},
					cves:    map[string]bool{},
					fixes:   map[Fix]bool{},
					entries: map[string]Entry{},
					arches:  map[imageKey]map[string]bool{},
				}
				accs[key] = a
			}
			if f.Severity == "CRITICAL" {
				a.row.Worst = "CRITICAL"
			}
			if list == ListUnclassified && a.row.Vendor == "" && f.Class == "os-pkgs" {
				a.row.Vendor = f.Vendor
			}
			a.cves[f.ID] = true
			a.fixes[Fix{f.PkgName, f.InstalledVersion, f.FixedVersion}] = true
			sk := f.ID + "|" + f.PkgName + "|" + f.Path
			if _, ok := a.entries[sk]; !ok {
				a.entries[sk] = Entry{ID: f.ID, Pkg: f.PkgName, Path: f.Path, Severity: f.Severity, Title: f.Title, SeenKey: sk}
			}
			if a.arches[ik] == nil {
				a.arches[ik] = map[string]bool{}
			}
			a.arches[ik][sc.Arch] = true
			allCVEs[f.ID] = true
			allImages[ik] = true
		}
	}

	out := Staging{Ours: []Row{}, BaseOS: []Row{}, Unclassified: []Row{}, CVEs: len(allCVEs), Images: len(allImages)}
	for _, a := range accs {
		r := a.finish()
		switch a.list {
		case ListOurs:
			out.Ours = append(out.Ours, r)
		case ListBaseOS:
			out.BaseOS = append(out.BaseOS, r)
		default:
			out.Unclassified = append(out.Unclassified, r)
		}
	}
	sortRows(out.Ours)
	sortRows(out.BaseOS)
	sortRows(out.Unclassified)
	return out
}

func (a *rowAcc) finish() Row {
	r := a.row
	r.CVEs = len(a.cves)
	r.Fixes = make([]Fix, 0, len(a.fixes))
	for f := range a.fixes {
		r.Fixes = append(r.Fixes, f)
	}
	sort.Slice(r.Fixes, func(i, j int) bool {
		x, y := r.Fixes[i], r.Fixes[j]
		if x.Pkg != y.Pkg {
			return x.Pkg < y.Pkg
		}
		if x.Installed != y.Installed {
			return x.Installed < y.Installed
		}
		return x.Fixed < y.Fixed
	})
	r.Entries = make([]Entry, 0, len(a.entries))
	for _, e := range a.entries {
		r.Entries = append(r.Entries, e)
	}
	sort.Slice(r.Entries, func(i, j int) bool {
		x, y := r.Entries[i], r.Entries[j]
		if x.ID != y.ID {
			return x.ID < y.ID
		}
		if x.Pkg != y.Pkg {
			return x.Pkg < y.Pkg
		}
		return x.Path < y.Path
	})
	r.Images = make([]ImageRef, 0, len(a.arches))
	for ik, set := range a.arches {
		arches := make([]string, 0, len(set))
		for ar := range set {
			arches = append(arches, ar)
		}
		sort.Strings(arches)
		r.Images = append(r.Images, ImageRef{Project: ik.project, Package: ik.pkg, Repo: ik.repo, Arches: arches})
	}
	sort.Slice(r.Images, func(i, j int) bool {
		x, y := r.Images[i], r.Images[j]
		if x.Project != y.Project {
			return x.Project < y.Project
		}
		if x.Package != y.Package {
			return x.Package < y.Package
		}
		return x.Repo < y.Repo
	})
	return r
}

// sortRows orders CRITICAL before HIGH, then more images first, then by name.
func sortRows(rows []Row) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if (a.Worst == "CRITICAL") != (b.Worst == "CRITICAL") {
			return a.Worst == "CRITICAL"
		}
		if len(a.Images) != len(b.Images) {
			return len(a.Images) > len(b.Images)
		}
		return a.Name < b.Name
	})
}
