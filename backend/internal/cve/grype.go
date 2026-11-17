package cve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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
		if sev != "HIGH" && sev != "CRITICAL" {
			continue
		}
		var fixed []string
		for _, v := range m.Vulnerability.Fix.Versions {
			if v = strings.TrimPrefix(v, "0:"); v != "" {
				fixed = append(fixed, v)
			}
		}
		if len(fixed) == 0 {
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

// mergeFindings returns trivy's findings followed by the grype findings trivy
// lacks. Key: (ID, PkgName, Path). On a duplicate the trivy finding is kept and
// "grype" is appended to its Scanners.
func mergeFindings(trivy, grype []model.CveFinding) []model.CveFinding {
	type key struct{ id, pkg, path string }
	merged := append([]model.CveFinding(nil), trivy...)
	idx := make(map[key]int, len(merged))
	for i, f := range merged {
		idx[key{f.ID, f.PkgName, f.Path}] = i
	}
	for _, f := range grype {
		k := key{f.ID, f.PkgName, f.Path}
		if i, ok := idx[k]; ok {
			merged[i].Scanners = append(append([]string(nil), merged[i].Scanners...), "grype")
			continue
		}
		idx[k] = len(merged)
		merged = append(merged, f)
	}
	return merged
}

// runGrype scans imageRef and returns its findings, enriched from rpms.
func (s *Scanner) runGrype(ctx context.Context, imageRef, platform string, rpms []rpmInfo) ([]model.CveFinding, error) {
	s.trivyMu.Lock()
	out, err := s.execFn(ctx, "grype",
		"registry:"+imageRef,
		"--platform", "linux/"+platform,
		"--only-fixed",
		"--by-cve",
		"-o", "json",
		"-q",
	)
	s.trivyMu.Unlock()
	if err != nil {
		stderr := ""
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			stderr = ": " + strings.TrimSpace(string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("grype: %w%s", err, stderr)
	}
	return parseGrypeOutput(out, rpms)
}
