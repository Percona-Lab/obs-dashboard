# Grype alongside Trivy in the CVE scanner

Date: 2026-11-17. Status: implemented.

Lines marked **(call)** are choices made by the author of this document, not
decisions the user made.

## 1. Why

Trivy reports no OS vulnerabilities at all on RHEL 10 images. Measured today
on `releases/18/containers/ubi10/percona-distribution-postgresql:18`: Trivy
0.69.3 and 0.76.2 (latest) both return 0 OS findings for 485 RPMs, while Grype
0.122.0 returns 14 fixable HIGH CVEs. Red Hat's own `ubi10/ubi-minimal` shows
the same gap, so it is Trivy's data, not our image. Every ubi10 image in the
dashboard therefore shows as clean regardless of its real state.

Grype cannot simply replace Trivy: it reports nothing for Python packages that
an RPM installed (Trivy finds e.g. dnspython `CVE-2026-128089`, HIGH, in the
pgadmin4 image). On RHEL 8 and 9 the two agree almost exactly.

## 2. Decided

- Run Grype next to Trivy for every image and arch, and store one merged
  list of findings.
- Merge by CVE ID: one finding per (CVE ID, package name, path). Path is
  empty for RPMs; it keeps the same library CVE in two different Go binaries
  apart, as Trivy reports them today **(call)**. When both scanners report a
  finding, Trivy's record is kept. Grype only adds findings Trivy lacks.
- If either scanner fails, the scan fails: a `CVEScanFailed` event is emitted
  and the previous stored result stays. A half result would make the counts
  and the `cve_since` / `clean_since` periods flap.
- Each finding records which scanner(s) reported it. Stored and returned by
  the API, not shown in the UI.
- Trivy stays on `ghcr.io/aquasecurity/trivy:latest` in the `Dockerfile`. No
  pinning. A rebuild of the image picks up the current release.
- No new UI. No EPSS, risk or KEV data.

## 3. Approaches considered

1. **Both scanners on every image, merged** (chosen). One code path, covers
   both gaps, and protects against the next silent gap in either tool. Costs
   about double the scan time, since each tool pulls the image itself.
2. **Grype only for RHEL 10 images.** Cheaper, but needs OS detection before
   choosing a scanner and leaves the dashboard blind to the next Trivy gap.
3. **Replace Trivy with Grype.** Loses the Python findings and the RPM vendor
   data the CVE tab depends on.

## 4. Design

### Running Grype

In `backend/internal/cve/scanner.go`, after `runTrivy` succeeds for a target,
run:

    grype registry:<imageRef> --platform linux/<platform> --only-fixed --by-cve -o json -q

under the same `trivyMu` lock **(call)**: one scanner process at a time keeps
memory and registry load as they are today. `--by-cve` makes Grype report the
CVE ID instead of a GHSA ID when it knows one. Grype has no severity filter
flag for output, so findings other than `High` and `Critical` are dropped
while parsing. Grype's own `ignoredMatches` (the kernel-headers noise) are
never read.

Grype exits 0 when it finds vulnerabilities (no `--fail-on`), so any non-zero
exit is a failure. Its stderr goes into the event's `Why`, as for Trivy.

### Mapping a Grype match to `model.CveFinding`

| CveFinding | From Grype |
|---|---|
| `ID` | `vulnerability.id` |
| `PkgName` | `artifact.name` |
| `InstalledVersion` | `artifact.version` |
| `FixedVersion` | `vulnerability.fix.versions` joined with `, `, each with a leading `0:` epoch removed so it reads like Trivy's |
| `Severity` | `vulnerability.severity` upper-cased |
| `Title` | empty: Grype has no short title **(call)** |
| `Class` / `Type` | `rpm` → `os-pkgs` / `redhat`; `go-module` → `lang-pkgs` / `gobinary`; `python` → `lang-pkgs` / `python-pkg`; anything else → `lang-pkgs` / Grype's type as is |
| `Path` | `artifact.locations[0].path` for non-RPM artifacts, without the leading `/` so it matches Trivy's paths |

Grype's RPM metadata has no vendor, and the CVE tab needs `Src`, `Vendor` and
`Owner` to tell what the team can bump. Trivy runs with `--list-all-pkgs` and
lists every RPM with its source name and maintainer even when it matches no
vulnerabilities (485 packages on the ubi10 image). So Grype findings are
enriched from Trivy's package list:

- RPM finding: look the package up by name in Trivy's RPM list and copy `Src`
  and `Vendor` (Trivy's package `Version` has no release part, so versions are
  not compared). If it is not there, use `artifact.upstreams[0].name`
  for `Src` and leave `Vendor` empty.
- Go binary finding: `Owner = goBinaryOwner(path, rpms)`.
- Python finding: `Owner = pythonOwner(name, version, rpms)`.

These are the existing helpers in `owner.go`, unchanged.

### Merging

`parseTrivyOutput` is split so that it also returns the RPM list. A new
function merges: start from Trivy's findings; for each Grype finding, if a
Trivy finding has the same `ID`, `PkgName` and `Path`, add `grype` to its scanner
list; otherwise append the Grype finding. `CriticalCount` and `HighCount` are
computed from the merged list.

`model.CveFinding` gains `Scanners []string` (`json:"scanners,omitempty"`),
holding `trivy`, `grype` or both. Findings are stored as JSON in
`cve_scans.findings_json`, so there is no schema migration; rows written
before this change simply have no `scanners`.

### Packaging

`Dockerfile` adds a stage `FROM ghcr.io/anchore/grype:latest AS grype` and
copies `/grype` to `/usr/local/bin/grype`, next to the existing Trivy copy.
Grype follows `latest` like Trivy **(call)**, for consistency with the
decision not to pin. Grype downloads its vulnerability database on first run
into its cache directory, as Trivy does.

### Naming

Log lines, error prefixes and event text that say "trivy" for the whole scan
stay as they are where they refer to Trivy, and say "grype" for Grype
failures. The lock keeps its name **(call)**.

## 5. Testing

- A Grype JSON fixture in `backend/internal/cve/testdata/`, trimmed from a
  real ubi10 scan, next to `trivy-listall.json`.
- Unit tests in `scanner_test.go` using `WithExecFn`, which already switches
  on the command name:
  - both report the same CVE and package → one finding, both scanners listed,
    Trivy's fields kept;
  - Grype-only RPM finding → added, with `Src` and `Vendor` from Trivy's
    package list;
  - Trivy-only Python finding → kept;
  - Grype Medium finding → dropped;
  - Grype fails → `CVEScanFailed`, nothing stored;
  - Trivy fails → Grype is not run.
- Manual check after deploy: a ubi10 image shows the krb5 findings.

## 6. Left to the builder

- Exact function names and how `parseTrivyOutput` is split.
- Whether the README's scanner section needs a line about Grype.
