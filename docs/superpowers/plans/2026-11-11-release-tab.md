# Release Tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A signed-in dashboard user runs a whole PPG release (select, changelog, pre-release sync, QA, release, final sync) from a new Release tab, and sees a history of finished releases.

**Architecture:** A new backend package `internal/release` talks to the `percona/obs-packaging` GitHub repository with a bearer token. It derives every release's wizard step from GitHub (PRs, labels, branches, workflow runs, jobs, annotations, artifacts), caches that snapshot for 15 seconds, and performs each wizard action as one GitHub call. The only stored data is an audit log of who did what. The frontend adds a `release` main tab that polls the snapshot while visible.

**Tech Stack:** Go (chi, `net/http`, `database/sql` + modernc SQLite, `archive/zip`), Vue 3 + TypeScript + Tailwind, vitest, `httptest`.

**Spec:** `docs/superpowers/specs/2026-11-11-release-tab-design.md`

## Global Constraints

- This plan covers the dashboard only. The obs-packaging side (spec § 6) is built in that repository; here every name from § 6 lives in `backend/internal/release/contract.go` and nowhere else.
- The GitHub token never appears in a response body, a log line or an error string. Errors from the GitHub client carry method, path and HTTP status only.
- No dashboard user's email or name is sent to GitHub in any field (PR title/body, comment, commit author or message, branch name, workflow input).
- The dashboard keeps no release state. `release_audit` is the only new table.
- All `/api/release/*` routes are behind `authSvc.RequireUser`; every non-GET also behind `authSvc.CheckOrigin`.
- With no token configured, the feature is off: `GET /api/release/status` returns `{"enabled":false}`, every other release route returns 404, the tab is hidden.
- Timestamps bound to SQLite are RFC3339Nano UTC strings (repo rule).
- Commits: `git commit -s`, no `Co-Authored-By` trailer (CLAUDE.md).
- Backend tests: `cd backend && go test ./...`. Frontend tests: `cd frontend && npx vitest run`. Type check: `cd frontend && npx vue-tsc --noEmit`.
- Frontend styling uses the existing Tailwind tokens (`bg-card`, `text-muted`, `ok`, `fail`, `warn`, `blocked`, `brand-purple`). Layout follows the mockups in `.superpowers/brainstorm/2543469-1794417318/content/`.

**User decisions (already made):**
- "I want to avoid going through the CLI and github."
- QA failures: "see which tests failed with links to the failed jenkins job. Fixing the failures should be done outside the dashboard."
- Re-cut: "rebase the release PR on top of latest main ... and re-run all workflows again"; afterwards "show me the new changelog and let me edit again."
- "changelog editing should amend the release commit"
- "only start the sync after approving the changelog", gated by the `obs-sync` PR label.
- Sync progress detail: option A, per OBS instance.
- "one authenticated user's decision is enough" to release.
- "allow releasing anyway after a confirmation" when QA lanes failed.
- "use a classic token from my account"
- Jenkins links: "use the annotation".
- Ineligible units: "show but not tickable with an alert explaining why".
- Acting user's identity: "keep it only in the dashboard".
- History: "what was released and when with links to github PR and released".

## Review Focus

1. GitHub unreachable, rate-limited or 5xx during a refresh: the snapshot endpoint keeps serving the last good snapshot with `stale: true` and the error text; it never returns an empty in-flight list (which would make in-flight units look selectable). Test in Task 7.
2. The token in an error path: a failing GitHub call must not echo the `Authorization` header or token. Test in Task 2.
3. Two users or a double click: an action issued against an old head SHA or step returns 409 and changes nothing; cutting a unit that is already in flight returns 409. Tests in Task 8.
4. A changelog whose release section is missing or duplicated, or with CRLF line endings: save refuses with a clear error instead of appending or corrupting the file; CRLF is preserved. Test in Task 3.
5. Run lookup by name with look-alike branches (`Cut release/a` vs `Cut release/a2`) and PR numbers (`Release PR #4` vs `#41`): matching is exact. Test in Task 4.

---

## File Structure

Backend (`backend/internal/release/`):

| File | Responsibility |
|---|---|
| `contract.go` | Every obs-packaging name: workflows, labels, job-name prefixes, artifact names, file paths |
| `github.go` | `Client`: typed GitHub REST calls, bearer auth, ETag cache |
| `changelog.go` | Extract / replace one release section of a `CHANGELOG.md`; parse `release.yaml` |
| `derive.go` | Pure functions: GitHub facts → wizard step, instance cards, lanes |
| `units.go` | Unit list + eligibility from the store |
| `service.go` | Snapshot cache/refresh, history, wizard actions, audit |
| `types.go` | JSON types shared by the above |

Other backend: `internal/store/release_audit.go`, `internal/config/config.go` (ReleaseConfig), `internal/api/release_routes.go`, `internal/api/server.go`, `cmd/obsboard/main.go`.

Frontend (`frontend/src/`): `types/release.ts`, `composables/useRelease.ts`, `lib/changelogPreview.ts`, `components/release/{ReleasePanel,ReleaseUnitList,ReleaseWizard,ReleaseChangelogStep,ReleaseRunCards,ReleaseQaStep,ReleaseHistory}.vue`, plus edits to `composables/useUrlState.ts`, `composables/useAuth.ts`, `components/AppHeader.vue`, `App.vue`.

---

### Task 1: Config and contract constants

**Goal:** `release.github_token` / `release.github_repo` are loaded, and all obs-packaging names exist as constants.

**Files:**
- Modify: `backend/internal/config/config.go`
- Modify: `backend/internal/config/config_test.go`
- Create: `backend/internal/release/contract.go`
- Create: `backend/internal/release/contract_test.go`
- Modify: `config.yaml.example`

**Acceptance Criteria:**
- [ ] `Config.Release` has `GitHubToken`, `GitHubRepo`; env `RELEASE_GITHUB_TOKEN`, `RELEASE_GITHUB_REPO`; default repo `percona/obs-packaging`; repo validated with the existing `repoRe`
- [ ] `ReleaseConfig.Enabled()` is true only when the token is non-empty
- [ ] `config.yaml.example` documents both keys, the required scopes (`repo`, `workflow`) and that the token should come from the env var

**Verify:** `cd backend && go test ./internal/config/ ./internal/release/` → PASS

**Steps:**

- [ ] **Step 1: Failing tests**

```go
// config_test.go
func TestReleaseConfigDefaultsAndEnv(t *testing.T) {
	cfg := loadForTest(t, "") // existing helper style in this file
	if cfg.Release.GitHubRepo != "percona/obs-packaging" || cfg.Release.Enabled() { t.Fatal("defaults") }
	t.Setenv("RELEASE_GITHUB_TOKEN", "ghp_x")
	cfg = loadForTest(t, "")
	if !cfg.Release.Enabled() { t.Fatal("token from env must enable") }
}
// contract_test.go
func TestRunNames(t *testing.T) {
	if CutRunName("release/20261111T143210") != "Cut release/20261111T143210" { t.Fatal() }
	if ReleaseRunName(41) != "Release PR #41" { t.Fatal() }
}
```

Use whatever config-loading helper `config_test.go` already has; add none.

- [ ] **Step 2: Run** `go test ./internal/config/ ./internal/release/` → FAIL (undefined)

- [ ] **Step 3: Implement**

`config.go`: `type ReleaseConfig struct{ GitHubToken, GitHubRepo string }`, `func (r ReleaseConfig) Enabled() bool`, `loadRelease(v)`, default + env binding next to the `upgrades.*` ones.

`contract.go`:

```go
const (
	WorkflowCut     = "obs-release-cut.yml"
	WorkflowPR      = "obs-release-pr.yml"
	WorkflowRelease = "obs-release.yml"
	LabelSync       = "obs-sync"
	LabelQA         = "qa-release"
	BranchPrefix    = "release/"
	UnitsPath       = "docs/versions/release-units.json"

	JobPopulatePrefix = "Populate pre-release ("      // + instance + ")"
	JobCDNPrefix      = "Wait for download CDN ("
	JobLanePrefix     = "Release / OBS QA / "
	JobCopyPrefix     = "Release to OBS ("
	JobFinalize       = "Publish GitHub releases and delete the pre-release"
	JobDeletePrefix   = "Delete the pre-release ("

	AnnotationJenkins    = "Jenkins build"          // message: "<release project> <url>"
	ArtifactChangelogs   = "generated-changelogs"   // one file changelogs.json: {"<unit root>": "<section>"}
	ArtifactReportPrefix = "prerelease-report-"     // + instance; JSON list of {project, outcome, removals[]}
)
func CutRunName(branch string) string  // "Cut " + branch
func ReleaseRunName(pr int) string     // "Release PR #<pr>"
// InstanceOf returns the text between prefix and the closing ")" of a job
// name, and whether name has that shape.
func InstanceOf(name, prefix string) (string, bool)
```

