package model

import "time"

type RollupState string

const (
	RollupFailed       RollupState = "failed"
	RollupBroken       RollupState = "broken"
	RollupUnresolvable RollupState = "unresolvable"
	RollupBlocked      RollupState = "blocked"
	RollupBuilding     RollupState = "building"
	// RollupFinished means the build worker finished but the scheduler has not yet
	// accepted the result. It is a transient state between building and succeeded/failed.
	RollupFinished  RollupState = "finished"
	RollupScheduled RollupState = "scheduled"
	RollupSucceeded RollupState = "succeeded"
	RollupPublished RollupState = "published" // terminal: all targets built and repos published
)

// Severity returns a sortable integer: higher = worse (for failure-first ordering).
func (s RollupState) Severity() int {
	switch s {
	case RollupBroken:
		return 5
	case RollupFailed:
		return 4
	case RollupUnresolvable:
		return 3
	case RollupBlocked:
		return 2
	case RollupBuilding, RollupFinished, RollupScheduled:
		return 1
	default:
		return 0
	}
}

type Target struct {
	Repo string `json:"repo"`
	Arch string `json:"arch"`
	// Instance is the slug of the OBS instance that hosts this repo/arch
	// target. Empty until the fleet records a repo owner (single-instance
	// deployments never set it).
	Instance            string     `json:"instance,omitempty"`
	State               string     `json:"state"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	Details             string     `json:"details,omitempty"`
	BlockedBy           string     `json:"blocked_by,omitempty"`
	BuildReason         string     `json:"build_reason,omitempty"`
	BuildReasonPackages []string   `json:"build_reason_packages,omitempty"`
	Published           bool       `json:"published,omitempty"`
	// BlockedByFetchedAt records when BlockedBy was last fetched from OBS.
	// In-memory only (json:"-"): excluded from targets_json storage and API
	// responses; zero after restart/MQ-replace → conservative refetch.
	BlockedByFetchedAt time.Time `json:"-"`
}

type CveFinding struct {
	ID               string   `json:"id"`
	PkgName          string   `json:"pkg"`
	InstalledVersion string   `json:"installed"`
	FixedVersion     string   `json:"fixed"`
	Severity         string   `json:"severity"`
	Title            string   `json:"title"`
	Class            string   `json:"class,omitempty"`    // "os-pkgs" | "lang-pkgs" (trivy's result Class; mapped for grype)
	Type             string   `json:"pkg_type,omitempty"` // "redhat", "gobinary", "python-pkg", … (trivy's result Type; mapped for grype)
	Path             string   `json:"path,omitempty"`     // binary (gobinary) or package file (other lang-pkgs); empty for RPMs
	Src              string   `json:"src,omitempty"`      // RPM source package name
	Vendor           string   `json:"vendor,omitempty"`   // RPM vendor (trivy "Maintainer"; for grype findings copied from trivy's package list, empty if the RPM is not listed)
	Owner            string   `json:"owner,omitempty"`    // source package of the RPM that ships Path, when resolved
	Scanners         []string `json:"scanners,omitempty"` // "trivy", "grype" or both
}

type CveScan struct {
	Repo          string       `json:"repo"`
	Arch          string       `json:"arch"`
	ImageRef      string       `json:"image_ref"`
	ScannedAt     time.Time    `json:"scanned_at"`
	CriticalCount int          `json:"critical_count"`
	HighCount     int          `json:"high_count"`
	CveSince      *time.Time   `json:"cve_since,omitempty"`
	CleanSince    *time.Time   `json:"clean_since,omitempty"`
	Findings      []CveFinding `json:"findings,omitempty"`
}

// ImageCveScan is a CveScan together with the image it belongs to, for
// listings that span more than one image.
type ImageCveScan struct {
	Project string `json:"project"`
	Package string `json:"package"`
	CveScan
}

type CvePeriod struct {
	Project    string    `json:"project"`
	Package    string    `json:"package"`
	Arch       string    `json:"arch"`
	CveSince   time.Time `json:"cve_since"`
	CleanSince time.Time `json:"clean_since"`
}

type Trigger struct {
	What string    `json:"what"`
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
}

type Package struct {
	Project        string      `json:"project"`
	Name           string      `json:"name"`
	Tags           []string    `json:"tags,omitempty"`
	IsRelease      bool        `json:"is_release,omitempty"`
	RollupState    RollupState `json:"rollup_state"`
	OKTargets      int         `json:"ok_targets"`
	TotalTargets   int         `json:"total_targets"`
	IsContainer    *bool       `json:"is_container,omitempty"`
	Version        string      `json:"version,omitempty"`
	ContainerTags  []string    `json:"container_tags,omitempty"`
	Trigger        *Trigger    `json:"trigger,omitempty"`
	Targets        []Target    `json:"targets"`
	UpdatedAt      time.Time   `json:"updated_at"`
	StateChangedAt *time.Time  `json:"state_changed_at,omitempty"`
	CveScans       []CveScan   `json:"cve_scans,omitempty"`
	Settled        bool        `json:"settled,omitempty"`
	// TargetsStable is set by BuildStateTask each worker pass: true only when
	// the previous pass had the same target set with identical states.
	// In-memory only (json:"-"); the zero value (false) on any cold-start path
	// forces downstream tasks to fetch.
	TargetsStable bool `json:"-"`
	// CacheWarm marks that this in-memory Package has completed at least one
	// BuildStateTask pass. TargetsStable may only be true when CacheWarm was
	// already set, so the first pass over any cold-start pointer (DB seed on
	// restart, MQ replace, fresh stub) always fetches. In-memory only (json:"-").
	CacheWarm bool `json:"-"`
}

type EventType string

const (
	EventTriggered       EventType = "triggered"
	EventStarted         EventType = "started"
	EventSucceeded       EventType = "succeeded"
	EventFailed          EventType = "failed"
	EventUnresolvable    EventType = "unresolvable"
	EventBroken          EventType = "broken"
	EventBlocked         EventType = "blocked"
	EventPublished       EventType = "published"
	EventCreated         EventType = "created"
	EventDeleted         EventType = "deleted"
	EventBuildStarted    EventType = "build_started"
	EventBuildFinished   EventType = "build_finished"
	EventVersionChange   EventType = "version_change"
	EventUpdated         EventType = "updated"
	EventCVEScanStarted  EventType = "cve_scan_started"
	EventCVEScanFinished EventType = "cve_scan_finished"
	EventCVEScanFailed   EventType = "cve_scan_failed"
)

type Event struct {
	ID      string    `json:"id"`
	Type    EventType `json:"type"`
	Tags    []string  `json:"tags,omitempty"`
	Project string    `json:"project"`
	Package string    `json:"package"`
	Repo    string    `json:"repo,omitempty"`
	Arch    string    `json:"arch,omitempty"`
	What    string    `json:"what"`
	Why     string    `json:"why"`
	Version string    `json:"version,omitempty"`
	URL     string    `json:"url"`
	At      time.Time `json:"at"`
	// Instance is the slug of the OBS instance the event came from; empty for
	// logical events (e.g. CVE scans).
	Instance string `json:"instance,omitempty"`
}
