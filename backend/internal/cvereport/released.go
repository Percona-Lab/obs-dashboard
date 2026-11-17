package cvereport

import (
	"sort"
	"strings"

	"github.com/percona/obs-dashboard/internal/model"
)

// Released status of one CVE against the current staging image.
const (
	StatusFixed     = "fixed"
	StatusOpen      = "open"
	StatusNoStaging = "no_staging"
)

const (
	releasesPrefix = "ppg:releases"
	stagingPrefix  = "ppg:staging"
)

type ReleasedCVE struct {
	ID            string `json:"id"`
	Pkg           string `json:"pkg"`
	Installed     string `json:"installed"`
	Fixed         string `json:"fixed"`
	Severity      string `json:"severity"`
	Title         string `json:"title"`
	Status        string `json:"status"`                    // "fixed" | "open" | "no_staging"
	StagingRowKey string `json:"staging_row_key,omitempty"` // when Status == "open"
}

type ReleasedImage struct {
	Project        string        `json:"project"`
	Package        string        `json:"package"`
	Repo           string        `json:"repo"`
	Version        string        `json:"version"`
	StagingProject string        `json:"staging_project"`
	StagingVersion string        `json:"staging_version,omitempty"`
	Total          int           `json:"total"`
	FixedInStaging int           `json:"fixed_in_staging"`
	CVEs           []ReleasedCVE `json:"cves"`
}

type cveKey struct{ id, pkg string }

// stagingProjectOf maps a released project to its staging counterpart.
func stagingProjectOf(project string) string {
	if strings.HasPrefix(project, releasesPrefix) {
		return stagingPrefix + strings.TrimPrefix(project, releasesPrefix)
	}
	return project
}

// BuildReleased pairs released scans with staging scans. versions maps
// "<project>/<package>" to the package version.
func BuildReleased(released, staging []model.ImageCveScan, versions map[string]string) []ReleasedImage {
	// staging findings by image, arch, then (CVE id, pkg).
	stg := map[imageKey]map[string]map[cveKey]model.CveFinding{}
	for _, sc := range staging {
		ik := imageKey{sc.Project, sc.Package, sc.Repo}
		if stg[ik] == nil {
			stg[ik] = map[string]map[cveKey]model.CveFinding{}
		}
		m := stg[ik][sc.Arch]
		if m == nil {
			m = map[cveKey]model.CveFinding{}
			stg[ik][sc.Arch] = m
		}
		for _, f := range sc.Findings {
			k := cveKey{f.ID, f.PkgName}
			if _, ok := m[k]; !ok {
				m[k] = f
			}
		}
	}

	// released findings by image, arch.
	rel := map[imageKey]map[string][]model.CveFinding{}
	for _, sc := range released {
		if len(sc.Findings) == 0 {
			continue
		}
		ik := imageKey{sc.Project, sc.Package, sc.Repo}
		if rel[ik] == nil {
			rel[ik] = map[string][]model.CveFinding{}
		}
		rel[ik][sc.Arch] = append(rel[ik][sc.Arch], sc.Findings...)
	}

	out := make([]ReleasedImage, 0, len(rel))
	for ik, byArch := range rel {
		arches := make([]string, 0, len(byArch))
		for ar := range byArch {
			arches = append(arches, ar)
		}
		sort.Strings(arches)

		stgIk := imageKey{stagingProjectOf(ik.project), ik.pkg, ik.repo}
		counterparts := stg[stgIk]
		im := ReleasedImage{
			Project:        ik.project,
			Package:        ik.pkg,
			Repo:           ik.repo,
			Version:        versions[ik.project+"/"+ik.pkg],
			StagingProject: stgIk.project,
			CVEs:           []ReleasedCVE{},
		}

		cves := map[cveKey]*ReleasedCVE{}
		var order []cveKey
		for _, ar := range arches {
			for _, f := range byArch[ar] {
				k := cveKey{f.ID, f.PkgName}
				if cves[k] != nil {
					continue
				}
				cves[k] = &ReleasedCVE{ID: f.ID, Pkg: f.PkgName, Installed: f.InstalledVersion,
					Fixed: f.FixedVersion, Severity: f.Severity, Title: f.Title}
				order = append(order, k)
			}
		}

		haveCounterpart := false
		for _, ar := range arches {
			if _, ok := counterparts[ar]; ok {
				haveCounterpart = true
				break
			}
		}
		if haveCounterpart {
			im.StagingVersion = versions[stgIk.project+"/"+stgIk.pkg]
		}

		for _, k := range order {
			c := cves[k]
			switch {
			case !haveCounterpart:
				c.Status = StatusNoStaging
			default:
				c.Status = StatusFixed
				for _, ar := range arches {
					if f, ok := counterparts[ar][k]; ok {
						list, name, _ := Classify(f)
						c.Status = StatusOpen
						c.StagingRowKey = RowKey(list, name)
						break
					}
				}
			}
			if c.Status == StatusFixed {
				im.FixedInStaging++
			}
			im.CVEs = append(im.CVEs, *c)
		}
		im.Total = len(im.CVEs)
		sortReleasedCVEs(im.CVEs)
		out = append(out, im)
	}

	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.Project != y.Project {
			return x.Project < y.Project
		}
		if x.Package != y.Package {
			return x.Package < y.Package
		}
		return x.Repo < y.Repo
	})
	return out
}

func statusRank(s string) int {
	switch s {
	case StatusOpen:
		return 0
	case StatusNoStaging:
		return 1
	}
	return 2
}

func severityRank(s string) int {
	if s == "CRITICAL" {
		return 0
	}
	return 1
}

// sortReleasedCVEs orders open, no_staging, fixed; then CRITICAL before HIGH;
// then by id and package.
func sortReleasedCVEs(cs []ReleasedCVE) {
	sort.Slice(cs, func(i, j int) bool {
		x, y := cs[i], cs[j]
		if a, b := statusRank(x.Status), statusRank(y.Status); a != b {
			return a < b
		}
		if a, b := severityRank(x.Severity), severityRank(y.Severity); a != b {
			return a < b
		}
		if x.ID != y.ID {
			return x.ID < y.ID
		}
		return x.Pkg < y.Pkg
	})
}
