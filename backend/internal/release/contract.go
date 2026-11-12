// Package release drives the obs-packaging release workflows from the
// dashboard. The names in this file are the contract with the obs-packaging
// repository and must match it exactly.
package release

import (
	"strconv"
	"strings"
)

const (
	WorkflowCut     = "obs-release-cut.yml"
	WorkflowPR      = "obs-release-pr.yml"
	WorkflowRelease = "obs-release.yml"
	LabelSync       = "obs-sync"
	LabelQA         = "qa-release"
	BranchPrefix    = "release/"
	// ActorActions is the actor of what a workflow does with its own token:
	// the cut pushes the release branch as it.
	ActorActions = "github-actions[bot]"
	UnitsPath    = "docs/versions/release-units.json"

	JobPopulatePrefix = "Populate pre-release (" // + instance + ")"
	JobCDNPrefix      = "Wait for download CDN ("
	JobLanePrefix     = "Release / OBS QA / "
	// JobQAPrefix starts the name of every job of the QA fan-out, lanes or
	// not ("Release" is the caller job in obs-release-pr.yml).
	JobQAPrefix     = "Release / "
	JobCopyPrefix   = "Release to OBS ("
	JobFinalize     = "Publish GitHub releases and delete the pre-release"
	JobDeletePrefix = "Delete the pre-release ("

	AnnotationJenkins    = "Jenkins build"        // message: "<release project> <url>"
	ArtifactChangelogs   = "generated-changelogs" // one file changelogs.json: {"<unit root>": "<section>"}
	ArtifactReportPrefix = "prerelease-report-"   // + instance; JSON list of {project, outcome, removals[]}
)

// CutRunName is the display name of the cut workflow run for branch.
func CutRunName(branch string) string { return "Cut " + branch }

// ReleaseRunName is the display name of the release workflow run for a PR.
func ReleaseRunName(pr int) string { return "Release PR #" + strconv.Itoa(pr) }

// InstanceOf returns the text between prefix and the closing ")" of a job
// name, and whether name has that shape. A skipped matrix job keeps its
// unexpanded name ("… (${{ matrix.instance.name }})"): that is no instance.
func InstanceOf(name, prefix string) (string, bool) {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ")") {
		return "", false
	}
	inst := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ")")
	if inst == "" || strings.Contains(inst, "${{") {
		return "", false
	}
	return inst, true
}
