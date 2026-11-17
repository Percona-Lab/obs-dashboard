package cvereport

import (
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

type Report struct {
	GeneratedAt string          `json:"generated_at"`
	Staging     Staging         `json:"staging"`
	Released    []ReleasedImage `json:"released"`
}

// Build assembles the whole report. Both scan slices include clean scans.
func Build(staging, released []model.ImageCveScan, versions map[string]string, now time.Time) Report {
	return Report{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Staging:     BuildStaging(staging),
		Released:    BuildReleased(released, staging, versions),
	}
}