- [ ] **Step 4: Run** → PASS

- [ ] **Step 5: Commit** `git commit -s -m "feat(release): config and obs-packaging contract names"`

```json:metadata
{"files": ["backend/internal/config/config.go", "backend/internal/config/config_test.go", "backend/internal/release/contract.go", "backend/internal/release/contract_test.go", "config.yaml.example"], "verifyCommand": "cd backend && go test ./internal/config/ ./internal/release/", "acceptanceCriteria": ["Config.Release loaded from yaml and env with default repo percona/obs-packaging", "Enabled() true only with a token", "config.yaml.example documents keys and scopes"], "modelTier": "mechanical"}
```

---

### Task 2: GitHub client

**Goal:** A typed client for every GitHub call the feature makes, safe to log, with conditional requests.

**Files:**
- Create: `backend/internal/release/github.go`
- Create: `backend/internal/release/github_test.go`

**Acceptance Criteria:**
- [ ] Every request carries `Authorization: Bearer <token>`, `Accept: application/vnd.github+json`, `X-GitHub-Api-Version: 2022-11-28`
- [ ] GETs send `If-None-Match` for a previously seen URL and reuse the cached body on 304
- [ ] Errors are `*APIError{Method, Path, Status}`; `Error()` contains no header or token; 404 → `ErrNotFound`, 409 and 422 on merge/ref update → `ErrConflict`
- [ ] `AmendFile` replaces one file in the branch head commit, keeping message, author and parents, and force-updates the ref only if the head is still `expectHead` (else `ErrConflict`, nothing written)
- [ ] List calls follow `Link: rel="next"` up to the given limit

**Verify:** `cd backend && go test ./internal/release/ -run GitHub` → PASS

**Interfaces** (other tasks use exactly these):

```go
var ErrNotFound, ErrConflict error
type APIError struct{ Method, Path string; Status int }

type Pull struct{ Number int; Title, HTMLURL, HeadRef, HeadSHA, HeadRepo, BaseRepo string; Labels []string; State string; MergedAt *time.Time; MergeSHA string; CreatedAt time.Time }
type Ref struct{ Name, SHA string }                     // Name without refs/heads/
type Run struct{ ID int64; Name, WorkflowFile, Event, HeadSHA, Status, Conclusion, HTMLURL string; CreatedAt time.Time }
type Job struct{ ID int64; Name, Status, Conclusion, HTMLURL string; StartedAt, CompletedAt *time.Time }
type Annotation struct{ Level, Title, Message string }  // Level: notice|warning|failure
type GHRelease struct{ Tag, Body, HTMLURL string; PublishedAt time.Time }

type GitHub interface {
	OpenPulls(ctx context.Context) ([]Pull, error)
	MergedPulls(ctx context.Context, since time.Time) ([]Pull, error)   // closed, sort=updated desc, stop at updated<since
	PullFiles(ctx context.Context, number int) ([]string, error)
	CreatePull(ctx context.Context, head, title, body string) (Pull, error)   // base main
	ClosePull(ctx context.Context, number int) error
	MergePull(ctx context.Context, number int, headSHA string) error    // merge_method=rebase, sha=headSHA
	AddLabel(ctx context.Context, number int, label string) error
	RemoveLabel(ctx context.Context, number int, label string) error    // 404 is success
	Branches(ctx context.Context, prefix string) ([]Ref, error)         // git/matching-refs/heads/<prefix>
	DeleteBranch(ctx context.Context, branch string) error              // 404/422 is success
	File(ctx context.Context, path, ref string) ([]byte, error)
	AmendFile(ctx context.Context, branch, expectHead, path string, content []byte) (string, error)
	Dispatch(ctx context.Context, workflow string, inputs map[string]string) error  // ref main
	WorkflowRuns(ctx context.Context, workflow string, limit int) ([]Run, error)
	RunsForSHA(ctx context.Context, sha string) ([]Run, error)
	Jobs(ctx context.Context, runID int64) ([]Job, error)
	Annotations(ctx context.Context, jobID int64) ([]Annotation, error)
	Artifact(ctx context.Context, runID int64, name string) ([]byte, error)  // first file inside the zip
	Releases(ctx context.Context, page, perPage int) ([]GHRelease, error)
	TagSHA(ctx context.Context, tag string) (string, error)             // peels annotated tags
	PullForCommit(ctx context.Context, sha string) (int, string, error) // number, html_url; 0 when none
}
type Client struct{ APIBase, Repo, Token string; HTTP *http.Client }   // implements GitHub
```

**Steps:**

- [ ] **Step 1: Failing tests** against an `httptest.Server`:

```go
func TestGitHubAuthHeadersAndETag(t *testing.T)      // 2nd OpenPulls sends If-None-Match, server answers 304, result equals the 1st
func TestGitHubErrorHidesToken(t *testing.T) {
	// server returns 500; c.Token = "ghp_SECRET"
	_, err := c.OpenPulls(ctx)
	if strings.Contains(err.Error(), "ghp_SECRET") || strings.Contains(fmt.Sprintf("%+v", err), "Bearer") { t.Fatal("leaks token") }
	var ae *APIError; if !errors.As(err, &ae) || ae.Status != 500 { t.Fatal() }
}
func TestGitHubMergeConflict(t *testing.T)           // PUT .../merge → 409 → errors.Is(err, ErrConflict); request body has merge_method "rebase" and sha
func TestGitHubRemoveLabelMissingIsOK(t *testing.T)  // DELETE → 404 → nil
func TestGitHubAmendFile(t *testing.T) {
	// fake git data API holding one commit C1 (parents [P], message "release: x\n\nSigned-off-by: A <a@x>", author A)
	// AmendFile("release/b", C1, "root/ppg/releases/18/CHANGELOG.md", []byte("new"))
	// asserts: new commit has parents [P], same message, same author; tree built with base_tree = C1's tree;
	//          PATCH ref body {"sha": new, "force": true}; returns new sha
}
func TestGitHubAmendFileStaleHead(t *testing.T)      // ref now points at C2 → ErrConflict, no blob/tree/commit created
func TestGitHubArtifactUnzips(t *testing.T)          // artifacts list → zip download → bytes of the first file
func TestGitHubPagination(t *testing.T)              // Jobs follows Link rel="next"
```

- [ ] **Step 2: Run** → FAIL

- [ ] **Step 3: Implement `Client`.** One private `do(ctx, method, path, body, out)` builds the request, applies the ETag cache (map URL → {etag, body}, mutex) for GET, and maps status codes. `AmendFile` order: GET `git/ref/heads/<branch>` (compare with `expectHead`) → GET `git/commits/<sha>` → POST `git/blobs` → POST `git/trees` (`base_tree`) → POST `git/commits` (message, author copied, `parents` copied) → PATCH `git/refs/heads/<branch>` `force:true`. `Artifact`: GET `actions/runs/<id>/artifacts?name=<name>` → GET `actions/artifacts/<id>/zip` (follow redirect) → `archive/zip`, cap the read at 4 MiB. `MergedPulls` filters `merged_at != null`.

- [ ] **Step 4: Run** → PASS

- [ ] **Step 5: Commit** `git commit -s -m "feat(release): GitHub client"`

```json:metadata
{"files": ["backend/internal/release/github.go", "backend/internal/release/github_test.go"], "verifyCommand": "cd backend && go test ./internal/release/ -run GitHub", "acceptanceCriteria": ["bearer auth and API headers on every request", "ETag conditional GETs", "errors never contain the token", "AmendFile keeps message/author/parents and guards on expectHead", "pagination followed"], "modelTier": "standard"}
```

---

### Task 3: Changelog sections and release records

**Goal:** Pure functions to read and replace one release's section of a `CHANGELOG.md` and to read the current entry of a `release.yaml`.

**Files:**
- Create: `backend/internal/release/changelog.go`
- Create: `backend/internal/release/changelog_test.go`

**Acceptance Criteria:**
- [ ] A section runs from the line `## [<id>]…` up to (not including) the next line starting `## [`, or end of file
- [ ] `ReplaceSection` changes only that section; text before and after is byte-identical; CRLF files stay CRLF
- [ ] Zero or more than one section for the id → `ErrSectionNotFound` / `ErrSectionAmbiguous`
- [ ] The replacement must itself start with `## [<id>]` and contain no other `## [` heading, else `ErrBadSection`
- [ ] `CurrentRelease` returns the last `releases:` entry in either form (plain string, or `{tag, source_commit}`) and the `project:` value

**Verify:** `cd backend && go test ./internal/release/ -run 'Section|CurrentRelease'` → PASS

