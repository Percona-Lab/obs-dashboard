package release

import "time"

// Step is the wizard step of a release.
type Step string

const (
	StepCutting   Step = "cutting"
	StepChangelog Step = "changelog"
	StepSync      Step = "sync"
	StepQA        Step = "qa"
	StepReleasing Step = "releasing"
	StepDone      Step = "done"
)

// RunState is "queued" | "running" | "success" | "failure". The cut of a
// release (Release.Cut) can also be "approval".
type RunState string

const (
	StateQueued  RunState = "queued"
	StateRunning RunState = "running"
	StateSuccess RunState = "success"
	StateFailure RunState = "failure"

	// StateApproval is a cut run GitHub holds until a person with write
	// access approves it on the run page. It is not a failure.
	StateApproval RunState = "approval"
)

type RunRef struct {
	State RunState `json:"state"`
	URL   string   `json:"url"`
}

type InstanceCard struct {
	Instance   string     `json:"instance"`
	State      RunState   `json:"state"`
	URL        string     `json:"url"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	CDN        string     `json:"cdn"` // "" | queued | running | success | failure | skipped
	Errors     []string   `json:"errors"`
	// Units lists each release project of the populate report with its
	// outcome on this instance. Empty until the job has reported, and for
	// the cards of the final sync, which has no report.
	Units []CardUnit `json:"units"`
	// NothingToSync is true when the job succeeded and every entry of its
	// report was skipped: the instance had nothing to populate.
	NothingToSync bool `json:"nothing_to_sync"`
}

// CardUnit is one release project on one instance. Outcome is "success",
// "failure" or "skipped" (the instance holds nothing of the project).
type CardUnit struct {
	Project string `json:"project"`
	Outcome string `json:"outcome"`
}

type Removal struct {
	Project string `json:"project"`
	Item    string `json:"item"`
}

type Lane struct {
	Name       string   `json:"name"`
	Unit       string   `json:"unit"`
	JenkinsURL string   `json:"jenkins_url"`
	URL        string   `json:"url"`
	State      RunState `json:"state"`

	jobID int64 // the GitHub job of the lane, to run it again
}

type QA struct {
	State string `json:"state"` // waiting | running | finished | failure
	Lanes []Lane `json:"lanes"`
	URL   string `json:"url"`   // the QA run; "" while none is known
	Error string `json:"error"` // why QA could not run; set with state failure
	// Skipped says a user chose to release this head of the PR without QA.
	// The release may then be made whatever State is.
	Skipped   bool   `json:"skipped"`
	SkippedBy string `json:"skipped_by"` // who skipped; "" unless Skipped

	runID int64 // the QA run, to run its failed jobs again; 0 while none is known
}

type Final struct {
	Tags           []string       `json:"tags"`
	Instances      []InstanceCard `json:"instances"`
	GitHubReleases string         `json:"github_releases"` // "" | queued | running | success | failure | skipped
	Cleanup        string         `json:"cleanup"`
	State          RunState       `json:"state"`
	URL            string         `json:"url"`
}

type UnitState struct {
	Unit           string  `json:"unit"`
	ReleaseProject string  `json:"release_project"`
	ReleaseDir     string  `json:"release_dir"`
	ReleaseID      string  `json:"release_id"`
	Tag            string  `json:"tag"`
	Changelog      string  `json:"changelog"`
	Generated      *string `json:"generated"`
}

type AuditEntry struct {
	At     time.Time `json:"at"`
	Email  string    `json:"email"`
	Action string    `json:"action"`
}

type Release struct {
	ID        string      `json:"id"`
	Branch    string      `json:"branch"`
	PR        int         `json:"pr"`
	PRURL     string      `json:"pr_url"`
	HeadSHA   string      `json:"head_sha"`
	Step      Step        `json:"step"`
	Dashboard bool        `json:"dashboard"`
	Units     []UnitState `json:"units"`
	Cut       *RunRef     `json:"cut"`
	SyncState string      `json:"sync_state"`
	SyncURL   string      `json:"sync_url"` // the run the sync state was read from; "" while none
	// SyncError says why the sync failed when no single instance did: a unit
	// that every instance skipped was populated nowhere.
	SyncError string         `json:"sync_error"`
	Sync      []InstanceCard `json:"sync"`
	Removals  []Removal      `json:"removals"`
	QA        *QA            `json:"qa"`
	Final     *Final         `json:"final"`
	StartedBy string         `json:"started_by"`
	StartedAt *time.Time     `json:"started_at"`
	Actions   []AuditEntry   `json:"actions"`
	// PRError says why the release PR could not be opened, while the release
	// waits for it; "" otherwise. The dashboard keeps trying.
	PRError string `json:"pr_error"`
	// CutUnits names the unit roots the dashboard asked to cut, while Units
	// is still empty (the cut has not produced them yet). Empty otherwise.
	CutUnits []string `json:"cut_units"`

	// Not sent to the browser: what the action guards need beyond the above.
	syncRunDone  bool     // the run behind SyncState has completed
	finalRunDone bool     // the release run has completed
	pending      bool     // a cut was dispatched and GitHub does not list its run yet
	pendingUnits []string // units of a pending first cut
	heldUnits    []string // units of a cut that waits for approval, when GitHub shows none
	finished     bool     // a merged release that is done: not listed
}
