# Release Unit Freeze Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A "Build freeze" card in the Releases tab that shows which release units are frozen on OBS, freezes and unfreezes them through the `obs-freeze.yml` workflow, and a warning on the cut list for a ticked unit that is not frozen.

**Architecture:** The freeze state is read from OBS project `_meta` per instance (`obs.Fleet`), combined in `release.Service` with the recent `obs-freeze.yml` runs (running / failed) and the `release_audit` log (who, when), and served by one new GET and one new POST route. The frontend polls the GET with the existing 15 s loop and renders a new card, a picker dialog, and a pill plus alert strip in the existing unit list.

**Tech Stack:** Go (chi, database/sql, net/http/httptest), Vue 3 `<script setup>` + TypeScript, Tailwind classes already in use, vitest + @vue/test-utils.

**Spec:** `docs/superpowers/specs/2026-11-26-release-unit-freeze-panel-design.md`

## Global Constraints

- Commits: `git commit -s`, never a `Co-Authored-By:` trailer (repo `CLAUDE.md`). Work on a branch, not `main`.
- Contract with obs-packaging, exact strings:
  - workflow file `obs-freeze.yml`; inputs `units` (unit roots joined by one space) and `action` (`freeze` | `unfreeze`)
  - run name `<action> <units>`; job name `<action> units (<instance>)`
  - marker: `rebuild="local"` on a `<repository>` of the project `_meta`
  - projects of a unit: `<root>`, `<root>:containers`, `<root>:tarballs` (logical names; `Instance.ToInstance` maps them)
- State words in Go and JSON: instance state `frozen` | `partial` | `unfrozen` | `unknown`; unit state `frozen` | `partial` | `unfrozen` | `""` (no state known).
- JSON is snake_case like the rest of `/api/release/*`; nil slices are sent as `[]`.
- The state on screen always comes from OBS. Nothing stores "frozen" in the database.
- No Freeze button outside the Build freeze card. The cut is never blocked by freeze state.
- UI strings quoted in the spec are used verbatim.
- New Vue components follow the existing ones: `data-testid` hooks, the same Tailwind tokens (`border-border`, `bg-bg-card`, `text-brand-purple`, `bg-warn-tint`, …), no new CSS file.

**User decisions (already made):**
- Layout A: its own "Build freeze" card between *In flight* and *New release*, picker in a dialog.
- "I don't think the UI should suggest to freeze a unit, but in the cut release panel we can have a UI alert in case the release unit is not frozen."
- Alert placement: option 1, on the row, when the unit is ticked.
- Freeze state source: option 1, read from OBS on every instance.

## Review Focus