**Interfaces:**

```go
var ErrSectionNotFound, ErrSectionAmbiguous, ErrBadSection error
func ExtractSection(changelog []byte, id string) (string, error)
func ReplaceSection(changelog []byte, id, section string) ([]byte, error)
type Record struct{ Project, Tag, SourceCommit string }
func CurrentRelease(releaseYAML []byte) (Record, error)
func ReleaseID(tag string) string   // "ppg/18.6-6" → "18.6-6"; text after the first "/"
```

**Steps:**

- [ ] **Step 1: Failing tests**

```go
const cl = "# Changelog\n\n## [18.6-6] - 2026-11-11\n\n### Changed\n- a\n\n## [18.6-5] - 2026-10-01\n\n### Changed\n- b\n"
func TestExtractSection(t *testing.T) {
	got, _ := ExtractSection([]byte(cl), "18.6-6")
	want := "## [18.6-6] - 2026-11-11\n\n### Changed\n- a\n\n"
	// got == want
}
func TestReplaceSectionKeepsRest(t *testing.T) {
	out, _ := ReplaceSection([]byte(cl), "18.6-6", "## [18.6-6] - 2026-11-11\n\n### Changed\n- a\n- c\n\n")
	// out has prefix "# Changelog\n\n" and suffix "## [18.6-5] - 2026-10-01\n\n### Changed\n- b\n", contains "- c"
}
func TestReplaceSectionCRLF(t *testing.T)        // input with \r\n → output has no bare \n
func TestSectionMissing(t *testing.T)            // id "9.9-9" → ErrSectionNotFound
func TestSectionAmbiguous(t *testing.T)          // heading twice → ErrSectionAmbiguous
func TestReplaceRejectsForeignHeading(t *testing.T) // replacement containing "## [18.6-5]" or not starting with "## [18.6-6]" → ErrBadSection
func TestSectionIDIsExact(t *testing.T)          // "## [18.6-60]" must not match id "18.6-6"
func TestCurrentRelease(t *testing.T) {
	y := "repository: x\nproject: ppg:staging:18\nreleases:\n  - ppg/18.6-5\n  - tag: ppg/18.6-6\n    source_commit: abc\n"
	// → Record{Project:"ppg:staging:18", Tag:"ppg/18.6-6", SourceCommit:"abc"}; ReleaseID(tag)=="18.6-6"
	// plain-string last entry → SourceCommit ""
}
```

- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement.** Line scan, no regexp on user text beyond the heading prefix test `## [<id>]`. YAML: use the YAML library already in `go.mod` (viper pulls one in; add no new module).
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): changelog section and release record helpers"`

```json:metadata
{"files": ["backend/internal/release/changelog.go", "backend/internal/release/changelog_test.go"], "verifyCommand": "cd backend && go test ./internal/release/ -run 'Section|CurrentRelease'", "acceptanceCriteria": ["section boundaries exact", "replace leaves the rest byte-identical and keeps CRLF", "missing/ambiguous/foreign-heading cases rejected", "CurrentRelease reads both entry forms"], "modelTier": "mechanical"}
```

---

### Task 4: State derivation

**Goal:** Pure functions that turn GitHub facts into the wizard step, the per-instance cards and the QA lanes.

**Files:**
- Create: `backend/internal/release/types.go`
- Create: `backend/internal/release/derive.go`
- Create: `backend/internal/release/derive_test.go`

**Acceptance Criteria:**
- [ ] Step table of spec § 3 is implemented exactly (see `DeriveStep` below)
- [ ] Only runs whose `HeadSHA` equals the PR head count for sync and QA
- [ ] Sync run = newest `obs-release-pr.yml` run for the head with at least one `Populate pre-release (…)` job that is not skipped; QA run = newest such run with at least one lane job
- [ ] Run and job lookup by name is exact, never by prefix of a longer name
- [ ] A lane's unit and Jenkins URL come from its `Jenkins build` annotation; without one, both are empty

**Verify:** `cd backend && go test ./internal/release/ -run Derive` → PASS

**Interfaces** (`types.go`; JSON names are the API contract for the frontend):

```go
type Step string
const ( StepCutting Step = "cutting"; StepChangelog Step = "changelog"; StepSync Step = "sync"; StepQA Step = "qa"; StepReleasing Step = "releasing"; StepDone Step = "done" )
type RunState string   // "queued" | "running" | "success" | "failure"

type RunRef struct{ State RunState `json:"state"`; URL string `json:"url"` }
type InstanceCard struct {
	Instance string `json:"instance"`; State RunState `json:"state"`; URL string `json:"url"`
	StartedAt, FinishedAt *time.Time `json:"started_at","finished_at"`
	CDN string `json:"cdn"`                    // "" | queued | running | success | failure | skipped
	FailedUnits []string `json:"failed_units"`; Errors []string `json:"errors"`
}
type Removal struct{ Project, Item string }   // json: project, item
type Lane struct{ Name, Unit, JenkinsURL, URL string; State RunState }   // json: name, unit, jenkins_url, url, state
type QA struct{ State string `json:"state"`; Lanes []Lane `json:"lanes"` }   // waiting | running | finished
type Final struct {
	Tags []string `json:"tags"`; Instances []InstanceCard `json:"instances"`
	GitHubReleases, Cleanup string `json:"github_releases","cleanup"`   // "" | queued | running | success | failure
	State RunState `json:"state"`; URL string `json:"url"`
}
type UnitState struct{ Unit, ReleaseProject, ReleaseDir, ReleaseID, Tag, Changelog string; Generated *string } // json snake_case
type AuditEntry struct{ At time.Time; Email, Action string }
type Release struct {
	ID, Branch string; PR int; PRURL, HeadSHA string; Step Step; Dashboard bool
	Units []UnitState; Cut *RunRef; SyncState string; Sync []InstanceCard; Removals []Removal
	QA *QA; Final *Final; StartedBy string; StartedAt *time.Time; Actions []AuditEntry
}

type Facts struct {
	Pull *Pull            // nil while no PR exists
	CutRun *Run           // newest run named exactly CutRunName(branch)
	HeadRuns []Run; Jobs map[int64][]Job; Annotations map[int64][]Annotation   // runs for Pull.HeadSHA
	ReleaseRun *Run; ReleaseJobs []Job
}
func RunStateOf(status, conclusion string) RunState      // completed+success→success; completed+other→failure; in_progress→running; else queued
func FindRun(runs []Run, name string) *Run               // newest with Name == name
func DeriveStep(f Facts) Step
func SyncCards(f Facts) (state string, cards []InstanceCard, run *Run)
func QALanes(f Facts, units []UnitState, hasQALabel bool) QA
func FinalStatus(f Facts) *Final
```

`DeriveStep`:

| Facts | Step |
|---|---|
| `Pull == nil` | `cutting` |
| Pull merged, release run conclusion success | `done` |
| Pull merged otherwise | `releasing` |
| Pull open, cut run for the branch queued/running | `cutting` |
| Pull open, no `obs-sync` label | `changelog` |
| `obs-sync`, `SyncCards` state != success | `sync` |
| `obs-sync`, sync success | `qa` |

`SyncCards` state: `queued` when no sync run exists yet; `failure` if any populate or CDN job failed; `success` when every populate job succeeded and every CDN job is success or skipped; else `running`. `QALanes` state: `waiting` without the label; `finished` when the QA run is completed; else `running`. A lane's `Unit` is the unit whose `ReleaseProject` equals, or is a `:`-prefix of, the annotation's first word.

**Steps:**

- [ ] **Step 1: Failing tests** — table-driven `TestDeriveStep` with one row per line of the table above, plus:

```go
func TestDeriveIgnoresRunsForOldHead(t *testing.T)   // successful populate run with HeadSHA "old", Pull.HeadSHA "new", label present → StepSync, state "queued"
func TestFindRunExact(t *testing.T) {
	runs := []Run{{Name: "Cut release/a2"}, {Name: "Release PR #41"}}
	if FindRun(runs, CutRunName("release/a")) != nil || FindRun(runs, ReleaseRunName(4)) != nil { t.Fatal("prefix match") }
}
func TestSyncCardsPerInstance(t *testing.T)          // jobs "Populate pre-release (isv)" success, "Populate pre-release (labs)" failure, "Wait for download CDN (isv)" skipped → state failure; card isv success cdn "skipped"; card labs failure
func TestSyncRunSkippedPopulateDoesNotCount(t *testing.T) // newest run (QA-only, populate jobs conclusion "skipped") is not the sync run; the older one is
func TestQALanes(t *testing.T) {
	// jobs: "Release / OBS QA / packages / pg_tde" failure with annotation {notice,"Jenkins build","ppg:releases:18 https://j/job/1/"},
	//       "Release / OBS QA / containers / ubi9" success no annotation, "Release / Detect QA matrix (isv)" success
	// units: {Unit:"ppg:staging:18", ReleaseProject:"ppg:releases:18"}
	// → 2 lanes; first: Unit "ppg:staging:18", JenkinsURL "https://j/job/1/", State failure; second: Unit "", JenkinsURL ""
}
func TestQALaneUnitPrefixBoundary(t *testing.T)      // annotation project "ppg:releases:18:extras:containers" → unit with ReleaseProject "ppg:releases:18:extras", not "ppg:releases:18"; "ppg:releases:180" matches neither
func TestFinalStatus(t *testing.T)                   // "Release to OBS (isv)" running, finalize queued → State running; all success → success
```

- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement.** For the unit match, prefer the longest matching `ReleaseProject`.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): derive wizard state from GitHub facts"`

```json:metadata
{"files": ["backend/internal/release/types.go", "backend/internal/release/derive.go", "backend/internal/release/derive_test.go"], "verifyCommand": "cd backend && go test ./internal/release/ -run Derive", "acceptanceCriteria": ["step table implemented", "only runs for the PR head count", "sync run and QA run selection rules", "exact name matching", "lane unit and Jenkins URL from annotation"], "modelTier": "standard"}
```

---

### Task 5: Audit log table

**Goal:** Store and read who performed each release action.

**Files:**
- Modify: `backend/internal/store/db.go` (schema)
- Create: `backend/internal/store/release_audit.go`
- Create: `backend/internal/store/release_audit_test.go`

**Acceptance Criteria:**
- [ ] Table `release_audit(id INTEGER PRIMARY KEY, at TEXT NOT NULL, email TEXT NOT NULL, action TEXT NOT NULL, branch TEXT NOT NULL, pr INTEGER NOT NULL DEFAULT 0, detail TEXT NOT NULL DEFAULT '')`, index on `branch`, created with `CREATE TABLE IF NOT EXISTS`
- [ ] Rows come back oldest first per branch; `at` round-trips as UTC

**Verify:** `cd backend && go test ./internal/store/ -run ReleaseAudit` → PASS

**Interfaces:**

```go
type ReleaseAudit struct{ At time.Time; Email, Action, Branch string; PR int; Detail string }
func AddReleaseAudit(db *sql.DB, a ReleaseAudit) error
func ReleaseAuditByBranch(db *sql.DB, branches []string) (map[string][]ReleaseAudit, error)
func ReleaseAuditByPR(db *sql.DB, prs []int, action string) (map[int]ReleaseAudit, error) // newest row per PR for that action
```

Actions (exact strings): `cut`, `save_changelog`, `approve_changelog`, `retry_sync`, `approve_qa`, `recut`, `release`, `retry_release`, `abandon`. `Detail` holds the failed lane names (newline-separated) for `release`.

**Steps:**

- [ ] **Step 1: Failing test** `TestReleaseAuditRoundTrip`: add `cut` then `release` (detail `"lane a\nlane b"`, pr 7) for branch `release/x`; `ReleaseAuditByBranch` returns both in order; `ReleaseAuditByPR([]int{7}, "release")` returns the second; empty input slices return empty maps without a query error.
- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement** in the style of the existing store files (open DB helper from existing store tests).
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): audit log table"`

```json:metadata
{"files": ["backend/internal/store/db.go", "backend/internal/store/release_audit.go", "backend/internal/store/release_audit_test.go"], "verifyCommand": "cd backend && go test ./internal/store/ -run ReleaseAudit", "acceptanceCriteria": ["release_audit table and index created idempotently", "rows returned oldest first per branch, UTC timestamps"], "modelTier": "mechanical"}
```

---

### Task 6: Units and eligibility

**Goal:** Build the selection list: every release unit with its built count and whether it can be ticked, and why not.

**Files:**
- Create: `backend/internal/release/units.go`
- Create: `backend/internal/release/units_test.go`

**Acceptance Criteria:**
- [ ] The unit list is read from `UnitsPath` on `main`; a missing or malformed file is an error, never an empty list
- [ ] `built`/`total` count packages across all member projects; built means `rollup_state` is `succeeded` or `published`
- [ ] Blocking reasons, checked in this order: `in_flight`, `not_built`, `unchanged`
- [ ] `unchanged`: the multiset of (package name, version, container tags) over the unit's staging member projects equals that over the matching release projects (member path with `staging` replaced by `releases`); a unit with no release packages at all is never `unchanged`
- [ ] `not_built` lists the names of packages that are not built (at most 10, then a count)

**Verify:** `cd backend && go test ./internal/release/ -run Unit` → PASS

**Interfaces:**

```go
type UnitDef struct{ Unit string `json:"unit"`; Members []string `json:"members"`; ReleaseProject string `json:"release_project"`; ReleaseDir string `json:"release_dir"` }
func ParseUnits(b []byte) ([]UnitDef, error)
type Blocked struct{ Reason, Text string; Packages []string; ReleaseID string }   // json: reason, text, packages, release_id
type UnitRow struct{ UnitDef; LastRelease string `json:"last_release"`; Built, Total int; Tickable bool; Blocked *Blocked `json:"blocked,omitempty"` }
// PackageSource is satisfied by a thin adapter over store.QueryPackages.
type PackageSource interface{ Packages(projectPrefix string) ([]*model.Package, error) }
func BuildUnitRows(defs []UnitDef, src PackageSource, inFlight map[string]string /* unit → release id */, lastRelease map[string]string) ([]UnitRow, error)
```

`store.QueryPackages(db, prefix)` returns a project and its subprojects; filter to exact member project names.

**Steps:**

- [ ] **Step 1: Failing tests** with a fake `PackageSource`:

```go
func TestParseUnitsRejectsGarbage(t *testing.T)   // "" , "{}" , "[{\"unit\":\"\"}]" → error
func TestUnitTickable(t *testing.T)               // all members published, release has an older version → Tickable, Blocked nil, Built==Total
func TestUnitNotBuilt(t *testing.T)               // one package failed in member "…:containers" → reason not_built, Packages contains it
func TestUnitInFlightWins(t *testing.T)           // inFlight has the unit and a package failed → reason in_flight, ReleaseID set
func TestUnitUnchanged(t *testing.T)              // same name/version/tags on both sides → reason unchanged
func TestUnitNoReleaseYet(t *testing.T)           // no release packages → tickable
func TestUnitMembersAreExact(t *testing.T)        // packages of "ppg:staging:18:extras" do not count for unit "ppg:staging:18" (members top, containers, tarballs)
```

- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): unit list and eligibility"`

```json:metadata
{"files": ["backend/internal/release/units.go", "backend/internal/release/units_test.go"], "verifyCommand": "cd backend && go test ./internal/release/ -run Unit", "acceptanceCriteria": ["unit list from the contract file, errors on missing/malformed", "built counts over exact member projects", "blocking reasons in order in_flight, not_built, unchanged", "unchanged compares name/version/tags against release projects"], "modelTier": "standard"}
```

---

### Task 7: Service — snapshot, history

**Goal:** Assemble the snapshot of in-flight releases and the history from GitHub, cached and safe against GitHub failures.

**Files:**
- Create: `backend/internal/release/service.go`
- Create: `backend/internal/release/service_test.go`
- Create: `backend/internal/release/fake_github_test.go` (in-memory `GitHub` used by Tasks 7 and 8)

**Acceptance Criteria:**
- [ ] A release is in flight when: a `release/*` branch exists (with or without PR), or an open PR changes a `release.yaml`, or a PR merged in the last 7 days changes a `release.yaml` and its `Release PR #N` run has not succeeded
- [ ] Release `ID`: the branch name after `release/` for dashboard branches, else `pr-<N>`
- [ ] A branch with a successful cut run and no PR gets its PR opened during refresh (title `Release <id>, <id>…`, body the generated changelog sections, no user identity); a failed or running cut run opens nothing
- [ ] Fork PRs (head repo ≠ base repo) are listed but `Dashboard=false` and every action on them is refused
- [ ] The snapshot is reused for 15 s; concurrent callers share one refresh
- [ ] If a refresh fails, the previous snapshot is returned with `Stale=true` and `Error` set; with no previous snapshot the call returns the error
- [ ] Units of a release come from its changed `release.yaml` files, each with its changelog section and, when the cut artifact exists, the generated text
- [ ] For the sync run, each instance's `prerelease-report-<instance>` artifact fills `Removals` (every `removals` entry, with its `project`) and the card's `FailedUnits` (projects whose `outcome` is not `success`); a failed populate job's `failure` annotations fill the card's `Errors`. A missing artifact leaves them empty
- [ ] `Units()` reads `LastRelease` for each unit from `<release_dir>/release.yaml` on `main` (empty when the file does not exist)
- [ ] History: one row per GitHub release whose tag is the last or an earlier entry of some unit's `release.yaml`, newest first, with PR link (via tag → commit → PR), cached per tag; audit data joined when present

