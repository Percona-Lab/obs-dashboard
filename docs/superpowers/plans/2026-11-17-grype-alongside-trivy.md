# Grype alongside Trivy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The CVE scanner runs Grype after Trivy on every image and stores one merged list of findings, so RHEL 10 images stop showing as falsely clean.

**Architecture:** A new `grype.go` in `backend/internal/cve` turns Grype JSON into `model.CveFinding`s, enriched from the RPM list Trivy already returns. `scanner.go` runs Grype after Trivy under the same lock and merges the two lists, Trivy winning on duplicates. The `Dockerfile` ships the Grype binary next to Trivy.

**Tech Stack:** Go (backend, `go test`), SQLite store (findings are a JSON column, no migration), Docker multi-stage build, TypeScript types in `frontend/src/types/api.ts`.

**Spec:** `docs/superpowers/specs/2026-11-17-grype-alongside-trivy-design.md`

## Global Constraints

- Merge key is exactly `(ID, PkgName, Path)`. `Path` is empty for RPM findings and has no leading `/`.
- Scanner names in `CveFinding.Scanners` are the literal strings `"trivy"` and `"grype"`, in that order when both.
- Severities stored are upper-case `HIGH` / `CRITICAL` only. Every stored finding has a non-empty `FixedVersion`.
- Grype is invoked as: `grype registry:<imageRef> --platform linux/<platform> --only-fixed --by-cve -o json -q`.
- No UI change. No new config. No DB migration.
- Commits: `git commit -s`, no `Co-Authored-By` trailer (repo `CLAUDE.md`).
- Run Go tests from `backend/`: `go test ./internal/cve/ ./internal/model/ ./internal/store/`.

**User decisions (already made):**
- "plan running grype alongside trivy and merging by CVE ID".
- Fail-if-either-fails: if either scanner fails the scan counts as failed and the previous result stays.
- Each finding records which scanner(s) reported it; stored, not shown in the UI.
- No EPSS or other new UI.
- "looks good except pinning the trivy version. Keep trivy to latest" — the `Dockerfile` keeps `ghcr.io/aquasecurity/trivy:latest`.

## Review Focus

1. Grype reports the same CVE for the same package more than once (it does, per match detail) → stored once. Test in Task 1.
2. Grype's primary ID is a GHSA even with `--by-cve` → the first `relatedVulnerabilities` ID starting with `CVE-` is used, so it merges with Trivy's CVE. Test in Task 1.
3. A Grype RPM that is not in Trivy's package list → finding kept with `Src` from Grype's upstream and empty `Vendor`, no panic. Test in Task 1.
4. The same library CVE in two different Go binaries → two findings stay two after the merge. Test in Task 2.
5. Grype exits non-zero or prints non-JSON → `CVEScanFailed` with Grype's stderr, and the previously stored scan is untouched. Test in Task 2.

---

### Task 1: Parse Grype output into findings

**Goal:** `parseGrypeOutput` turns Grype JSON into enriched, filtered, de-duplicated `model.CveFinding`s, and findings can carry scanner names.

**Files:**
- Create: `backend/internal/cve/grype.go`
- Create: `backend/internal/cve/grype_test.go` (`package cve`, like `owner_test.go`)
- Create: `backend/internal/cve/testdata/grype.json`
- Modify: `backend/internal/model/types.go:59-72` (`CveFinding`)
- Modify: `frontend/src/types/api.ts:52-59` (`CveFinding`)

**Acceptance Criteria:**
- [ ] `model.CveFinding` has `Scanners []string` with tag `json:"scanners,omitempty"`; the TS interface has `scanners?: string[]`.
- [ ] `parseGrypeOutput` on the fixture returns exactly 4 findings with the fields in Step 2.
- [ ] Medium findings, findings with no fix version, and repeated matches are not returned.
- [ ] Every returned finding has `Scanners == []string{"grype"}`.

