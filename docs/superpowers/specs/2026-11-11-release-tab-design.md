# Release tab: cutting PPG releases from the dashboard

Date: 2026-11-11. Status: approved design, not yet implemented.

## 1. Purpose

Cutting a PPG release today means running `percona-obs project release` on a
laptop, pushing a branch, opening a PR on GitHub, adding labels, reading PR
comments and check results, merging, and watching workflow runs. The Release
tab replaces all of that. A signed-in dashboard user picks what to release,
edits and approves the changelogs, watches the pre-release sync, approves and
reviews QA, and approves the release, without touching the CLI or GitHub.

The dashboard does not implement release logic. Every step is one action on
the `percona/obs-packaging` GitHub repository, whose workflows do the work.
The release process itself is described in obs-packaging
`docs/PERCONA_OBS_TOOL.md` § "Releasing packages" and its
`2026-11-10-independent-release-units-design.md` spec.

Decisions marked **(user)** were made by the user. Decisions marked
**(my call)** were made while writing this document and have not been
confirmed.

## 2. Goals, non-goals, anti-goals

Goals:

1. Run a whole release from the dashboard: select, changelog, pre-release
   sync, QA, release, final sync.
2. Show at each step whether it is waiting, running, done or failed, and why
   it failed.
3. Let any signed-in user open the tab and find every in-flight release at
   its current step.

Non-goals:

- Fixing build or QA failures. That happens outside the dashboard **(user)**.
- Phase-level sync progress (drain, freeze, copy, verify). Progress is per OBS
  instance **(user)**.
- Roles. Every allowlisted user can perform every release action, as with the
  other protected actions (authentication design D3) **(my call)**.
- Editing changelog entries of earlier releases **(my call)**.

Anti-goals:

- The GitHub token reaching the browser, a log line, or an API response.
- A second implementation of release rules (unit membership, release ids,
  changelog generation, record checks) inside the dashboard.
- Release state stored in the dashboard that can disagree with GitHub.
- A dashboard user's name or email written to the public obs-packaging
  repository (PR body, comment, commit, branch name, workflow input).

## 3. How a release maps onto GitHub

A release in flight is an open PR on obs-packaging that changes at least one
`root/*/releases/**/release.yaml`. Its state is read from GitHub; the
dashboard keeps no release state of its own **(my call, follows from the
label gating the user chose)**. Releases cut from the dashboard use a branch
named `release/<timestamp>`. A release PR opened by hand (CLI flow) is shown
too and can be driven from step 2 onwards, provided its branch is in the
repository itself; its starter is shown as unknown **(my call)**.

| Wizard step | PR state | Dashboard action that leaves this step |
|---|---|---|
| 1 Select | no PR | dispatch the cut workflow |
| 2 Changelog | cut run in progress, or PR open with no `obs-sync` label | add label `obs-sync` |
| 3 Pre-release sync | `obs-sync` present, populate run queued/running/failed | none; advances when populate succeeds |
| 4 QA | populate succeeded; `qa-release` absent (waiting) or present (running/finished) | add `qa-release`; later merge the PR |
| 5 Release | PR merged | none; ends when `obs-release.yml` succeeds |

A release may carry several units in one PR. Labels are per PR, so changelog
approval, QA approval and release approval each cover all units of the
release **(my call)**. Several releases can be in flight at once when they
cover different units (obs-packaging enforces one in-flight PR per
`release.yaml`).

## 4. The tab

A fifth `mainTab` value, `release`, shown only when signed in **(user:
authenticated users only)**. Mockups of every screen are in
`.superpowers/brainstorm/2543469-1794417318/content/` (`release-select.html`,
`release-changelog.html`, `release-sync.html` option A,
`release-qa-and-final.html`); the user approved them.

### 4.1 Landing view

In-flight releases are listed first, each with its units, PR number, who
started it and its current step; opening one shows the wizard at that step.
Below is the selection list for a new release, and below that the history
(§ 4.10) **(my call)**.

### 4.2 Step 1: select

One row per release unit, grouped by PostgreSQL major plus Common. A row
shows the unit root, its member projects, the last release id and a built
count for the whole unit (all member projects) **(user: list of eligible
staging subprojects with built status)**.