1. **Unit names where one is the start of another** (`ppg:staging:17` and `ppg:staging:17:extras`) in a run name: a run of the second must not mark the first. → Task 2, `TestParseFreezeRunName`, `TestFreezeRunsByUnit_NoPrefixMatch`.
2. **One OBS instance is down:** its state is `unknown`, the other instances still decide the unit state, the endpoint still answers 200. → Task 1 `TestUnitFreeze_InstanceDown`, Task 3 `TestFreezeStates_UnknownInstanceIgnored`.
3. **Freeze clicked twice, or while a run for that unit is in progress:** the second request is a 409 and dispatches nothing. → Task 3 `TestSetFreeze_RefusesUnitWithRunInProgress`, `TestSetFreeze_RefusesRightAfterDispatch`.
4. **A freeze run started by hand on GitHub** (no audit row; possibly naming a unit that is not in the units file): shown as in progress with no name; the unknown unit is ignored. → Task 3 `TestFreezeStates_RunWithoutAudit`.
5. **The freeze endpoint fails in the browser:** the card shows an error, the cut list shows no pill and no strip, Cut release still works. → Task 5 `loadFreeze keeps cut usable on failure`, Task 7 `shows no pill or strip while freeze is null`.

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/obs/freeze.go` (new) | Parse the freeze marker from `_meta`; `Fleet.UnitFreeze`: per-instance state of one unit |
| `backend/internal/release/contract.go` | The `obs-freeze.yml` names and run-name build/parse |
| `backend/internal/release/freeze.go` (new) | Types, pure derivation, `Service.FreezeStates`, `Service.SetFreeze`, caches |
| `backend/internal/release/service.go` | `Options.Freeze`, the service fields |
| `backend/internal/api/release_routes.go` | `GET` / `POST /api/release/freeze` |
| `backend/cmd/obsboard/main.go` | Adapter from `*obs.Fleet` to `release.FreezeSource` |
| `frontend/src/types/release.ts` | `UnitFreeze`, `FreezeAction` |
| `frontend/src/composables/useRelease.ts` | `freeze` state, polling, `setFreeze` |
| `frontend/src/lib/unitGroups.ts` (new) | Group units by PostgreSQL major (moved out of `ReleaseUnitList.vue`) |
| `frontend/src/components/release/FreezeCard.vue` (new) | The card |
| `frontend/src/components/release/FreezePicker.vue` (new) | The dialog |
| `frontend/src/components/release/ReleaseUnitList.vue` | Pill and alert strip |
| `frontend/src/components/release/ReleasePanel.vue` | Mount the card, pass freeze to the list |

---

### Task 1: Read the freeze marker from OBS

**Goal:** `Fleet.UnitFreeze` returns, for one unit root, its freeze state on each OBS instance that hosts it.

**Files:**
- Create: `backend/internal/obs/freeze.go`
- Test: `backend/internal/obs/freeze_test.go`

**Acceptance Criteria:**
- [ ] `parseFreezeState` returns `frozen` / `partial` / `unfrozen` / `empty` from a `_meta` document exactly as `freeze_state` in obs-packaging `percona_obs/unit_freeze.py` does.
- [ ] A project that answers 404 on an instance, or is `empty`, does not vote; an instance with no voting project is left out of the result.
- [ ] Any other read error on an instance gives that instance `unknown`.
- [ ] Results are in fleet instance order and labelled with `Instance.Name`.

**Verify:** `cd backend && go test ./internal/obs -run 'Freeze' -v` → PASS

**Interfaces (used by Task 4):**

```go
type InstanceFreeze struct {
	Instance string // Instance.Name
	State    string // "frozen" | "partial" | "unfrozen" | "unknown"
}
func (f *Fleet) UnitFreeze(ctx context.Context, unit string) []InstanceFreeze
```

**Steps:**

- [ ] **Step 1: Write the failing tests** in `freeze_test.go`, using `newFakeOBS` / `fleetOf` from `fleet_test.go` (instance `x` has root `isv:percona`, `y` has root `percona`; fake paths are `/source/<project>/_meta`).

```go
const metaFrozen = `<project name="p"><repository name="a" rebuild="local"/><repository name="b" rebuild="local"/></project>`
const metaPartial = `<project name="p"><repository name="a" rebuild="local"/><repository name="b"/></project>`
const metaUnfrozen = `<project name="p"><repository name="a"/><repository name="b" rebuild="transitive"/></project>`
const metaEmpty = `<project name="p"><title>t</title></project>`

func TestParseFreezeState(t *testing.T) {
	for meta, want := range map[string]string{metaFrozen: "frozen", metaPartial: "partial", metaUnfrozen: "unfrozen", metaEmpty: "empty"} {
		got, err := parseFreezeState([]byte(meta))
		if err != nil || got != want { t.Errorf("%s: got %q, %v; want %q", meta, got, err, want) }
	}
	if _, err := parseFreezeState([]byte("<not xml")); err == nil { t.Error("want an error for bad XML") }
}

// x: root frozen, containers frozen, tarballs absent → frozen.
// y: root frozen, containers unfrozen → partial.
func TestUnitFreeze_PerInstance(t *testing.T) { /* want [{x frozen} {y partial}] */ }

// x hosts nothing of the unit (all 404); y: root unfrozen, tarballs empty → [{y unfrozen}].
func TestUnitFreeze_AbsentAndEmptyDoNotVote(t *testing.T) {}