**Verify:** `cd backend && go test ./internal/cve/ -run 'Grype' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the fixture `backend/internal/cve/testdata/grype.json`**

It pairs with `testdata/trivy-listall.json` (RPMs `krb5-libs`/src `krb5`/"Red Hat, Inc." and `gosu`/"obs://…" exist there).

```json
{
  "matches": [
    {"vulnerability": {"id": "CVE-2026-0001", "severity": "High", "fix": {"versions": ["0:1.21.1-10.el9_9.2"], "state": "fixed"}},
     "relatedVulnerabilities": [{"id": "CVE-2026-0001"}],
     "artifact": {"name": "krb5-libs", "version": "1.21.1-10.el9_8.1", "type": "rpm", "locations": [{"path": "/var/lib/rpm/rpmdb.sqlite"}], "upstreams": [{"name": "krb5"}]}},
    {"vulnerability": {"id": "CVE-2026-0100", "severity": "High", "fix": {"versions": ["0:1.21.1-10.el9_9.3"], "state": "fixed"}},
     "relatedVulnerabilities": [],
     "artifact": {"name": "krb5-libs", "version": "1.21.1-10.el9_8.1", "type": "rpm", "locations": [{"path": "/var/lib/rpm/rpmdb.sqlite"}], "upstreams": [{"name": "krb5"}]}},
    {"vulnerability": {"id": "CVE-2026-0100", "severity": "High", "fix": {"versions": ["0:1.21.1-10.el9_9.3"], "state": "fixed"}},
     "relatedVulnerabilities": [],
     "artifact": {"name": "krb5-libs", "version": "1.21.1-10.el9_8.1", "type": "rpm", "locations": [{"path": "/var/lib/rpm/rpmdb.sqlite"}], "upstreams": [{"name": "krb5"}]}},
    {"vulnerability": {"id": "CVE-2026-0101", "severity": "Critical", "fix": {"versions": ["2:3.0-2.el10", "2:3.1-1.el10"], "state": "fixed"}},
     "relatedVulnerabilities": [],
     "artifact": {"name": "newpkg", "version": "2:3.0-1.el10", "type": "rpm", "locations": [{"path": "/var/lib/rpm/rpmdb.sqlite"}], "upstreams": [{"name": "newsrc"}]}},
    {"vulnerability": {"id": "CVE-2026-0102", "severity": "Medium", "fix": {"versions": ["0:1.3-1"], "state": "fixed"}},
     "relatedVulnerabilities": [],
     "artifact": {"name": "zlib", "version": "1.2-1", "type": "rpm", "locations": [], "upstreams": []}},
    {"vulnerability": {"id": "CVE-2026-0103", "severity": "High", "fix": {"versions": [], "state": "fixed"}},
     "relatedVulnerabilities": [],
     "artifact": {"name": "zlib", "version": "1.2-1", "type": "rpm", "locations": [], "upstreams": []}},
    {"vulnerability": {"id": "GHSA-aaaa-bbbb-cccc", "severity": "High", "fix": {"versions": ["0.55.0"], "state": "fixed"}},
     "relatedVulnerabilities": [{"id": "GHSA-aaaa-bbbb-cccc"}, {"id": "CVE-2026-0005"}],
     "artifact": {"name": "golang.org/x/net", "version": "v0.54.0", "type": "go-module", "locations": [{"path": "/usr/sbin/gosu"}], "upstreams": []}}
  ],
  "ignoredMatches": [
    {"vulnerability": {"id": "CVE-2026-0999", "severity": "High", "fix": {"versions": ["1"], "state": "fixed"}},
     "relatedVulnerabilities": [],
     "artifact": {"name": "kernel-headers", "version": "5", "type": "rpm", "locations": [], "upstreams": []}}
  ]
}
```

- [ ] **Step 2: Write the failing test `TestParseGrypeOutput` in `grype_test.go`**

Call with these RPMs (copied from the Trivy fixture):

```go
rpms := []rpmInfo{
	{Name: "krb5-libs", Version: "1.21.1", Src: "krb5", Vendor: "Red Hat, Inc."},
	{Name: "gosu", Version: "1.17", Src: "gosu", Vendor: "obs://obs.pg.labs.percona.com/isv:percona:ppg:common:deps"},
}
```

Assert `len(got) == 4`, in this order, with exactly these fields (unlisted string fields empty, `Scanners` always `["grype"]`):

| ID | PkgName | InstalledVersion | FixedVersion | Severity | Class | Type | Path | Src | Vendor | Owner |
|---|---|---|---|---|---|---|---|---|---|---|
| CVE-2026-0001 | krb5-libs | 1.21.1-10.el9_8.1 | 1.21.1-10.el9_9.2 | HIGH | os-pkgs | redhat | | krb5 | Red Hat, Inc. | |
| CVE-2026-0100 | krb5-libs | 1.21.1-10.el9_8.1 | 1.21.1-10.el9_9.3 | HIGH | os-pkgs | redhat | | krb5 | Red Hat, Inc. | |
| CVE-2026-0101 | newpkg | 2:3.0-1.el10 | 2:3.0-2.el10, 2:3.1-1.el10 | CRITICAL | os-pkgs | redhat | | newsrc | | |
| CVE-2026-0005 | golang.org/x/net | v0.54.0 | 0.55.0 | HIGH | lang-pkgs | gobinary | usr/sbin/gosu | | | gosu |

Add `TestParseGrypeOutputInvalidJSON`: `parseGrypeOutput([]byte("not json"), nil)` returns a non-nil error whose text starts with `parse grype JSON`.

- [ ] **Step 3: Run to verify it fails**

Run: `cd backend && go test ./internal/cve/ -run Grype -v`
Expected: FAIL to compile, `undefined: parseGrypeOutput`

- [ ] **Step 4: Add `Scanners` to the model and the TS type**

`model.CveFinding`: `Scanners []string \`json:"scanners,omitempty"\` // "trivy", "grype" or both`. TS: `scanners?: string[]`.