**Verify:** `cd backend && go test ./internal/release/ -run 'Snapshot|History'` → PASS

**Interfaces:**

```go
type Options struct{ GitHub GitHub; DB *sql.DB; Packages PackageSource; Now func() time.Time; TTL time.Duration /* 15s */ }
type Service struct{ /* … */ }
func NewService(o Options) *Service
type Snapshot struct{ Releases []Release `json:"releases"`; Stale bool `json:"stale"`; Error string `json:"error"`; RefreshedAt time.Time `json:"refreshed_at"` }
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error)
func (s *Service) Invalidate()                       // next Snapshot refreshes
func (s *Service) Units(ctx context.Context) ([]UnitRow, error)   // uses Snapshot for inFlight
type HistoryRow struct{ Tag, ReleaseID, Unit string; ReleasedAt time.Time; ReleaseURL, PRURL, Notes, ReleasedBy string; FailedLanes []string } // json snake_case
func (s *Service) History(ctx context.Context, page int) ([]HistoryRow, error)   // 20 per page
```

**Steps:**

- [ ] **Step 1: Write `fake_github_test.go`**: a struct with maps for pulls, pull files, branches, files by `ref:path`, runs, jobs, annotations, artifacts, releases, plus a `calls []string` log and an `err error` that every method returns when set.

- [ ] **Step 2: Failing tests**

```go
func TestSnapshotStepsEndToEnd(t *testing.T)     // one fixture per step (cutting, changelog, sync, qa, releasing); asserts Release.Step, Units[0].ReleaseID "18.6-6", Changelog section text
func TestSnapshotOpensPRAfterCut(t *testing.T)   // branch + successful "Cut release/x" run + no PR → calls contains CreatePull once; body lacks "@"; second refresh does not create again
func TestSnapshotNoPRWhileCutRunning(t *testing.T)
func TestSnapshotHandMadePR(t *testing.T)        // open PR from branch "release-pg-18" changing root/ppg/releases/18/release.yaml → ID "pr-47", Dashboard true, StartedBy ""
func TestSnapshotForkPR(t *testing.T)            // HeadRepo != BaseRepo → Dashboard false
func TestSnapshotMergedLeavesWhenReleaseRunSucceeded(t *testing.T)
func TestSnapshotCached(t *testing.T)            // two calls within TTL → one OpenPulls call; after Invalidate → two
func TestSnapshotStaleOnGitHubError(t *testing.T) {
	// first call ok with one release; then fake.err = &APIError{Status: 403}; advance clock 20s
	snap, err := svc.Snapshot(ctx)
	// err == nil, snap.Stale, snap.Error != "", len(snap.Releases) == 1
}
func TestSnapshotErrorWithoutPrevious(t *testing.T)   // fake.err set from the start → err != nil
func TestSnapshotStartedByFromAudit(t *testing.T)     // audit row cut for the branch → StartedBy email, StartedAt
func TestSnapshotRemovalsAndFailedUnits(t *testing.T) // report artifact [{project:"ppg:releases:18",outcome:"failure",removals:["package python3-six"]}] + failure annotation "staging not green" → Removals[0]{ppg:releases:18, package python3-six}; card FailedUnits [ppg:releases:18], Errors [staging not green]
func TestHistoryRows(t *testing.T)                    // releases ppg/18.6-6 and ppg/18.6-5 + audit release row for the PR → rows newest first, PRURL set, ReleasedBy and FailedLanes on the first, Unit "ppg:staging:18"
func TestHistoryTagWithoutPR(t *testing.T)            // PullForCommit returns 0 → PRURL ""
```

- [ ] **Step 3: Run** → FAIL
- [ ] **Step 4: Implement.** Refresh under a mutex with a `done` channel for sharing. Per release gather `Facts` (Task 4) and derive. Cache `PullFiles` for merged PRs and `TagSHA`/`PullForCommit` per tag forever (immutable). The unit of a `release.yaml` path is its `project:` value; `ReleaseDir` is the file's directory. For history, map tag → unit by reading each unit's `release.yaml` on `main` (all entries).
- [ ] **Step 5: Run** → PASS
- [ ] **Step 6: Commit** `git commit -s -m "feat(release): snapshot and history service"`

```json:metadata
{"files": ["backend/internal/release/service.go", "backend/internal/release/service_test.go", "backend/internal/release/fake_github_test.go"], "verifyCommand": "cd backend && go test ./internal/release/ -run 'Snapshot|History'", "acceptanceCriteria": ["in-flight detection rules", "PR auto-opened once after a successful cut, without user identity", "15s cache with shared refresh", "stale snapshot served on GitHub failure", "history rows with PR link and audit join", "removals, failed units and errors from report artifacts and annotations", "last release per unit from release.yaml on main"], "modelTier": "frontier"}
```

---

### Task 8: Service — wizard actions

**Goal:** Each wizard button is one guarded method that performs the GitHub action and writes an audit row.

**Files:**
- Modify: `backend/internal/release/service.go`
- Create: `backend/internal/release/actions_test.go`

**Acceptance Criteria:**
- [ ] Every action takes the acting email, the release id and (except `Cut`) the head SHA the client saw; a different current head, a wrong step, or `Dashboard=false` → `ErrConflict`, no GitHub write, no audit row
- [ ] Every successful action writes one audit row and calls `Invalidate`
- [ ] No action passes the email to any `GitHub` method

**Verify:** `cd backend && go test ./internal/release/ -run Action` → PASS

**Interfaces and rules:**

```go
var ErrConflict, ErrBadRequest error   // ErrConflict re-exported from github.go's
func (s *Service) Cut(ctx, email string, units []string) (id string, err error)
func (s *Service) SaveChangelog(ctx, email, id, head, unit, section string) (newHead string, err error)
func (s *Service) ResetChangelog(ctx, email, id, head, unit string) (newHead string, err error)
func (s *Service) ApproveChangelog(ctx, email, id, head string) error
func (s *Service) RetrySync(ctx, email, id, head string) error
func (s *Service) ApproveQA(ctx, email, id, head string) error
func (s *Service) Recut(ctx, email, id, head string) error
func (s *Service) Release(ctx, email, id, head string, confirmFailed bool) error
func (s *Service) RetryRelease(ctx, email, id string) error
func (s *Service) Abandon(ctx, email, id, head string) error
```

| Action | Allowed step | Does |
|---|---|---|
| `Cut` | — | every unit must be `Tickable` (else `ErrConflict` naming the unit); branch `release/<UTC 20060102T150405>`; `Dispatch(WorkflowCut, {units: "a b", branch})` |
| `SaveChangelog` | `changelog`, cut not running | `File` → `ReplaceSection` → `AmendFile(branch, head, <ReleaseDir>/CHANGELOG.md)`; section errors → `ErrBadRequest` |
| `ResetChangelog` | same | as save, with the unit's `Generated` text; none available → `ErrBadRequest` |
| `ApproveChangelog` | `changelog`, cut not running or failed | `AddLabel(LabelSync)` |
| `RetrySync` | `sync` with state `failure` | `RemoveLabel(LabelSync)` then `AddLabel(LabelSync)` |
| `ApproveQA` | `qa` with QA `waiting` | `AddLabel(LabelQA)` |
| `Recut` | `changelog`, `sync`, `qa` | `RemoveLabel` both, then `Dispatch(WorkflowCut, {units, branch})`; only for branches with `BranchPrefix` |
| `Release` | `qa` with QA `finished` | failed lanes and `!confirmFailed` → `ErrBadRequest`; `MergePull(pr, head)`; audit detail = failed lane names |
| `RetryRelease` | `releasing` with final state `failure` | `Dispatch(WorkflowRelease, {pr_number, tags: "t1 t2"})` |
| `Abandon` | any before merge | `ClosePull` (if a PR exists), `DeleteBranch` |

**Steps:**

- [ ] **Step 1: Failing tests** (fake GitHub from Task 7):