// x.fail.Store(true); y frozen → [{x unknown} {y frozen}].
func TestUnitFreeze_InstanceDown(t *testing.T) {}
```

- [ ] **Step 2: Run** `cd backend && go test ./internal/obs -run 'Freeze' -v` → FAIL (undefined).

- [ ] **Step 3: Implement** in `freeze.go`:
  - `func parseFreezeState(metaXML []byte) (string, error)` with `encoding/xml`: count `<repository>` children of the root and those with attribute `rebuild="local"`.
  - `func (c *Client) ProjectFreezeState(ctx context.Context, project string) (string, error)`: `c.get(ctx, "freeze_state", "/source/"+url.PathEscape(project)+"/_meta")`, body limited to `1<<20` like `ProjectPublishFlags`. No cache here.
  - `func (f *Fleet) UnitFreeze(ctx, unit)`: `fanOut` over `f.Instances()`; per instance read `unit`, `unit+":containers"`, `unit+":tarballs"` through `in.ToInstance`; skip `IsNotFound` and `empty`; any other error → `unknown` for the instance; otherwise all voters `frozen` → `frozen`, all `unfrozen` → `unfrozen`, else `partial`. Do not use `settle` (it merges instances).

- [ ] **Step 4: Run** the Verify command → PASS. Also `go vet ./internal/obs`.

- [ ] **Step 5: Commit** `git commit -s -m "feat(obs): read the freeze state of a release unit per instance"`

---

### Task 2: Freeze contract and pure derivation

**Goal:** The workflow names and the functions that turn run names, runs and instance states into per-unit facts, with no I/O.

**Files:**
- Modify: `backend/internal/release/contract.go`
- Create: `backend/internal/release/freeze.go`
- Test: `backend/internal/release/freeze_test.go`, `backend/internal/release/contract_test.go`

**Acceptance Criteria:**
- [ ] `ParseFreezeRunName(FreezeRunName(a, units))` returns `a` and `units` for both actions; anything else returns `ok == false`.
- [ ] A run is matched to a unit only by a whole space-separated token.
- [ ] Unit state is computed over instances whose state is not `unknown`.

**Verify:** `cd backend && go test ./internal/release -run 'Freeze' -v` → PASS

**Interfaces (used by Tasks 3, 4):**

```go
// contract.go
const (
	WorkflowFreeze = "obs-freeze.yml"
	ActionFreeze   = "freeze"
	ActionUnfreeze = "unfreeze"
)
func FreezeRunName(action string, units []string) string            // "<action> <u1> <u2>"
func ParseFreezeRunName(name string) (action string, units []string, ok bool)
func FreezeJobPrefix(action string) string                          // "<action> units ("  — use with InstanceOf

// freeze.go
type InstanceFreeze struct {
	Instance string `json:"instance"`
	State    string `json:"state"`
}
type FreezeSource interface {
	UnitFreeze(ctx context.Context, unit string) []InstanceFreeze
}
type FreezeRun struct {
	Action string `json:"action"`
	URL    string `json:"url"`
	By     string `json:"by"`
}
type FreezeFailure struct {
	Action    string   `json:"action"`
	Instances []string `json:"instances"`
	URL       string   `json:"url"`
}
type UnitFreeze struct {
	Unit      string           `json:"unit"`
	State     string           `json:"state"`
	Instances []InstanceFreeze `json:"instances"`
	FrozenBy  string           `json:"frozen_by"`
	FrozenAt  *time.Time       `json:"frozen_at"`
	Pending   *FreezeRun       `json:"pending"`
	Failed    *FreezeFailure   `json:"failed"`
}
func unitFreezeState(inst []InstanceFreeze) string
// latest: for each unit, the newest run (by CreatedAt) whose name names it.
func latestFreezeRuns(runs []Run) map[string]Run
func runInProgress(r Run) bool // Status != "completed"
```

**Steps:**

- [ ] **Step 1: Write the failing tests.**

```go
func TestParseFreezeRunName(t *testing.T) {
	a, u, ok := ParseFreezeRunName("freeze ppg:staging:18 ppg:staging:17:extras")
	// want "freeze", ["ppg:staging:18","ppg:staging:17:extras"], true
	a, u, ok = ParseFreezeRunName(FreezeRunName(ActionUnfreeze, []string{"ppg:staging:16"})) // "unfreeze", ["ppg:staging:16"], true
	for _, bad := range []string{"", "freeze", "thaw ppg:staging:18", "Cut release/20261126T100000"} {
		if _, _, ok := ParseFreezeRunName(bad); ok { t.Errorf("%q parsed", bad) }
	}
}

func TestFreezeRunsByUnit_NoPrefixMatch(t *testing.T) {
	runs := []Run{{ID: 1, Name: "freeze ppg:staging:17:extras", CreatedAt: mustTime()}}
	got := latestFreezeRuns(runs)
	// got has "ppg:staging:17:extras" and does NOT have "ppg:staging:17"
}

func TestLatestFreezeRuns_NewestWins(t *testing.T) {
	// run 1 "freeze A B" at t, run 2 "unfreeze B" at t+1h → A:1, B:2
}

func TestUnitFreezeState(t *testing.T) {
	// [] → "" ; [frozen,frozen] → frozen ; [unfrozen] → unfrozen ; [frozen,unfrozen] → partial ;
	// [partial] → partial ; [frozen,unknown] → frozen ; [unknown] → ""
}
```

- [ ] **Step 2: Run** the Verify command → FAIL.
- [ ] **Step 3: Implement** the Interfaces block. `ParseFreezeRunName` uses `strings.Fields`; `ok` needs a known action and at least one unit.
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): freeze workflow contract and state derivation"`