A unit is tickable only when all of these hold. Otherwise the row stays
visible, unticked, with an alert giving the reason **(user)**:

| Condition | Alert |
|---|---|
| Every package in every member project is built | names the failed/blocked/building packages, links to the Builds tab |
| No open release PR changes the unit's `release.yaml` | names the in-flight release and links to it |
| Staging differs from the unit's release project | "identical to release `<id>`" |

"Differs" means the set of package name + version-release (and container
image tags) in the unit's staging projects is not equal to the set in its
release projects **(my call)**. The dashboard already stores packages for
staging and for `ppg:releases:*` projects; if a nested unit's release
projects turn out not to be tracked, extending the tracking is part of this
work.

The mockup shows the next release id on each row. It is dropped: the id is
derived by the cut and first shown in step 2 **(my call)**.

"Cut release" dispatches the cut workflow for the ticked units.

### 4.3 Step 2: changelog

While the cut runs, a short progress list (branch created, generating,
opening PR) taken from the cut workflow run.

Once the PR exists:

- A release strip: units, PR number, who started it and when, "Abandon
  release".
- One tab per unit. Editor on the left, rendered preview on the right. The
  editor holds only the section of this release in the unit's `CHANGELOG.md`
  (from its `## [<id>]` heading to the next `## [` heading).
- "Save" amends the release commit and force-pushes the release branch
  **(user: changelog edits amend the release commit)**. The commit keeps its
  message, parent and sign-off.
- "Reset to generated text" restores the section as the last cut produced it
  **(my call)**. The source is the changelog artifact of the latest
  successful cut run for the branch (§ 6 item 1).
- "Approve changelogs & start sync" adds `obs-sync` **(user: sync starts only
  after changelog approval, gated on the `obs-sync` label)**.

The editor is read-only once `obs-sync` is on the PR **(my call)**: an amend
at that point would re-run the sync (see § 6, note on amends).

### 4.4 Step 3: pre-release sync

One card per OBS instance (the populate matrix legs of `obs-release-pr.yml`
and its CDN wait job): queued, running, done or failed, elapsed time, and a
link to the run on GitHub **(user: option A)**.

When populate is done, the packages and subprojects that the release would
delete from the release projects (the hop-2 dry-run result) are listed.

On failure: the failure text of the instance's pre-release PR comment, a
"Retry sync" button (dispatches `obs-release-pr.yml` with the PR number), and
"Update to latest main & restart" (§ 4.7) **(my call)**.

### 4.5 Step 4: QA

Before QA: the dry-run deletions again and "Approve & run QA", which adds
`qa-release` **(user)**.

The lanes are the jobs of the newest `obs-release-pr.yml` run for the PR's
current head SHA in which the QA fan-out ran; runs for older heads are
ignored. A lane belongs to the unit whose release project (or one of its
member projects) the lane's annotation names (§ 6 item 3).

During and after: passed/failed/running counts, lanes grouped by unit, failed
lanes first, each failed lane linking to its Jenkins job **(user)**; passed
lanes collapsed. A lane without a Jenkins link falls back to its GitHub job
page.

Buttons:

- "Update to latest main & restart" (§ 4.7) **(user)**.
- "Release". Enabled once every lane has finished. With failed lanes it asks
  for confirmation and lists them **(user: releasing with failures is allowed
  after confirmation)**.

### 4.6 Step 5: release

"Release" merges the PR with the rebase method (the only one the repository
allows; `obs-pr-cleanup.yml` already tags `merge_commit_sha`). One user's
decision is enough; no second reviewer **(user)**. Who approved, and any
failed lanes it was released with, go into the dashboard's audit log (§ 5.4),
not into the PR.

The view then shows: PR merged, tags created, one card per OBS instance for
the `obs-release.yml` copy legs (same detail as step 3), GitHub releases
published, pre-release deleted.

On a failed leg: "Retry final sync" re-dispatches `obs-release.yml` with the
same `pr_number` and `tags` (documented as idempotent) **(my call)**.

A merged release stays listed as in flight until its `obs-release.yml` run
succeeds; it then appears in the history (§ 4.10).