```go
func TestActionCutDispatches(t *testing.T)          // inputs {"units":"ppg:staging:18 ppg:staging:17:extras","branch":"release/20261111T143210"} with Now fixed; audit row "cut" for that branch
func TestActionCutRefusesInFlightUnit(t *testing.T) // → ErrConflict, no Dispatch
func TestActionSaveChangelogAmends(t *testing.T)    // AmendFile called with path "root/ppg/releases/18/CHANGELOG.md", expectHead = head; returns new head
func TestActionSaveStaleHead(t *testing.T)          // head "old" → ErrConflict; calls has no AmendFile; no audit row
func TestActionSaveAfterApprovalRefused(t *testing.T) // PR has obs-sync → ErrConflict
func TestActionSaveBadSection(t *testing.T)         // section with a second "## [" heading → ErrBadRequest
func TestActionApproveChangelog(t *testing.T)       // AddLabel(pr, "obs-sync")
func TestActionRetrySyncRelabels(t *testing.T)      // calls order: RemoveLabel obs-sync, AddLabel obs-sync
func TestActionRecutRemovesLabelsThenDispatches(t *testing.T) // order: RemoveLabel obs-sync, RemoveLabel qa-release, Dispatch; units = the release's units
func TestActionRecutHandMadeRefused(t *testing.T)   // ID "pr-47" → ErrConflict
func TestActionReleaseNeedsConfirm(t *testing.T)    // one failed lane, confirmFailed false → ErrBadRequest, no MergePull; true → MergePull(pr, head), audit detail "Release / OBS QA / packages / pg_tde"
func TestActionReleaseBeforeQAFinished(t *testing.T) // QA running → ErrConflict
func TestActionRetryReleaseInputs(t *testing.T)     // {"pr_number":"415","tags":"ppg/18.6-6 ppg/extras-17.10-2"}
func TestActionAbandon(t *testing.T)                // ClosePull + DeleteBranch; after merge → ErrConflict
func TestActionNeverSendsEmail(t *testing.T)        // run every action with email "someone@percona.com"; no recorded call argument contains "someone" or "@percona.com"
func TestActionForkRefused(t *testing.T)
```

- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement.** One private `guard(ctx, id, head, allowed func(Release) bool) (Release, error)` that takes a fresh snapshot (`Invalidate` first) and applies the three checks; every action goes through it.
- [ ] **Step 4: Run** → PASS; then `go test ./internal/release/` → PASS
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): wizard actions with audit"`

```json:metadata
{"files": ["backend/internal/release/service.go", "backend/internal/release/actions_test.go"], "verifyCommand": "cd backend && go test ./internal/release/", "acceptanceCriteria": ["each action guarded by id, head SHA and step; conflicts write nothing", "one audit row per successful action", "email never passed to GitHub", "action table implemented"], "modelTier": "standard"}
```

---

### Task 9: HTTP routes and wiring

**Goal:** Expose the service under `/api/release/*` and construct it in `main`.

**Files:**
- Create: `backend/internal/api/release_routes.go`
- Create: `backend/internal/api/release_routes_test.go`
- Modify: `backend/internal/api/server.go` (`NewRouter` gains a `rel *release.Service` parameter, nil when disabled)
- Modify: `backend/cmd/obsboard/main.go`
- Modify: existing `NewRouter` call sites in tests

**Acceptance Criteria:**
- [ ] Routes (all `RequireUser`; writes also `CheckOrigin`):

| Method + path | Body | Response |
|---|---|---|
| `GET /api/release/status` | | `{"enabled": bool}` (registered even when disabled) |
| `GET /api/release/units` | | `{"units": [UnitRow]}` |
| `GET /api/release/releases` | | `Snapshot` |
| `GET /api/release/history?page=N` | | `{"rows": [HistoryRow]}` |
| `POST /api/release/releases` | `{"units": [..]}` | `{"id": ".."}` 201 |
| `PUT /api/release/releases/{id}/changelog` | `{"head","unit","section"}` | `{"head": ".."}` |
| `POST /api/release/releases/{id}/changelog/reset` | `{"head","unit"}` | `{"head": ".."}` |
| `POST /api/release/releases/{id}/{action}` | `{"head", "confirm_failed"?}` | 204 |

  `{action}` ∈ `approve-changelog`, `retry-sync`, `approve-qa`, `recut`, `release`, `retry-release`, `abandon`; anything else 404.
- [ ] Status mapping: `ErrConflict` → 409, `ErrBadRequest` → 400 with the message, `ErrNotFound` → 404, other → 502 with body `github request failed` (details logged, not returned)
- [ ] Signed out → 401 on every route; disabled → 404 on every route except `status`
- [ ] Request bodies capped at 256 KiB
- [ ] `main.go` builds `release.Client{Repo, Token}` and the service only when `cfg.Release.Enabled()`, and logs `release: enabled` with the repo (never the token)

**Verify:** `cd backend && go test ./...` → PASS

**Steps:**

- [ ] **Step 1: Failing tests** in the style of `upgrades_routes_test.go` (reuse its signed-in request helper): `TestReleaseRoutesRequireUser`, `TestReleaseDisabled404`, `TestReleaseStatusWhenDisabled`, `TestReleaseConflictIs409`, `TestReleaseBadSectionIs400`, `TestReleaseGitHubFailureIs502NoDetails` (response body must not contain the fake error's path), `TestReleaseUnknownAction404`, `TestReleaseWriteNeedsOrigin`.

  The handlers take an interface declared in `release_routes.go` listing the `*release.Service` methods they call, so tests pass a stub.

- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement** handlers, register them next to the upgrades group, wire `main.go` (`PackageSource` adapter over `store.QueryPackages`).
- [ ] **Step 4: Run** `go test ./...` and `go vet ./...` → PASS
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): API routes"`

```json:metadata
{"files": ["backend/internal/api/release_routes.go", "backend/internal/api/release_routes_test.go", "backend/internal/api/server.go", "backend/cmd/obsboard/main.go"], "verifyCommand": "cd backend && go test ./... && go vet ./...", "acceptanceCriteria": ["route table implemented behind RequireUser/CheckOrigin", "error mapping 409/400/404/502 without leaking details", "disabled → 404 except status", "service constructed only when a token is configured"], "modelTier": "standard"}
```

---

### Task 10: Frontend plumbing — types, composable, tab

**Goal:** The Release tab exists for signed-in users when the feature is enabled, backed by a polling composable.

**Files:**
- Create: `frontend/src/types/release.ts`
- Create: `frontend/src/composables/useRelease.ts`
- Create: `frontend/src/composables/useRelease.test.ts`
- Create: `frontend/src/components/release/ReleasePanel.vue` (placeholder body: in-flight count; filled in by Tasks 11–13)
- Modify: `frontend/src/composables/useUrlState.ts`, `frontend/src/composables/useAuth.ts`, `frontend/src/components/AppHeader.vue`, `frontend/src/components/AppHeader.test.ts`, `frontend/src/App.vue`

**Acceptance Criteria:**
- [ ] `MainTab` includes `'release'`; the URL state accepts it
- [ ] The header shows a "Release" pill only when signed in and `enabled`; the existing guard also sends signed-out visitors away from `release`
- [ ] `useRelease()` polls `GET /api/release/releases` every 15 s only while `active` is true and the document is visible; stops otherwise
- [ ] Every action helper POSTs/PUTs JSON, and on any response refreshes the snapshot; 409 sets `notice` to "This release changed. The view has been refreshed."; other failures set `notice` to the response text
- [ ] Types mirror Task 4/6/7 JSON exactly (snake_case)

**Verify:** `cd frontend && npx vitest run src/composables/useRelease.test.ts src/components/AppHeader.test.ts && npx vue-tsc --noEmit` → PASS

**Interfaces:**

```ts
// types/release.ts — field names as emitted by the backend
export type ReleaseStep = 'cutting' | 'changelog' | 'sync' | 'qa' | 'releasing'
export type RunState = 'queued' | 'running' | 'success' | 'failure'
export interface UnitRow { unit: string; members: string[]; release_project: string; release_dir: string; last_release: string; built: number; total: number; tickable: boolean; blocked?: { reason: 'in_flight' | 'not_built' | 'unchanged'; text: string; packages: string[]; release_id: string } }
export interface InstanceCard { instance: string; state: RunState; url: string; started_at: string | null; finished_at: string | null; cdn: string; failed_units: string[]; errors: string[] }
export interface Lane { name: string; unit: string; jenkins_url: string; url: string; state: RunState }
export interface UnitState { unit: string; release_project: string; release_dir: string; release_id: string; tag: string; changelog: string; generated: string | null }
export interface ReleaseState { id: string; branch: string; pr: number; pr_url: string; head_sha: string; step: ReleaseStep; dashboard: boolean; units: UnitState[]; cut: { state: RunState; url: string } | null; sync_state: string; sync: InstanceCard[]; removals: { project: string; item: string }[]; qa: { state: 'waiting' | 'running' | 'finished'; lanes: Lane[] } | null; final: { tags: string[]; instances: InstanceCard[]; github_releases: string; cleanup: string; state: RunState; url: string } | null; started_by: string; started_at: string | null; actions: { at: string; email: string; action: string }[] }
export interface Snapshot { releases: ReleaseState[]; stale: boolean; error: string; refreshed_at: string }
export interface HistoryRow { tag: string; release_id: string; unit: string; released_at: string; release_url: string; pr_url: string; notes: string; released_by: string; failed_lanes: string[] }

// composables/useRelease.ts (module-level singleton, like useAuth)
export function useRelease(): {
  enabled: Ref<boolean>; snapshot: Ref<Snapshot | null>; units: Ref<UnitRow[]>; unitsError: Ref<string>; notice: Ref<string>; busy: Ref<boolean>
  loadStatus(): Promise<void>; refresh(): Promise<void>; loadUnits(): Promise<void>
  start(active: Ref<boolean>): void                       // polling lifecycle
  cut(units: string[]): Promise<string | null>
  saveChangelog(id: string, head: string, unit: string, section: string): Promise<boolean>
  resetChangelog(id: string, head: string, unit: string): Promise<boolean>
  act(id: string, action: 'approve-changelog' | 'retry-sync' | 'approve-qa' | 'recut' | 'release' | 'retry-release' | 'abandon', head: string, confirmFailed?: boolean): Promise<boolean>
  loadHistory(page: number): Promise<HistoryRow[]>
}
```