---

### Task 3: Freeze state and actions in the release service

**Goal:** `Service.FreezeStates` and `Service.SetFreeze`.

**Files:**
- Modify: `backend/internal/release/freeze.go`, `backend/internal/release/service.go` (`Options`, `Service` fields, `NewService`)
- Test: `backend/internal/release/freeze_service_test.go`

**Acceptance Criteria:**
- [ ] `FreezeStates` returns one `UnitFreeze` per unit of the units file, in file order, with `Instances` never nil.
- [ ] `Pending` is set for a unit whose latest `obs-freeze.yml` run is in progress, and for a unit dispatched less than `recentCutTTL` ago whose run GitHub does not list yet.
- [ ] `Failed` is set for a unit whose latest run completed with a conclusion other than `success`; `Instances` are the failed jobs' instances (`InstanceOf(job.Name, FreezeJobPrefix(action))`, conclusion not `success`/`skipped`).
- [ ] `FrozenBy` / `FrozenAt` come from the newest audit row (`freeze` or `unfreeze`) naming the unit, only when that row is a `freeze` and the unit state is `frozen` or `partial`.
- [ ] `Pending.By` is the email of that newest row when its action equals the pending action and its time is within `recentCutTTL` of the run's `CreatedAt` (or it is the remembered dispatch); otherwise `""`.
- [ ] OBS reads for a unit are reused for `freezeMetaTTL` (1 minute) and dropped by `SetFreeze` and when a run seen in progress is no longer in progress.
- [ ] `SetFreeze` refuses (and dispatches nothing): empty list or unknown action or unknown unit or duplicate → `ErrBadRequest`; a unit with `Pending` → `ErrConflict`.
- [ ] `SetFreeze` dispatches `WorkflowFreeze` with `{"units": "<joined by space>", "action": "<action>"}` and writes one audit row: action `freeze`/`unfreeze`, branch `""`, PR `0`, detail the joined units.
- [ ] With `Options.Freeze == nil` every unit has no instances and state `""`.

**Verify:** `cd backend && go test ./internal/release -run 'Freeze' -v` → PASS, then `go test ./internal/release` → PASS

**Interfaces (used by Task 4):**

```go
// service.go: Options gains
Freeze FreezeSource // nil: no OBS freeze state

// freeze.go
const (
	auditFreeze   = ActionFreeze
	auditUnfreeze = ActionUnfreeze
	freezeMetaTTL = time.Minute
)
func (s *Service) FreezeStates(ctx context.Context) ([]UnitFreeze, error)
func (s *Service) SetFreeze(ctx context.Context, email, action string, units []string) error
```

**Steps:**

- [ ] **Step 1: Write the failing tests** in `freeze_service_test.go`. Use `newFixture` (its `unitsJSON` defines the units; read `service_test.go` for their names and reuse them as `unitA`, `unitB`). Add a fake source:

```go
type fakeFreeze struct {
	mu    sync.Mutex
	state map[string][]InstanceFreeze
	calls int
}
func (f *fakeFreeze) UnitFreeze(_ context.Context, unit string) []InstanceFreeze { /* count, return state[unit] */ }
```

and a fixture variant that passes it as `Options.Freeze`. Tests, each asserting on the `UnitFreeze` of `unitA` unless said otherwise:

| Test | Setup | Assert |
|---|---|---|
| `TestFreezeStates_AllUnitsInFileOrder` | no state | `len == number of units`, order of the file, `Instances != nil`, `State == ""` |
| `TestFreezeStates_FrozenByFromAudit` | state `[{labs frozen}]`; `SetFreeze(email "maria@x", freeze, unitA)`; drop the pending by adding a completed successful run named `FreezeRunName(...)` created at dispatch time | `State == "frozen"`, `FrozenBy == "maria@x"`, `FrozenAt` = dispatch time |
| `TestFreezeStates_FrozenByHiddenAfterUnfreeze` | freeze by maria, then (clock +10 min, completed run) unfreeze by rui, state still frozen | `FrozenBy == ""` |
| `TestFreezeStates_UnknownInstanceIgnored` | state `[{labs unknown} {obs frozen}]` | `State == "frozen"`, both instances returned |
| `TestFreezeStates_PendingRun` | run `freeze unitA`, `Status: "in_progress"`, `HTMLURL: "https://github.com/run/7"` | `Pending == {freeze, that URL, ""}` |
| `TestFreezeStates_RunWithoutAudit` | in-progress run `freeze unitA not:a:unit`, no audit rows | unitA pending with `By == ""`; no entry for `not:a:unit`; no error |
| `TestFreezeStates_FailedRunNamesInstances` | completed run `freeze unitA unitB`, conclusion `failure`; jobs `freeze units (labs)` success, `freeze units (obs)` failure | both units: `Failed == {freeze, ["obs"], url}`, `Pending == nil` |
| `TestFreezeStates_NewerRunClearsFailure` | failed run at t, successful run at t+1h for unitA only | unitA `Failed == nil`; unitB still failed |
| `TestFreezeStates_MetaCached` | call twice within 30 s | `fakeFreeze.calls` for unitA == 1; after clock +61 s, 2 |
| `TestFreezeStates_RunFinishDropsCache` | in-progress run, call; mark run completed/success, call within 30 s | second call re-reads OBS |
| `TestSetFreeze_Dispatches` | `SetFreeze(email, freeze, [unitA, unitB])` | `gh.callsOf("Dispatch")` has one entry containing `obs-freeze.yml`, `action=freeze`, `units=<unitA> <unitB>`; one audit row with action `freeze` and that detail |
| `TestSetFreeze_RefusesRightAfterDispatch` | freeze unitA, then unfreeze unitA at once | second returns `errors.Is(err, ErrConflict)`; one Dispatch call |
| `TestSetFreeze_RefusesUnitWithRunInProgress` | in-progress run for unitA | `ErrConflict`, zero Dispatch calls |
| `TestSetFreeze_BadInput` | `nil` units; action `"thaw"`; unit `"nope"`; `[unitA, unitA]` | each `ErrBadRequest`, zero Dispatch calls |
| `TestSetFreeze_AlreadyFrozenAccepted` | state frozen, no run | `nil` error, one Dispatch call |
| `TestFreezeStates_NoSource` | fixture without `Options.Freeze` | every unit `State == ""`, `len(Instances) == 0` |

- [ ] **Step 2: Run** the Verify command → FAIL.

- [ ] **Step 3: Implement.**
  - `Service` fields under a new `freezeMu sync.Mutex`: `freezeSrc FreezeSource`; `freezeMeta map[string]freezeMetaEntry{at time.Time; inst []InstanceFreeze}`; `freezePending map[string]freezeDispatch{action, email string; at time.Time}`; `freezeRunning map[int64]bool` (ids seen in progress on the previous read).
  - `FreezeStates`: `mainUnits(ctx)` for the unit list; `s.gh.WorkflowRuns(ctx, WorkflowFreeze, runListLimit)`; `latestFreezeRuns`; `store.ReleaseAuditSince(s.db, []string{auditFreeze, auditUnfreeze}, time.Time{})` and keep the newest row per unit (split `Detail` with `strings.Fields`; skip when `s.db == nil`); OBS state per unit through the cache, units read concurrently; jobs of a failed latest run via `s.gh.Jobs`. A remembered dispatch is dropped once a run naming the unit with `CreatedAt` not before the dispatch second is listed, or after `recentCutTTL` (same rule as `pendingCutOf`).
  - `SetFreeze`: take `s.actMu` like `Cut`; validate; call `FreezeStates` for the pending check (a GitHub error is returned, nothing dispatched); `s.gh.Dispatch`; remember the dispatch truncated to the second; `s.recordRow(email, action, "", 0, joined)`; clear `freezeMeta`.

- [ ] **Step 4: Run** both Verify commands → PASS.
- [ ] **Step 5: Commit** `git commit -s -m "feat(release): freeze state and freeze/unfreeze actions"`

---

### Task 4: Routes and wiring

**Goal:** `GET /api/release/freeze`, `POST /api/release/freeze`, and the fleet connected to the service.

**Files:**
- Modify: `backend/internal/api/release_routes.go` (`ReleaseService`, `mountRelease`)
- Modify: `backend/cmd/obsboard/main.go`
- Test: `backend/internal/api/release_routes_test.go`

**Acceptance Criteria:**
- [ ] `GET /api/release/freeze` → 200 `{"units":[UnitFreeze…]}`; a nil list is `[]`; errors go through `relFail`.
- [ ] `POST /api/release/freeze` with `{"action":"freeze","units":["…"]}` → 204; it carries the `origin` middleware like the other writes; `ErrBadRequest` → 400, `ErrConflict` → 409 (as `relFail` already maps them).
- [ ] The service receives the signed-in email (`relEmail`).
- [ ] `main.go` passes `Freeze: freezeSource{fleet}`; the adapter converts `obs.InstanceFreeze` to `release.InstanceFreeze`.

