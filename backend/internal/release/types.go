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

// RunState is "queued" | "running" | "success" | "failure".
type RunState string

const (
	StateQueued  RunState = "queued"
	StateRunning RunState = "running"
	StateSuccess RunState = "success"
	StateFailure RunState = "failure"
)

type RunRef struct {
	State RunState `json:"state"`
	URL   string   `json:"url"`
}

type InstanceCard struct {
	Instance    string     `json:"instance"`
	State       RunState   `json:"state"`
	URL         string     `json:"url"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	CDN         string     `json:"cdn"` // "" | queued | running | success | failure | skipped
	FailedUnits []string   `json:"failed_units"`
	Errors      []string   `json:"errors"`
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
}

type QA struct {
	State string `json:"state"` // waiting | running | finished
	Lanes []Lane `json:"lanes"`
}

type Final struct {
	Tags           []string       `json:"tags"`
	Instances      []InstanceCard `json:"instances"`
	GitHubReleases string         `json:"github_releases"` // "" | queued | running | success | failure
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
	ID        string         `json:"id"`
	Branch    string         `json:"branch"`
	PR        int            `json:"pr"`
	PRURL     string         `json:"pr_url"`
	HeadSHA   string         `json:"head_sha"`
	Step      Step           `json:"step"`
	Dashboard bool           `json:"dashboard"`
	Units     []UnitState    `json:"units"`
	Cut       *RunRef        `json:"cut"`
	SyncState string         `json:"sync_state"`
	Sync      []InstanceCard `json:"sync"`
	Removals  []Removal      `json:"removals"`
	QA        *QA            `json:"qa"`
	Final     *Final         `json:"final"`
	StartedBy string         `json:"started_by"`
	StartedAt *time.Time     `json:"started_at"`
	Actions   []AuditEntry   `json:"actions"`
}