### 4.7 Update to latest main & restart (re-cut)

Removes `obs-sync` and `qa-release` first, then dispatches the cut workflow
again for the same branch and units. The dashboard does not re-check
eligibility; the cut fails by itself if a unit cannot be cut. If the cut run
fails, the PR is left as it was, without labels: step 2 shows the failed run
with its link and a "Retry" button, and the changelog editor stays disabled
until a cut succeeds **(my call)**. The workflow regenerates the release commit on
top of the latest `main` and force-pushes. The wizard returns to step 2 with
the newly generated changelog; earlier hand edits are gone and the user edits
and approves again **(user)**.

### 4.8 Abandon

Available until the PR is merged. Closes the PR and deletes the release
branch; obs-packaging's existing PR cleanup removes the pre-release projects.
The units become selectable again **(my call)**.

### 4.9 Two users at once

Every user sees the same state. Each action names the PR head SHA and step it
was issued against; if GitHub has moved on, the backend refuses with 409 and
the client refreshes **(my call)**.

### 4.10 History

A list of finished releases, newest first **(user)**. One row per release
tag: release id, unit, release date, a link to the GitHub release and a link
to the release PR. The row expands to show the changelog section. Who
released it and whether it went out with failed QA lanes are shown when the
audit log has them **(my call)**.

The source is the GitHub releases of obs-packaging (one per tag, created by
`obs-release.yml`, notes are the changelog section), so releases made before
the tab existed are listed too. Nothing is stored for it. The PR is the one
associated with the tagged commit; a release without one shows no PR link.

## 5. Backend

### 5.1 GitHub access

A classic personal access token from the user's account with `repo` and
`workflow` scopes, read from config/env (`release.github_token`, env
`RELEASE_GITHUB_TOKEN`), plus `release.github_repo` (default
`percona/obs-packaging`) **(user: classic token from their account)**. All
GitHub actions therefore appear as the token owner; the real actor is recorded only inside
the dashboard (§ 5.4) **(user)**.

Checked on 2026-11-11: the repository is public, `main` has no branch
protection, rulesets or required checks, and only rebase merging is enabled.
The token's owner can therefore force-push `release/*` branches and merge
without review. Commits the dashboard creates through the API are unsigned;
the sign-off trailer is part of the commit message and is kept.

Events caused by this token trigger workflows; events caused by a workflow's
own `GITHUB_TOKEN` do not. Nothing in the flow depends on the latter: populate
starts from the `obs-sync` label and QA from the `qa-release` label, both
added by the dashboard. The PR is opened by the dashboard, not by the cut
workflow, so the repository's ordinary PR checks run on it **(my call)**.

With no token configured the feature is off: the tab is hidden and the
release routes return 404 **(my call)**.

The token reaches every repository its owner can access. It is handled like
the dashboard's other secrets and is to be replaced by a GitHub App
installation token if one becomes available; the code takes "a bearer token"
and nothing more **(my call)**.

### 5.2 Routes

All under `RequireUser` and, for writes, `CheckOrigin` (existing auth
middleware). Names are indicative; the builder may adjust them.

| Route | Does |
|---|---|
| `GET /api/release/units` | selection list with eligibility |
| `GET /api/release/releases` | in-flight releases with derived step |
| `GET /api/release/history` | finished releases (§ 4.10), paged |
| `GET /api/release/releases/{id}` | full state: units, changelog sections, runs, lanes, deletions |
| `POST /api/release/releases` | cut (body: unit roots) |
| `PUT /api/release/releases/{id}/changelog/{unit}` | amend |
| `POST /api/release/releases/{id}/approve-changelog` | add `obs-sync` |
| `POST /api/release/releases/{id}/retry-sync` | dispatch populate |
| `POST /api/release/releases/{id}/approve-qa` | add `qa-release` |
| `POST /api/release/releases/{id}/recut` | § 4.7 |
| `POST /api/release/releases/{id}/release` | merge (body: confirm failed lanes) |
| `POST /api/release/releases/{id}/retry-release` | dispatch `obs-release.yml` |
| `POST /api/release/releases/{id}/abandon` | § 4.8 |

Every write is logged with the user's email, as the existing protected
actions are.