- [ ] **Step 5: Implement in `grype.go`**

```go
// parseGrypeOutput converts `grype -o json` output into findings. rpms is the
// RPM list from the Trivy scan of the same image, used for Src/Vendor/Owner.
func parseGrypeOutput(data []byte, rpms []rpmInfo) ([]model.CveFinding, error)
```

Rules, all pinned by the test:
- Read only `matches`; never `ignoredMatches`.
- Keep a match only if `severity` is `High` or `Critical` (case-insensitive) and `fix.versions` is non-empty.
- `ID`: `vulnerability.id`; if it does not start with `CVE-`, the first `relatedVulnerabilities[].id` that does; otherwise the original ID.
- `FixedVersion`: versions joined with `", "`, each with a leading `0:` removed (other epochs kept).
- Type mapping: `rpm` → `os-pkgs`/`redhat`; `go-module` → `lang-pkgs`/`gobinary`; `python` → `lang-pkgs`/`python-pkg`; anything else → `lang-pkgs`/the Grype type unchanged.
- `Path`: empty for `rpm`; otherwise `locations[0].path` with the leading `/` trimmed (empty if no locations).
- RPM enrichment: first `rpms` entry with the same `Name` gives `Src` and `Vendor`; if none, `Src = upstreams[0].name` (if any), `Vendor` empty.
- `Owner`: `goBinaryOwner(Path, rpms)` for `gobinary`, `pythonOwner(PkgName, InstalledVersion, rpms)` for `python-pkg` (both in `owner.go`).
- De-duplicate on `(ID, PkgName, Path)`, keeping the first, preserving input order.
- JSON error: `fmt.Errorf("parse grype JSON: %w", err)`.

- [ ] **Step 6: Run to verify it passes**