**Steps:**

- [ ] **Step 1: Failing tests** (`vi.stubGlobal('fetch', …)`, fake timers, as in `useUpgrades.test.ts`):

```ts
it('polls every 15s only while active', async () => { /* active=true → 1 call; advance 15s → 2; active=false; advance 30s → still 2 */ })
it('act refreshes and reports 409', async () => { /* fetch POST → 409; expect notice 'This release changed. The view has been refreshed.' and a GET /api/release/releases afterwards */ })
it('cut posts the unit list', async () => { /* body {"units":["ppg:staging:18"]}; returns id from 201 */ })
// AppHeader.test.ts
it('shows Release only when signed in and enabled', …)
```

- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement.** In `App.vue` render `<ReleasePanel v-else-if="mainTab === 'release' && signedIn" />`, call `loadStatus()` once auth is loaded and signed in, and `start(computed(() => mainTab.value === 'release'))`.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): release tab plumbing"`

```json:metadata
{"files": ["frontend/src/types/release.ts", "frontend/src/composables/useRelease.ts", "frontend/src/composables/useRelease.test.ts", "frontend/src/components/release/ReleasePanel.vue", "frontend/src/composables/useUrlState.ts", "frontend/src/composables/useAuth.ts", "frontend/src/components/AppHeader.vue", "frontend/src/components/AppHeader.test.ts", "frontend/src/App.vue"], "verifyCommand": "cd frontend && npx vitest run src/composables/useRelease.test.ts src/components/AppHeader.test.ts && npx vue-tsc --noEmit", "acceptanceCriteria": ["release tab value and guarded header pill", "15s polling only while the tab is active and visible", "actions refresh and surface 409 as a notice", "types mirror backend JSON"], "modelTier": "standard"}
```

---

### Task 11: Landing view, unit selection, history

**Goal:** The tab's landing page: in-flight releases, the selection list with "Cut release", and the history.

**Files:**
- Modify: `frontend/src/components/release/ReleasePanel.vue`
- Create: `frontend/src/components/release/ReleaseUnitList.vue`
- Create: `frontend/src/components/release/ReleaseUnitList.test.ts`
- Create: `frontend/src/components/release/ReleaseHistory.vue`
- Create: `frontend/src/components/release/ReleaseHistory.test.ts`
- Create: `frontend/src/lib/changelogPreview.ts`
- Create: `frontend/src/lib/changelogPreview.test.ts`

**Acceptance Criteria:**
- [ ] In-flight releases listed first: unit release ids, PR number (link), started by / when ("unknown" when empty), current step label; clicking opens the wizard for that release (`ReleasePanel` holds `selectedId`; back link returns to the landing view)
- [ ] A stale snapshot shows a warning bar with the error text
- [ ] Unit rows grouped by the third `:`-segment of the unit root (`18`, `17`, …, `common`), heading "PostgreSQL <N>" or "Common"; each row: unit root, members, last release, `built / total` pill
- [ ] Non-tickable rows have a disabled checkbox and an alert with `blocked.text`; `not_built` lists `blocked.packages`; `in_flight` has a button that opens that release
- [ ] "Cut release" is disabled with nothing ticked or while `busy`; on success the wizard for the returned id opens
- [ ] A units load error is shown as an error box, not as an empty list
- [ ] History: newest first, 20 per page with "Load more"; row shows release id, unit, date, links "GitHub release" and "PR" (PR link omitted when empty), released-by and a "released with failed QA" badge when present; expanding shows the notes via `changelogPreview`
- [ ] `changelogPreview` renders without `v-html`

**Verify:** `cd frontend && npx vitest run src/components/release src/lib/changelogPreview.test.ts && npx vue-tsc --noEmit` → PASS

**Interfaces:**

```ts
// lib/changelogPreview.ts
export type PreviewNode = { kind: 'h2' | 'h3' | 'li' | 'p'; text: string; links: { text: string; href: string }[] }
export function parseChangelog(md: string): PreviewNode[]
// "## x" → h2, "### x" → h3, "- x"/"* x" → li, other non-blank lines → p.
// Bare http(s) URLs in the text become links; anything else is plain text.
```

`ReleaseUnitList` props `{ units: UnitRow[]; busy: boolean; error: string }`, emits `cut: [units: string[]]`, `open: [releaseId: string]`. `ReleaseHistory` takes no props and uses `useRelease().loadHistory`.

**Steps:**

- [ ] **Step 1: Failing tests**

```ts
// changelogPreview.test.ts
expect(parseChangelog('## [18.6-6] - 2026-11-11\n\n### Changed\n- a (https://x.y/z)\n<script>')).toEqual([
  { kind: 'h2', text: '[18.6-6] - 2026-11-11', links: [] },
  { kind: 'h3', text: 'Changed', links: [] },
  { kind: 'li', text: 'a (https://x.y/z)', links: [{ text: 'https://x.y/z', href: 'https://x.y/z' }] },
  { kind: 'p', text: '<script>', links: [] },
])
// "javascript:alert(1)" is never a link
// ReleaseUnitList.test.ts
it('disables blocked units and shows the reason')     // blocked row: checkbox disabled, alert text present
it('emits cut with ticked units only')
it('groups by major and common')                      // headings "PostgreSQL 18", "Common"
it('shows an error box instead of an empty list')
// ReleaseHistory.test.ts
it('renders rows with release and PR links, omits a missing PR link')
```

- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement**, following `release-select.html` for the rows and alert colours (red `not_built`, amber `in_flight`, grey `unchanged`).
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): landing view, unit selection and history"`

```json:metadata
{"files": ["frontend/src/components/release/ReleasePanel.vue", "frontend/src/components/release/ReleaseUnitList.vue", "frontend/src/components/release/ReleaseUnitList.test.ts", "frontend/src/components/release/ReleaseHistory.vue", "frontend/src/components/release/ReleaseHistory.test.ts", "frontend/src/lib/changelogPreview.ts", "frontend/src/lib/changelogPreview.test.ts"], "verifyCommand": "cd frontend && npx vitest run src/components/release src/lib/changelogPreview.test.ts && npx vue-tsc --noEmit", "acceptanceCriteria": ["in-flight list with step and starter", "grouped unit rows with disabled blocked rows and reasons", "cut button rules", "history rows with links and expandable notes", "markdown preview without v-html"], "modelTier": "standard"}
```

---

### Task 12: Wizard shell and changelog step

**Goal:** The wizard frame (release strip, step bar, abandon, re-cut) and step 2 with the editor.

**Files:**
- Create: `frontend/src/components/release/ReleaseWizard.vue`
- Create: `frontend/src/components/release/ReleaseWizard.test.ts`
- Create: `frontend/src/components/release/ReleaseChangelogStep.vue`
- Create: `frontend/src/components/release/ReleaseChangelogStep.test.ts`
- Modify: `frontend/src/components/release/ReleasePanel.vue`