### 5.4 Audit log

The acting user's identity never goes to GitHub: the repository is public
**(user)**. A SQLite table records one row per release action: time, user
email, action (cut, save changelog, approve changelog, retry sync, approve
QA, re-cut, release, retry release, abandon), release branch, PR number when
known, and for a release the failed lanes it was confirmed with. This is the
only release data the dashboard stores; it is a record of who did what, not
release state.

The Release tab shows, to signed-in users, who started a release (the cut
row for its branch) and who performed each later step. Releases with no rows
(started by hand) show the starter as unknown.

### 5.3 Reading state

State is derived from: open and recently merged PRs whose head branch starts
with `release/`, their labels, their changed `release.yaml` files, the runs
of the cut workflow, `obs-release-pr.yml` and `obs-release.yml`, the jobs and
annotations of those runs, and the per-instance pre-release PR comments.

The backend refreshes this from GitHub only while at least one client has the
Release tab open, every 15 seconds, using conditional requests, and pushes
changes to clients over the existing stream. Design for up to five releases
in flight. No GitHub
webhook is used **(my call)**.

## 6. What obs-packaging must provide

These are requirements on `percona/obs-packaging`, to be specified and built
there first. The dashboard is built against this contract.

The names below are this design's proposal **(my call)**; the dashboard is
coded against them behind one small module, and the obs-packaging spec may
rename them.

1. **Cut workflow** `obs-release-cut.yml`. `workflow_dispatch` with inputs
   `units` (space-separated unit roots, e.g.
   `ppg:staging:18 ppg:staging:17:extras`) and `branch` (e.g.
   `release/20261111-1432`). It creates or resets `branch` to the current
   `main`, runs `project release` for each unit and force-pushes one
   signed-off commit. Run name: `Cut <branch>`. It pushes nothing if any unit
   cannot be cut. It uploads an artifact `generated-changelogs` holding, per
   unit, the changelog section it generated. It does not open the PR; the
   dashboard does, once the run succeeds.
2. **Sync gate.** `obs-release-pr.yml` populates only when the PR carries
   `obs-sync`: on `labeled` with `obs-sync`, on a push to a PR that already
   has it, and on `workflow_dispatch`. A push to a PR without the label does
   nothing. `qa-release` behaviour is unchanged.
3. **Jenkins annotation.** Each QA lane job emits a notice annotation titled
   `Jenkins build` whose message is `<release project> <jenkins build url>`,
   readable through the Checks API while the job runs **(user: annotation,
   added in obs-packaging)**.
4. **Run names.** `obs-release.yml` runs are named `Release PR #<N>`, so a
   release's final sync can be found.
5. **Unit list.** `docs/versions/release-units.json` on `main`, kept current
   by CI: a list of `{unit, members[], release_project, release_dir}`. The dashboard never applies the `containers`/`tarballs`
   bundling rule itself **(my call)**.
6. **Deletions.** The hop-2 dry-run deletions are available in a form the
   dashboard can read without parsing prose: the existing
   `prerelease-report-<instance>` artifact, with its format documented.
7. **Independent release units** (2026-11-10 spec; merged there as PR #246).
   The unit list reflects whatever units the tool defines; the dashboard
   needs no change when they change.

Note on amends: `release_pr.py classify` compares pushes with a three-dot
compare, so an amended changelog-only commit is not recognised as
changelog-only and repopulates. This design avoids the case by making the
changelog read-only after approval; fixing `classify` is not required.

## 7. Left to the builder

- Route names, response shapes, component structure.
- How the release commit is amended through the GitHub API.
- The refresh interval and how "tab is open" is tracked (the presence gate
  exists).
- How the release id (`{id}` in routes) is formed; the PR number or the
  branch name both work, but a release must be addressable before its PR
  exists.
- Error presentation for GitHub API failures and rate limiting.
- Tests: backend against a stubbed GitHub API; frontend component tests in
  the existing style.

## 8. Order of work

1. obs-packaging: contract items 1 to 6 (own spec and plan in that repo).
2. Dashboard: backend GitHub client and state reading, then the tab.
3. A rehearsal on the labs instance before the first production release.
