package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/percona/obs-dashboard/internal/model"
)

// UnitDef is one entry of the release units file.
type UnitDef struct {
	Unit           string   `json:"unit"`
	Members        []string `json:"members"`
	ReleaseProject string   `json:"release_project"`
	ReleaseDir     string   `json:"release_dir"`
}

// ParseUnits parses and validates the release units file. An empty list is an
// error.
func ParseUnits(b []byte) ([]UnitDef, error) {
	var defs []UnitDef
	if err := json.Unmarshal(b, &defs); err != nil {
		return nil, fmt.Errorf("release units: %w", err)
	}
	if len(defs) == 0 {
		return nil, errors.New("release units: no units defined")
	}
	seen := map[string]bool{}
	for i, d := range defs {
		if d.Unit == "" || len(d.Members) == 0 {
			return nil, fmt.Errorf("release units: entry %d needs a unit name and members", i)
		}
		for _, m := range d.Members {
			if m == "" {
				return nil, fmt.Errorf("release units: unit %q has an empty member", d.Unit)
			}
		}
		if seen[d.Unit] {
			return nil, fmt.Errorf("release units: duplicate unit %q", d.Unit)
		}
		seen[d.Unit] = true
	}
	return defs, nil
}

// Blocked explains why a unit cannot be chosen.
type Blocked struct {
	Reason    string   `json:"reason"` // in_flight | not_built | unchanged
	Text      string   `json:"text"`
	Packages  []string `json:"packages,omitempty"`
	ReleaseID string   `json:"release_id,omitempty"`
}

// UnitRow is a unit with its build progress and eligibility.
type UnitRow struct {
	UnitDef
	LastRelease string   `json:"last_release"`
	Built       int      `json:"built"`
	Total       int      `json:"total"`
	Tickable    bool     `json:"tickable"`
	Blocked     *Blocked `json:"blocked,omitempty"`
}

// PackageSource lists packages of a project and its subprojects.
type PackageSource interface {
	Packages(projectPrefix string) ([]*model.Package, error)
}

const maxNotBuiltNames = 10

// BuildUnitRows computes the selection list. inFlight maps unit to the id of
// its running release; lastRelease maps unit to its last release id.
func BuildUnitRows(defs []UnitDef, src PackageSource, inFlight map[string]string, lastRelease map[string]string) ([]UnitRow, error) {
	rows := make([]UnitRow, 0, len(defs))
	for _, d := range defs {
		row := UnitRow{UnitDef: d, LastRelease: lastRelease[d.Unit]}
		var staging, release []*model.Package
		for _, m := range d.Members {
			pk, err := exactProject(src, m)
			if err != nil {
				return nil, err
			}
			staging = append(staging, pk...)
			rp, err := exactProject(src, releaseOf(m))
			if err != nil {
				return nil, err
			}
			release = append(release, rp...)
		}
		var notBuilt []string
		for _, p := range staging {
			row.Total++
			if p.RollupState == model.RollupSucceeded || p.RollupState == model.RollupPublished {
				row.Built++
			} else {
				notBuilt = append(notBuilt, p.Name)
			}
		}
		switch {
		case inFlight[d.Unit] != "":
			id := inFlight[d.Unit]
			row.Blocked = &Blocked{Reason: "in_flight", ReleaseID: id, Text: "Release " + id + " is already in progress."}
		case row.Total == 0:
			row.Blocked = &Blocked{Reason: "not_built", Text: "No packages found in staging."}
		case len(notBuilt) > 0:
			sort.Strings(notBuilt)
			names := notBuilt
			if len(names) > maxNotBuiltNames {
				names = names[:maxNotBuiltNames]
			}
			row.Blocked = &Blocked{Reason: "not_built", Packages: names,
				Text: fmt.Sprintf("Not fully built: %d package(s) not built.", len(notBuilt))}
		case len(release) > 0 && sameContent(staging, release):
			last := "the last release"
			if row.LastRelease != "" {
				last = "release " + row.LastRelease
			}
			row.Blocked = &Blocked{Reason: "unchanged", ReleaseID: row.LastRelease,
				Text: "Staging is identical to " + last + ". Nothing changed since then."}
		}
		row.Tickable = row.Blocked == nil
		rows = append(rows, row)
	}
	return rows, nil
}

// releaseOf maps a staging project to its release project.
func releaseOf(project string) string {
	parts := strings.Split(project, ":")
	for i, p := range parts {
		if p == "staging" {
			parts[i] = "releases"
			break
		}
	}
	return strings.Join(parts, ":")
}

func exactProject(src PackageSource, project string) ([]*model.Package, error) {
	all, err := src.Packages(project)
	if err != nil {
		return nil, err
	}
	var out []*model.Package
	for _, p := range all {
		if p.Project == project {
			out = append(out, p)
		}
	}
	return out, nil
}

func contentKeys(pk []*model.Package) []string {
	keys := make([]string, 0, len(pk))
	for _, p := range pk {
		tags := append([]string(nil), p.ContainerTags...)
		sort.Strings(tags)
		keys = append(keys, p.Name+"\x00"+p.Version+"\x00"+strings.Join(tags, "\x01"))
	}
	sort.Strings(keys)
	return keys
}

func sameContent(a, b []*model.Package) bool {
	ka, kb := contentKeys(a), contentKeys(b)
	if len(ka) != len(kb) {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}