**Verify:** `cd backend && go test ./internal/api -run 'Release' -v && go build ./...` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** next to the existing route tests, extending their fake `ReleaseService` with `FreezeStates` and `SetFreeze` (record the arguments):
  - `TestReleaseFreezeGet`: fake returns one unit → body has `units[0].unit`, `state`, `instances` as an array, `pending: null`.
  - `TestReleaseFreezeGetEmpty`: fake returns nil → body is `{"units":[]}`.
  - `TestReleaseFreezePost`: 204; fake saw action `freeze`, the units, and the test user's email.
  - `TestReleaseFreezePostConflict`: fake returns an error wrapping `release.ErrConflict` → 409.
  - `TestReleaseFreezePostNeedsOrigin`: same request the existing cut test uses to prove the origin check → same refusal status.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.** Add to `ReleaseService`:

```go
FreezeStates(ctx context.Context) ([]release.UnitFreeze, error)
SetFreeze(ctx context.Context, email, action string, units []string) error
```

  Register the GET before `/api/release/releases/{id}/{action}` and the POST with `r.With(origin)`. In `main.go` add, beside `packageSource`:

```go
// freezeSource adapts the fleet to release.FreezeSource.
type freezeSource struct{ f *obs.Fleet }
func (s freezeSource) UnitFreeze(ctx context.Context, unit string) []release.InstanceFreeze
```

- [ ] **Step 4: Run** the Verify command → PASS. Then `cd backend && go test ./...` → PASS.
- [ ] **Step 5: Commit** `git commit -s -m "feat(api): release unit freeze routes"`

---

### Task 5: Frontend types and data

**Goal:** The freeze state and the freeze/unfreeze action in `useRelease`.

**Files:**
- Modify: `frontend/src/types/release.ts`, `frontend/src/composables/useRelease.ts`
- Test: `frontend/src/composables/useRelease.test.ts`

**Acceptance Criteria:**
- [ ] `freeze` is `null` until the first successful load; `freezeError` is `''` on success and a message on failure; a failure keeps the last good `freeze`.
- [ ] `loadFreeze()` runs with every poll tick and on `refresh()`.
- [ ] `setFreeze(action, units)` POSTs `{action, units}` to `/api/release/freeze`, sets `busy` while it runs, reloads the freeze state after it, returns `true` on success.
- [ ] A 409 from `setFreeze` puts the server's error text in `notice` (not the "This release changed" text).
- [ ] `resetReleaseForTests` resets the new refs.

**Verify:** `cd frontend && npx vitest run src/composables/useRelease.test.ts && npx vue-tsc --noEmit` → PASS

**Interfaces (used by Tasks 6, 7):**

```ts
// types/release.ts
export type FreezeAction = 'freeze' | 'unfreeze'
export type FreezeUnitState = 'frozen' | 'partial' | 'unfrozen' | ''
export interface UnitFreeze {
  unit: string
  state: FreezeUnitState
  instances: { instance: string; state: 'frozen' | 'partial' | 'unfrozen' | 'unknown' }[]
  frozen_by: string
  frozen_at: string | null
  pending: { action: FreezeAction; url: string; by: string } | null
  failed: { action: FreezeAction; instances: string[]; url: string } | null
}

// useRelease(): adds
freeze: Ref<UnitFreeze[] | null>
freezeError: Ref<string>
loadFreeze(): Promise<void>
setFreeze(action: FreezeAction, units: string[]): Promise<boolean>
```

**Steps:**

- [ ] **Step 1: Write the failing tests**, in the style of the existing ones in `useRelease.test.ts` (they stub `fetch`):
  - `loads the freeze state`: GET `/api/release/freeze` answers `{units:[…]}` → `freeze.value` equals it, `freezeError.value === ''`.
  - `loadFreeze keeps cut usable on failure`: GET answers 500 → `freeze.value === null`, `freezeError.value !== ''`, `notice.value === ''`, `busy.value === false`.
  - `keeps the last freeze state when a reload fails`: success then 500 → `freeze.value` still the first answer.
  - `setFreeze posts and reloads`: assert the POST body `{action:'freeze', units:['ppg:staging:18']}`, a following GET of `/freeze`, return `true`.
  - `setFreeze shows the server text on 409`: POST answers 409 with the JSON/text error the other tests use for `errorText` → `notice.value` is that text; returns `false`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement.** `send` gets a last optional parameter `serverConflict = false`; when true a 409 uses `await errorText(res)`. The poll tick calls `void loadFreeze()` next to `void doRefresh()`; `setFreeze` awaits `loadFreeze()` after `send`.