Run: `cd backend && go test ./internal/cve/ ./internal/model/ -v -run 'Grype|Owner|Norm'` → PASS. Then `cd frontend && npx tsc --noEmit` (or the repo's type-check script in `package.json`) → no errors.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/cve/grype.go backend/internal/cve/grype_test.go backend/internal/cve/testdata/grype.json backend/internal/model/types.go frontend/src/types/api.ts
git commit -s -m "feat(cve): parse grype output into findings"
```

```json:metadata
{"files": ["backend/internal/cve/grype.go", "backend/internal/cve/grype_test.go", "backend/internal/cve/testdata/grype.json", "backend/internal/model/types.go", "frontend/src/types/api.ts"], "verifyCommand": "cd backend && go test ./internal/cve/ -run 'Grype' -v", "acceptanceCriteria": ["CveFinding has Scanners []string json:\"scanners,omitempty\" and the TS interface has scanners?: string[]", "parseGrypeOutput on the fixture returns exactly the 4 findings in the Step 2 table", "Medium, no-fix and repeated matches are not returned", "Every returned finding has Scanners == [\"grype\"]"], "modelTier": "standard"}
```

---

### Task 2: Run Grype in the scanner and merge

**Goal:** Each scan runs Trivy then Grype and stores the merged findings; either failing fails the scan.

**Files:**
- Modify: `backend/internal/cve/scanner.go` (`scanPackage` ~line 300, `runTrivy` ~356, `parseTrivyOutput` ~412)
- Modify: `backend/internal/cve/grype.go` (add `runGrype`, `mergeFindings`)
- Modify: `backend/internal/cve/scanner_test.go` (existing fakes + new tests)
- Modify: `backend/internal/cve/grype_test.go` (merge unit test)

**Interfaces (from Task 1):** `parseGrypeOutput(data []byte, rpms []rpmInfo) ([]model.CveFinding, error)`; `model.CveFinding.Scanners`.

**Acceptance Criteria:**
- [ ] For one target the exec function is called exactly twice: `trivy` first, then `grype` with the argument list from Global Constraints.
- [ ] Scanning with `trivy-listall.json` + `grype.json` stores 10 findings: Trivy's 8 plus `CVE-2026-0100` and `CVE-2026-0101`; `CVE-2026-0001` and `CVE-2026-0005` (path `usr/sbin/gosu`) have `Scanners == ["trivy","grype"]` and keep Trivy's `Title`.
- [ ] `CriticalCount` / `HighCount` are computed from the merged list.
- [ ] Grype failure → one `CVEScanFailed` event whose `Why` starts with `grype:` and contains Grype's stderr; the previously stored scan row is unchanged.
- [ ] Trivy failure → Grype is not executed.
- [ ] All pre-existing tests in `backend/internal/cve` pass.

**Verify:** `cd backend && go test ./internal/cve/ ./internal/store/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

In `grype_test.go`, `TestMergeFindings`:

```go
trivy := []model.CveFinding{
	{ID: "CVE-1", PkgName: "a", Title: "t", Scanners: []string{"trivy"}},
	{ID: "CVE-2", PkgName: "x/net", Path: "usr/bin/one", Scanners: []string{"trivy"}},
}
grype := []model.CveFinding{
	{ID: "CVE-1", PkgName: "a", Scanners: []string{"grype"}},                        // duplicate
	{ID: "CVE-2", PkgName: "x/net", Path: "usr/bin/two", Scanners: []string{"grype"}}, // other binary
	{ID: "CVE-1", PkgName: "b", Scanners: []string{"grype"}},                        // other package
}
got := mergeFindings(trivy, grype)
// len 4; got[0] is CVE-1/a with Title "t" and Scanners ["trivy","grype"];
// got[1] unchanged with Scanners ["trivy"]; got[2], got[3] are the two grype-only ones, Scanners ["grype"].
```

In `scanner_test.go` add a helper fake and three tests:

```go
// scannerExec returns an ExecFn serving trivyOut for "trivy" and grypeOut for
// "grype", recording each call's name and args.
func scannerExec(trivyOut, grypeOut []byte, calls *[][]string) cve.ExecFn
```

- `TestScanMergesTrivyAndGrype`: fixtures `trivy-listall.json` + `grype.json` via `ScanNow`. Assert `len(calls) == 2`, `calls[0][0] == "trivy"`, `calls[1]` equals `["grype", "registry:<imageRef>", "--platform", "linux/amd64", "--only-fixed", "--by-cve", "-o", "json", "-q"]`. Assert stored findings: 10 total (Trivy's 8 + `CVE-2026-0100` + `CVE-2026-0101`); `CVE-2026-0001` has `Scanners ["trivy","grype"]` and `Title "krb5 flaw"`; `CVE-2026-0005` has `Scanners ["trivy","grype"]`; `CVE-2026-0100` has `Scanners ["grype"]`, `Src "krb5"`, `Vendor "Red Hat, Inc."`; `CVE-2026-0007` has `Scanners ["trivy"]`. Assert `CriticalCount == 3`, `HighCount == 7`.
- `TestScanFailsWhenGrypeFails`: first `ScanNow` with both succeeding (stores a row); second with a fake where `grype` returns `&exec.ExitError{Stderr: []byte("db update failed")}`-style error (construct by running `exec.Command("sh", "-c", "echo db update failed >&2; exit 1").Output()` inside the fake and returning its error). Assert the stored scan's `ScannedAt` and findings are those of the first scan, and the last event for the package has `Type == model.EventCVEScanFailed` and `Why` starting with `grype:` and containing `db update failed`.
- `TestScanSkipsGrypeWhenTrivyFails`: fake returns an error for `trivy`; assert no call named `grype`.

Update existing fakes that ignore the command name so they still mean what they say: `TestScannerParsesTrivyVulns` (its `defer close(execDone)` would run twice — close only on the `grype` call, return `{"matches":[]}` for it), `TestRunTrivyListsAllPackages` (capture `args` only when `name == "trivy"`), and any other fake in `scanner_test.go` / `rescan_test.go` that returns Trivy JSON unconditionally: return `{"matches":[]}` when `name == "grype"`.

- [ ] **Step 2: Run to verify they fail**

Run: `cd backend && go test ./internal/cve/ -v -run 'Merge|ScanMerges|ScanFails|ScanSkips'`
Expected: FAIL to compile, `undefined: mergeFindings`

- [ ] **Step 3: Split the Trivy parser**

Change `parseTrivyOutput(data []byte, imageRef, repo, arch string) (model.CveScan, error)` to also return the RPM list: `(model.CveScan, []rpmInfo, error)`. It sets `Scanners: []string{"trivy"}` on every finding. `runTrivy` returns the same triple. Counts are no longer computed there (Step 5 does it).

- [ ] **Step 4: Add to `grype.go`**

```go
// mergeFindings returns trivy's findings followed by the grype findings trivy
// lacks. Key: (ID, PkgName, Path). On a duplicate the trivy finding is kept and
// "grype" is appended to its Scanners.
func mergeFindings(trivy, grype []model.CveFinding) []model.CveFinding

// runGrype scans imageRef and returns its findings, enriched from rpms.
func (s *Scanner) runGrype(ctx context.Context, imageRef, platform string, rpms []rpmInfo) ([]model.CveFinding, error)
```

`runGrype` holds `s.trivyMu` around the exec, as `runTrivy` does. Any exec error is a failure (Grype exits 0 with findings): return `fmt.Errorf("grype: %w%s", err, stderr)` with stderr formatted exactly as `runTrivy` does for its error.

- [ ] **Step 5: Wire into `scanPackage`**

After `runTrivy` succeeds, call `runGrype`. On its error, log `cve: grype failed` and emit the same `CVEScanFailed` event the Trivy branch emits (extract the duplicated event literal into a small helper used by both branches), then `continue`. On success set `scan.Findings = mergeFindings(scan.Findings, grypeFindings)` and compute `scan.CriticalCount` / `scan.HighCount` from the merged list before `store.UpsertCveScan`.

- [ ] **Step 6: Run to verify everything passes**

Run: `cd backend && go vet ./... && go test ./...` → PASS

- [ ] **Step 7: Commit**

```bash
git add backend/internal/cve
git commit -s -m "feat(cve): run grype alongside trivy and merge findings"
```

```json:metadata
{"files": ["backend/internal/cve/scanner.go", "backend/internal/cve/grype.go", "backend/internal/cve/scanner_test.go", "backend/internal/cve/grype_test.go", "backend/internal/cve/rescan_test.go"], "verifyCommand": "cd backend && go vet ./... && go test ./...", "acceptanceCriteria": ["Per target the exec function is called exactly twice: trivy, then grype with: registry:<imageRef> --platform linux/<platform> --only-fixed --by-cve -o json -q", "Fixtures trivy-listall.json + grype.json store 10 findings, CriticalCount 3, HighCount 7; CVE-2026-0001 and CVE-2026-0005 have Scanners [trivy,grype] and Trivy's Title", "Grype failure emits CVEScanFailed with Why starting 'grype:' containing stderr, stored scan unchanged", "Trivy failure means grype is not executed", "All pre-existing tests in backend pass"], "modelTier": "standard"}
```

---

### Task 3: Ship Grype in the image and document it

**Goal:** The runtime image contains a working `grype` binary and the docs say the scanner uses both tools.

**Files:**
- Modify: `Dockerfile:18-23`
- Modify: `docs/superpowers/specs/2026-11-17-grype-alongside-trivy-design.md` (status line)
- Modify: `backend/internal/model/types.go` (comments on `Class`/`Type`/`Vendor` that say "trivy" only)

**Acceptance Criteria:**
- [ ] `Dockerfile` has `FROM ghcr.io/anchore/grype:latest AS grype` and `COPY --from=grype /grype /usr/local/bin/grype`.
- [ ] The Trivy stage is still `FROM ghcr.io/aquasecurity/trivy:latest AS trivy`.
- [ ] In the built image, `grype version` and `trivy --version` both exit 0.
- [ ] The spec's status line reads `Status: implemented.`

**Verify:** `docker build -t obsboard:grype-check . && docker run --rm --entrypoint grype obsboard:grype-check version && docker run --rm --entrypoint trivy obsboard:grype-check --version` → both print a version, exit 0

**Steps:**

- [ ] **Step 1: Edit the `Dockerfile`**

Add the Grype stage after the Trivy stage and the `COPY` after Trivy's `COPY`. Do not change the Trivy lines. The Grype image is `scratch`-based with a static binary at `/grype`; it runs on Alpine as is.

- [ ] **Step 2: Build and run the verify command**

Expected: Grype prints `Application: grype` and a version; Trivy prints `Version:`.

- [ ] **Step 3: Update comments and spec status**

In `types.go`, reword the `Class`, `Type` and `Vendor` field comments so they no longer claim the value always comes from Trivy (e.g. `// "os-pkgs" | "lang-pkgs" (trivy's result Class; mapped for grype)`). Set the spec status to `implemented`.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile backend/internal/model/types.go docs/superpowers/specs/2026-11-17-grype-alongside-trivy-design.md docs/superpowers/plans/2026-11-17-grype-alongside-trivy.md docs/superpowers/plans/2026-11-17-grype-alongside-trivy.md.tasks.json
git commit -s -m "build: ship grype in the runtime image"
```

```json:metadata
{"files": ["Dockerfile", "backend/internal/model/types.go", "docs/superpowers/specs/2026-11-17-grype-alongside-trivy-design.md"], "verifyCommand": "docker build -t obsboard:grype-check . && docker run --rm --entrypoint grype obsboard:grype-check version && docker run --rm --entrypoint trivy obsboard:grype-check --version", "acceptanceCriteria": ["Dockerfile has FROM ghcr.io/anchore/grype:latest AS grype and COPY --from=grype /grype /usr/local/bin/grype", "Trivy stage is still ghcr.io/aquasecurity/trivy:latest", "grype version and trivy --version both exit 0 in the built image", "Spec status line reads 'Status: implemented.'"], "modelTier": "mechanical"}
```

---

## After the plan

Not a task, for whoever deploys: existing stored scans gain Grype findings at the next nightly rescan (`NightlyScheduler.enqueueAll`). To confirm in production, open a ubi10 image in the CVE tab and check that krb5 findings appear.
