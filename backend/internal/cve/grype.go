package cve

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/percona/obs-dashboard/internal/model"
)

type grypeOutput struct {
	Matches []grypeMatch `json:"matches"`
}

type grypeMatch struct {
	Vulnerability struct {
		ID       string `json:"id"`
		Severity string `json:"severity"`
		Fix      struct {
			Versions []string `json:"versions"`
		} `json:"fix"`
	} `json:"vulnerability"`
	RelatedVulnerabilities []struct {
		ID string `json:"id"`
	} `json:"relatedVulnerabilities"`
	Artifact struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		Type      string `json:"type"`
		Locations []struct {
			Path string `json:"path"`
		} `json:"locations"`
		Upstreams []struct {
			Name string `json:"name"`
		} `json:"upstreams"`
	} `json:"artifact"`
}

// parseGrypeOutput converts `grype -o json` output into findings. rpms is the
// RPM list from the Trivy scan of the same image, used for Src/Vendor/Owner.
func parseGrypeOutput(data []byte, rpms []rpmInfo) ([]model.CveFinding, error) {
	var out grypeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse grype JSON: %w", err)
	}
	type key struct{ id, pkg, path string }
	seen := map[key]bool{}
	var findings []model.CveFinding
	for _, m := range out.Matches {
		sev := strings.ToUpper(m.Vulnerability.Severity)
		if (sev != "HIGH" && sev != "CRITICAL") || len(m.Vulnerability.Fix.Versions) == 0 {
			continue
		}
		id := m.Vulnerability.ID
		if !strings.HasPrefix(id, "CVE-") {
			for _, r := range m.RelatedVulnerabilities {
				if strings.HasPrefix(r.ID, "CVE-") {
					id = r.ID
					break
				}
			}
		}
		fixed := make([]string, len(m.Vulnerability.Fix.Versions))
		for i, v := range m.Vulnerability.Fix.Versions {
			fixed[i] = strings.TrimPrefix(v, "0:")
		}
		a := m.Artifact
		f := model.CveFinding{
			ID:               id,
			PkgName:          a.Name,
			InstalledVersion: a.Version,
			FixedVersion:     strings.Join(fixed, ", "),
			Severity:         sev,
			Scanners:         []string{"grype"},
		}
		switch a.Type {
		case "rpm":
			f.Class, f.Type = "os-pkgs", "redhat"
		case "go-module":
			f.Class, f.Type = "lang-pkgs", "gobinary"
		case "python":
			f.Class, f.Type = "lang-pkgs", "python-pkg"
		default:
			f.Class, f.Type = "lang-pkgs", a.Type
		}
		if a.Type != "rpm" && len(a.Locations) > 0 {
			f.Path = strings.TrimPrefix(a.Locations[0].Path, "/")
		}
		if a.Type == "rpm" {
			found := false
			for _, r := range rpms {
				if r.Name == a.Name {
					f.Src, f.Vendor, found = r.Src, r.Vendor, true
					break
				}
			}
			if !found && len(a.Upstreams) > 0 {
				f.Src = a.Upstreams[0].Name
			}
		}
		switch f.Type {
		case "gobinary":
			f.Owner = goBinaryOwner(f.Path, rpms)
		case "python-pkg":
			f.Owner = pythonOwner(f.PkgName, f.InstalledVersion, rpms)
		}
		k := key{f.ID, f.PkgName, f.Path}
		if seen[k] {
			continue
		}
		seen[k] = true
		findings = append(findings, f)
	}
	return findings, nil
}