- [ ] **Step 4: Run** the Verify command → PASS.
- [ ] **Step 5: Commit** `git commit -s -m "feat(ui): freeze state and actions in useRelease"`

---

### Task 6: Build freeze card and picker

**Goal:** The card and its dialog, mounted in the Releases tab.

**Files:**
- Create: `frontend/src/lib/unitGroups.ts`, `frontend/src/components/release/FreezeCard.vue`, `frontend/src/components/release/FreezePicker.vue`
- Modify: `frontend/src/components/release/ReleaseUnitList.vue` (use `groupUnits`), `frontend/src/components/release/ReleasePanel.vue`
- Test: `frontend/src/components/release/FreezeCard.test.ts`, `frontend/src/components/release/FreezePicker.test.ts`

**Acceptance Criteria:**
- [ ] `groupUnits(units)` returns the same groups, titles and order `ReleaseUnitList.vue` computes today; the list uses it and its existing tests still pass.
- [ ] The card (`data-testid="freeze-card"`) sits between the *In flight* and *New release* sections, title "Build freeze", sub-line "A frozen unit ignores dependency updates. Fixes merged to main still build.", a count pill "n frozen" (units with state `frozen` or `partial`), and a button "Freeze units…" (`data-testid="freeze-open"`).
- [ ] It lists the units with state `frozen` or `partial`, or with `pending`. None: "No frozen units."
- [ ] Row text: `frozen by <frozen_by> · <local date-time of frozen_at>` when `frozen_by` is set; then `all instances` for `frozen`, or `<frozen instance names> only — not frozen on <the others>` for `partial`; `could not read <instances>` for instances in state `unknown`.
- [ ] A pending row shows the spinner, "Freezing…" or "Unfreezing…", "started by <by>" when `by` is set, and a "workflow run" link (through `safeHref`); its buttons are disabled.
- [ ] A row with `failed` shows "The last <action> failed on <instances joined by ', '>" and a link to the run.
- [ ] "Unfreeze" (`data-testid="unfreeze"`) opens `ReleaseConfirm` with title "Unfreeze <unit>?", body "The postponed rebuilds will start.", confirm label "Unfreeze"; when the unit's `UnitRow.blocked?.reason === 'in_flight'`, `warning` is "A release of this unit is in progress." Confirm emits `unfreeze` with `[unit]`.
- [ ] "Freeze everywhere" (`data-testid="freeze-everywhere"`) shows on `partial` rows only and emits `freeze` with `[unit]`.
- [ ] `error` prop non-empty and no data: "Could not load the freeze state: <error>" in the fail style. `freeze === null` and no error: `ReleaseLoading`.
- [ ] The picker (`role="dialog"`, `data-testid="freeze-picker"`) lists all units grouped with `groupUnits`, each with a tick box, the members line and the "n / m built" pill. A unit is disabled with a reason when: state `frozen` ("already frozen"), `pending` ("a freeze is running" / "an unfreeze is running"), or state `''` ("state unknown"). The note "Unfreezing later lets the postponed rebuilds start." is shown. Footer: "n unit(s) selected", Cancel, "Freeze n unit(s)" (disabled at 0 or while `busy`). Esc and a click on the backdrop cancel.
- [ ] `ReleasePanel` calls `setFreeze`; the picker closes when it resolves `true`.

**Verify:** `cd frontend && npx vitest run src/components/release && npx vue-tsc --noEmit` → PASS

**Interfaces:**

```ts
// lib/unitGroups.ts
export interface UnitGroup { key: string; title: string; rows: UnitRow[] }
export function groupUnits(units: UnitRow[]): UnitGroup[]

// FreezeCard.vue
props: { freeze: UnitFreeze[] | null; units: UnitRow[]; error: string; busy: boolean }
emits: { freeze: [units: string[]]; unfreeze: [units: string[]] }

// FreezePicker.vue
props: { freeze: UnitFreeze[]; units: UnitRow[]; busy: boolean }
emits: { freeze: [units: string[]]; cancel: [] }
```

**Steps:**