**Acceptance Criteria:**
- [ ] Release strip: unit ids, PR link (when a PR exists), "started by … · <relative time>", "Abandon release" (hidden once `step === 'releasing'`); abandon asks for confirmation in an in-page dialog (no `window.confirm`)
- [ ] Step bar with five steps; `cutting` and `changelog` both highlight "2 · Changelog"; earlier steps marked done
- [ ] `step === 'cutting'`: progress list "Release branch created / Generating release files and changelogs / Opening the release PR" driven by `cut.state` and `pr`; a failed cut shows "The cut failed", a link to the run and a "Retry" button (`recut`)
- [ ] `step === 'changelog'`: one tab per unit (label = release id), editor (`<textarea>`) and `changelogPreview` side by side, stacked below 768 px
- [ ] An unsaved-edits dot on a unit tab; "Save" enabled only with unsaved edits; "Reset to generated text" enabled only when `generated !== null`
- [ ] "Approve changelogs & start sync" is disabled while any unit has unsaved edits
- [ ] When a refresh brings a new `head_sha`, a unit without unsaved edits takes the new text; a unit with unsaved edits keeps them and shows "The release changed on GitHub. Saving will fail; copy your text and reload." 
- [ ] For `step` beyond `changelog` the editor is not rendered; the changelog is shown read-only in a collapsed section
- [ ] `dashboard === false` (fork PR): all buttons hidden, a note explains why
- [ ] "Update to latest main & restart" appears in steps `sync` and `qa` only for ids not starting with `pr-`, with a confirmation dialog stating that changelog edits will be regenerated

**Verify:** `cd frontend && npx vitest run src/components/release && npx vue-tsc --noEmit` → PASS

**Interfaces:** `ReleaseWizard` props `{ release: ReleaseState }`, emits `back`. `ReleaseChangelogStep` props `{ release: ReleaseState }`. Both call `useRelease()` for actions. `ReleaseWizard` renders `ReleaseRunCards` / `ReleaseQaStep` (Task 13) for later steps; until Task 13 lands, render nothing for them.

**Steps:**

- [ ] **Step 1: Failing tests**

```ts
// ReleaseChangelogStep.test.ts
it('save sends head, unit and the edited section')           // saveChangelog('20261111T143210', 'abc', 'ppg:staging:18', '<edited>')
it('approve is disabled with unsaved edits')
it('reset is disabled when generated is null')
it('keeps unsaved edits and warns when head_sha changes')
it('takes the new text when there are no unsaved edits')
// ReleaseWizard.test.ts
it('shows the cut progress and a retry on a failed cut')
it('hides abandon while releasing')
it('hides every action for a fork PR')
it('abandon needs confirmation')                             // act not called until the dialog is confirmed
```

- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement**, following `release-changelog.html`.
- [ ] **Step 4: Run** → PASS
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): wizard shell and changelog step"`

```json:metadata
{"files": ["frontend/src/components/release/ReleaseWizard.vue", "frontend/src/components/release/ReleaseWizard.test.ts", "frontend/src/components/release/ReleaseChangelogStep.vue", "frontend/src/components/release/ReleaseChangelogStep.test.ts", "frontend/src/components/release/ReleasePanel.vue"], "verifyCommand": "cd frontend && npx vitest run src/components/release && npx vue-tsc --noEmit", "acceptanceCriteria": ["release strip, step bar, abandon with confirmation", "cut progress and failed-cut retry", "per-unit editor with save/reset/approve rules", "unsaved edits survive a head change with a warning", "fork PRs are read-only", "re-cut button with confirmation"], "modelTier": "standard"}
```

---

### Task 13: Sync, QA and release steps

**Goal:** Steps 3–5: instance cards, removals, QA lanes with Jenkins links, release with confirmation, final sync.

**Files:**
- Create: `frontend/src/components/release/ReleaseRunCards.vue`
- Create: `frontend/src/components/release/ReleaseRunCards.test.ts`
- Create: `frontend/src/components/release/ReleaseQaStep.vue`
- Create: `frontend/src/components/release/ReleaseQaStep.test.ts`
- Modify: `frontend/src/components/release/ReleaseWizard.vue`

**Acceptance Criteria:**
- [ ] `ReleaseRunCards` (used for step 3 and for the final sync): one card per instance with state pill, elapsed time (`started_at` → `finished_at` or now), "View log on GitHub" link; a failed card lists `failed_units` and `errors`
- [ ] Step 3: with `sync_state === 'queued'` and no cards, "Waiting for the sync to start"; on failure, "Retry sync" and the re-cut button; removals listed once sync succeeded ("Releasing will remove N items from …"), or "Nothing will be removed"
- [ ] Step 4 `waiting`: removals again and "Approve & run QA"
- [ ] Step 4 `running`/`finished`: passed / failed / running counts; lanes grouped by unit (release id as heading; lanes with empty `unit` under "Other"); failed lanes first, each with link text "Jenkins job ↗" to `jenkins_url`, or "GitHub job ↗" to `url` when `jenkins_url` is empty; passed lanes collapsed behind "N passed lanes · show"
- [ ] "Release" is disabled until QA is `finished`. With failed lanes, clicking opens a dialog listing them with the button "Release anyway"; confirming calls `act(id, 'release', head, true)`. Without failed lanes it calls `act(id, 'release', head, false)` after a plain confirmation
- [ ] Step 5: "PR #N merged", tags, `ReleaseRunCards` for `final.instances`, then "Publish the GitHub releases" and "Delete the pre-release" with their states; on `final.state === 'failure'` a "Retry final sync" button

**Verify:** `cd frontend && npx vitest run src/components/release && npx vue-tsc --noEmit` → PASS

**Interfaces:** `ReleaseRunCards` props `{ cards: InstanceCard[] }`. `ReleaseQaStep` props `{ release: ReleaseState }`.

**Steps:**

- [ ] **Step 1: Failing tests**

```ts
// ReleaseRunCards.test.ts
it('shows failed units and errors on a failed card')
// ReleaseQaStep.test.ts
it('links failed lanes to Jenkins and falls back to the GitHub job')
it('groups lanes by unit with failed first and passed collapsed')
it('release is disabled while QA is running')
it('release with failed lanes needs "Release anyway" and sends confirm_failed')   // act called with (id, 'release', head, true) only after the dialog button
it('release without failures sends confirm_failed false')
it('approve QA is offered only while waiting')
```

Add to `ReleaseWizard.test.ts`: `it('offers Retry final sync when the final sync failed')`.

- [ ] **Step 2: Run** → FAIL
- [ ] **Step 3: Implement**, following `release-sync.html` (option A) and `release-qa-and-final.html`.
- [ ] **Step 4: Run** `npx vitest run && npx vue-tsc --noEmit && npm run build` → PASS
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): sync, QA and release steps"`

```json:metadata
{"files": ["frontend/src/components/release/ReleaseRunCards.vue", "frontend/src/components/release/ReleaseRunCards.test.ts", "frontend/src/components/release/ReleaseQaStep.vue", "frontend/src/components/release/ReleaseQaStep.test.ts", "frontend/src/components/release/ReleaseWizard.vue"], "verifyCommand": "cd frontend && npx vitest run && npx vue-tsc --noEmit && npm run build", "acceptanceCriteria": ["instance cards with state, elapsed time, log link, failure details", "removals shown before QA approval", "lanes grouped by unit with Jenkins links and GitHub fallback", "release confirmation, with Release anyway for failed lanes", "final sync view with retry"], "modelTier": "standard"}
```

---

### Task 14: Documentation

**Goal:** Operators know how to enable the feature and what it depends on.

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`

**Acceptance Criteria:**
- [ ] README has a "Release tab" section: what it does, `RELEASE_GITHUB_TOKEN` (classic token, scopes `repo` and `workflow`, acts as the token's owner), `RELEASE_GITHUB_REPO`, that it requires the obs-packaging changes of spec § 6, and that actor identity is kept in the dashboard only
- [ ] CHANGELOG has an Unreleased "Added" entry in the file's existing format

**Verify:** `grep -n "RELEASE_GITHUB_TOKEN" README.md && grep -n -i "release tab" CHANGELOG.md` → both match

**Steps:**

- [ ] **Step 1:** Write both entries, matching each file's existing heading style.
- [ ] **Step 2:** Run the verify command.
- [ ] **Step 3: Commit** `git commit -s -m "docs: release tab"`

```json:metadata
{"files": ["README.md", "CHANGELOG.md"], "verifyCommand": "grep -n \"RELEASE_GITHUB_TOKEN\" README.md && grep -n -i \"release tab\" CHANGELOG.md", "acceptanceCriteria": ["README documents the token, scopes, repo setting and dependency on obs-packaging", "CHANGELOG has an Added entry"], "modelTier": "mechanical"}
```

---

## Dependencies

Task 2 → 1. Task 4 → 1, 2. Task 6 → 1. Task 7 → 2, 3, 4, 5, 6. Task 8 → 7. Task 9 → 8. Task 10 → 9 (types only; can start once Task 4/6/7 interfaces are fixed). Task 11 → 10. Task 12 → 11. Task 13 → 12. Task 14 → 9.

## Not in this plan

- The obs-packaging changes (spec § 6): cut workflow, sync gate, Jenkins annotation, run name, unit list file. Until they land, the tab can be exercised only against the stubbed GitHub in tests.
- The labs rehearsal (spec § 8 step 3).