- [ ] **Step 1: Move the grouping.** Write `groupUnits` in `unitGroups.ts` from the `major` + `groups` code in `ReleaseUnitList.vue`, use it there, run `npx vitest run src/components/release/ReleaseUnitList.test.ts` → PASS.
- [ ] **Step 2: Write the failing tests.** One fixture set: `ppg:staging:18` frozen on `labs` and `obs` by `maria@percona.com`; `ppg:staging:17:extras` partial (`labs` frozen, `obs` unfrozen); `ppg:staging:16` state `unfrozen` with `pending {action:'freeze', url:'https://github.com/r/7', by:'rui@percona.com'}`; `ppg:staging:17` unfrozen; `ppg:staging:15` state `''`.
  - `FreezeCard.test.ts`: lists exactly the first three units; count pill "2 frozen"; row 18 contains "frozen by maria@percona.com" and "all instances"; row 17:extras contains "labs only — not frozen on obs" and has `freeze-everywhere`; row 16 contains "Freezing…", "started by rui@percona.com", its `unfreeze` button is disabled; clicking `unfreeze` on 18 then `confirm-ok` emits `unfreeze` `[['ppg:staging:18']]`; with `units` marking 18 `in_flight` the confirm shows `confirm-warning`; `freeze-everywhere` emits `freeze` `[['ppg:staging:17:extras']]`; a `failed` entry renders "The last freeze failed on obs"; empty list → "No frozen units."; `freeze: null, error: 'boom'` → "Could not load the freeze state: boom"; an instance in state `unknown` → "could not read labs".
  - `FreezePicker.test.ts`: 18, 16 and 15 boxes are disabled with "already frozen", "a freeze is running", "state unknown"; ticking 17 enables the button reading "Freeze 1 unit" and clicking emits `freeze` `[['ppg:staging:17']]`; 17:extras (partial) is tickable; Cancel emits `cancel`; `busy` disables the button.
- [ ] **Step 3: Run** → FAIL.
- [ ] **Step 4: Implement** the two components and mount them in `ReleasePanel.vue` (card inside the existing `<template v-else>`, before `data-testid="new-release"`; the picker is owned by the card or the panel, whichever keeps `ReleasePanel` smaller). Reuse `ReleaseSpinner`, `ReleaseLoading`, `ReleaseConfirm`, `safeHref`.
- [ ] **Step 5: Run** the Verify command → PASS.
- [ ] **Step 6: Commit** `git commit -s -m "feat(ui): build freeze card and picker in the Releases tab"`

---

### Task 7: Freeze pill and "not frozen" alert on the cut list

**Goal:** `ReleaseUnitList` shows the freeze state and warns about a ticked unit that is not frozen.

**Files:**
- Modify: `frontend/src/components/release/ReleaseUnitList.vue`, `frontend/src/components/release/ReleasePanel.vue`
- Test: `frontend/src/components/release/ReleaseUnitList.test.ts`

**Acceptance Criteria:**
- [ ] New prop `freeze: UnitFreeze[] | null`; `ReleasePanel` passes it.
- [ ] State `frozen` → pill "❄ frozen" (`data-testid="freeze-pill"`) before the built pill; `partial` → "❄ partly frozen" in the warn style. No pill otherwise.
- [ ] A ticked unit with state `unfrozen` shows a strip (`data-testid="not-frozen"`, same markup as the blocked strip, `border-warn bg-warn-tint`): "Not frozen: a dependency update can start rebuilds while this release is prepared."
- [ ] A ticked unit with state `partial`: "Frozen on <frozen instance names joined by ', '> only: a dependency update can start rebuilds while this release is prepared."
- [ ] No strip for an unticked unit, a `frozen` unit, state `''`, a unit missing from `freeze`, or `freeze === null`.
- [ ] The strip has no button; `cut-release` is enabled exactly as before.

**Verify:** `cd frontend && npx vitest run && npx vue-tsc --noEmit` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** (extend `mk` with a `freeze` prop defaulting to `null`):
  - `shows the frozen pill`: 18 frozen → its row has `freeze-pill` with "❄ frozen".
  - `warns about a ticked unit that is not frozen`: 18 `unfrozen`; no `not-frozen` before ticking; tick → strip with the exact text; untick → gone.
  - `names the frozen instances of a partly frozen unit`: partial, `labs` frozen, `obs` unfrozen; tick → "Frozen on labs only:".
  - `shows no strip for a ticked frozen unit`.
  - `shows no pill or strip while freeze is null`: tick 18 → no `freeze-pill`, no `not-frozen`, `cut-release` enabled, click emits `cut`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** in `ReleaseUnitList.vue`; pass `:freeze="freeze"` from `ReleasePanel.vue`.
- [ ] **Step 4: Run** the Verify command → PASS. Then `cd frontend && npm run build` → succeeds.
- [ ] **Step 5: Commit** `git commit -s -m "feat(ui): show freeze state and warn about unfrozen units on the cut list"`
