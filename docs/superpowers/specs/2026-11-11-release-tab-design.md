# Release tab: cutting PPG releases from the dashboard

Date: 2026-11-11. Status: implemented (PR #8). Updated after implementation to match the code: sections 3 to 6 describe what was built, including behaviour added during review and after the obs-packaging rehearsal.

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
**(my call)** were made while writing this document. Decisions marked
**(review)** were made during implementation and review, where the original
design was silent; they have not been individually confirmed.

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
`root/*/releases/**/release.yaml`, or a `release/*` branch that has no PR
yet, or a cut run that has not produced a branch yet. Its state is read from
GitHub; the dashboard keeps no release state of its own **(my call, follows
from the label gating the user chose)**. Releases cut from the dashboard use
a branch named `release/<UTC timestamp>`. A release PR opened by hand (CLI
flow) is shown too, with the id `pr-<N>`, and can be driven from step 2
onwards; its starter is shown as unknown and it cannot be re-cut **(my
call)**. A PR from a fork is listed read-only: every action is refused
**(review)**.

| Wizard step | State on GitHub | Dashboard action that leaves this step |
|---|---|---|
| 1 Select | no release | dispatch the cut workflow |
| 2 Changelog (`cutting`) | cut run queued, running, held for approval, or failed with no branch | none; advances when the cut succeeds and the PR is opened |
| 2 Changelog (`changelog`) | PR open, no `obs-sync` label | add label `obs-sync` |
| 3 Pre-release sync (`sync`) | `obs-sync` present, populate not yet successful | none; advances when populate succeeds |
| 4 QA (`qa`) | populate succeeded; `qa-release` absent (waiting) or present (running, finished, failed to start) | add `qa-release`; later merge the PR |
| 5 Release (`releasing`) | PR merged, final sync not yet successful | none; the release leaves the list when `obs-release.yml` succeeds |

A release may carry several units in one PR. Labels are per PR, so changelog
approval, QA approval and release approval each cover all units of the
release **(my call)**. Several releases can be in flight at once when they
cover different units (obs-packaging enforces one in-flight PR per
`release.yaml`).

Runs with the conclusion `action_required` are never treated as failures.
For `obs-release-pr.yml` they are ignored (the bot's force-push on a re-cut
creates such runs, with no jobs). For the cut workflow they mean GitHub is
holding the run for approval (§ 4.3). Skipped matrix jobs keep an unexpanded
name (`Populate pre-release (${{ matrix.instance.name }})`); such jobs are
ignored **(review, from the obs-packaging rehearsal)**.

## 4. The tab

A fifth `mainTab` value, `release`, shown only when signed in and when the
feature is enabled **(user: authenticated users only)**. With the feature
disabled, a link to the tab goes to Overview; a temporary failure of the
status call does not move the user off the tab **(review)**. Mockups of the
original screens are in `.superpowers/brainstorm/2543469-1794417318/content/`
(`release-select.html`, `release-changelog.html`, `release-sync.html` option
A, `release-qa-and-final.html`); the user approved them.

### 4.1 Landing view

In-flight releases are listed first, each with its release ids, PR number,
who started it and when, and its current step (Cutting, Waiting for
approval, Cut failed, Changelog, Pre-release sync, QA, Releasing); opening
one shows the wizard at that step. Below is the selection list for a new
release, and below that the history (§ 4.10) **(my call)**.

A stale snapshot (GitHub could not be reached) shows a warning bar with the
reason; the last good data stays on screen and every action is refused
until a refresh succeeds **(review)**.

### 4.2 Step 1: select

One row per release unit, grouped by PostgreSQL major plus Common. A row
shows the unit root, its member projects, the last release id and a built
count for the whole unit (all member projects) **(user: list of eligible
staging subprojects with built status)**. The unit list comes from
`docs/versions/release-units.json` in obs-packaging; a load failure is shown
as an error, never as an empty list.

A unit is tickable only when all of these hold. Otherwise the row stays
visible, unticked, with an alert giving the reason **(user)**. The reasons
are checked in this order:

| Condition | Alert |
|---|---|
| No release in flight covers the unit | names the in-flight release, with a button that opens it |
| Every package in every member project is built (state succeeded or published), and the unit has at least one package | names up to ten unbuilt packages, with an "Open in Builds" button; a unit with no packages says "No packages found in staging." **(review)** |
| Staging differs from the unit's release project | "identical to release `<id>`" |

"Differs" means the set of package name, version and container tags in the
unit's staging projects is not equal to the set in its release projects
**(my call)**. A unit that has never been released is tickable.

While a cut is queued or running and the units it covers are not yet known,
no unit can be ticked ("A release is being cut. Try again in a minute.").
Units cut from the dashboard in the last two minutes count as in flight even
before GitHub shows the cut **(review)**.

The mockup shows the next release id on each row. It is not shown: the id is
derived by the cut and first shown in step 2 **(my call)**.

"Cut release" dispatches the cut workflow for the ticked units. The tab then
shows "Starting the cut…" until GitHub lists the run; if it has not appeared
after 90 seconds the tab returns to the landing view with a notice
**(review)**.

### 4.3 Step 2: changelog

While the cut runs, a short progress list (starting the cut on GitHub,
generating release files and changelogs, opening the release PR), with a
spinner on the stage in progress. Throughout the wizard, anything being
waited for shows a spinner: a queued or running instance or QA lane, the
sync, QA and the final sync before they start, and a cut held for approval
**(user)**. The cut does not open the PR; the dashboard opens it, without
labels, once the cut run has succeeded and every unit has a release id and a
changelog section. It is opened at most once per branch: a branch whose PR
someone closed is left alone and not listed **(review)**.

Other cut states **(review)**:

- **Held for approval.** GitHub can hold a cut run until a person with write
  access approves it (seen the first time the workflow is dispatched after
  the workflow file changes). The held run has no jobs and does not carry
  the branch in its name. The dashboard matches it to a release through its
  own audit log: the `cut` or `recut` row closest in time to the run's
  creation, within 30 seconds before and 180 seconds after, each run and row
  used once. The release shows "Waiting for approval on GitHub" with a link
  to the run. Only Abandon is accepted meanwhile (when a PR exists). A held
  run older than 24 hours, or one whose release was abandoned afterwards, is
  ignored. A held cut that was dispatched outside the dashboard is not
  shown.
- **Failed first cut.** The workflow pushes nothing on failure, so there is
  no branch. The release is listed as "Cut failed" for 24 hours with a link
  to the run and a "Retry"; there is nothing to abandon.
- **Failed re-cut.** The PR is left as it was, without labels. The wizard
  shows "The cut failed", the run link and "Retry"; the editor is read-only
  and Save, Reset and Approve are refused until a cut succeeds.

Every step of the wizard shows a step bar (finished steps ticked, the
current step highlighted) and, below it, a status line: "Step N of 5 ·
<name>" and what is happening in that step. The line has three kinds:
waiting on GitHub (with a spinner), "Your turn" when the user has to act
(review the changelog, approve QA, release), and failed **(user)**.

Once the PR exists:

- A release strip: units, PR number, who started it and when, "Abandon
  release", and an activity list of who performed each step (§ 5.4).
- One tab per unit. Editor on the left, rendered preview on the right. The
  editor holds only the section of this release in the unit's `CHANGELOG.md`
  (from its `## [<id>]` heading to the next `## [` heading). The preview is
  built from parsed text; nothing from GitHub is rendered as HTML.
- "Save" amends the release commit and force-pushes the release branch
  **(user: changelog edits amend the release commit)**. The commit keeps its
  message, author, parent and sign-off. The rest of the file is left
  byte-identical.
- "Reset to generated text" restores the section as the last cut produced it
  **(my call)**, from the `generated-changelogs` artifact of the latest
  successful cut run. It asks first when the unit has unsaved edits.
- "Approve changelogs & start sync" adds `obs-sync` **(user: sync starts only
  after changelog approval, gated on the `obs-sync` label)**. It is disabled
  while any unit has unsaved edits.

Unsaved text is kept per release and unit while the page is open: it
survives switching unit tabs, going back to the landing view, switching main
tabs and a failed save. The browser warns before leaving the page with
unsaved text. If the changelog of a unit changes on GitHub while it has
unsaved edits, the text is kept and a banner says so; saving overwrites the
GitHub text **(review)**.

The editor is read-only once `obs-sync` is on the PR **(my call)**.

### 4.4 Step 3: pre-release sync

One card per OBS instance (the populate matrix legs of `obs-release-pr.yml`):
queued, running, done or failed, elapsed time, and a link to the job on
GitHub **(user: option A)**. Once its job has reported, each card lists
every unit of the release with its outcome on that instance, from the
populate report: synced, failed, or not on this instance **(user)**. A unit
can live on one instance only; on the other its outcome is `skipped`, which
means nothing was populated there. That is never a failure and never
pending. A card whose units are all skipped shows "Nothing to sync". A failed
card also shows the job's error annotations.

A unit is populated when every instance reports `success` or `skipped` and at
least one reports `success`. Once every instance has reported, a unit that
all of them skipped makes the sync a failure, with the text "`<project>` was
not populated on any instance", even though every job succeeded; QA cannot
be approved and "Retry sync" is offered **(user)**. The dashboard never links
a pre-release project, which does not exist on an instance that skipped the
unit.

When populate is done, the packages and subprojects that the release would
delete from the release projects (the hop-2 dry-run result, from the
populate report) are listed, or "Nothing will be removed".

On failure: a "Retry sync" button (removes and re-adds `obs-sync`, which
re-runs populate for the PR head) and "Update to latest main & restart"
(§ 4.7) **(my call)**. If the workflow fails before any instance reports
(for example in its `resolve` job), the step shows that the sync failed,
with a link to the run **(review)**.

### 4.5 Step 4: QA

Before QA: the dry-run deletions again and "Approve & run QA", which adds
`qa-release` **(user)**.

The lanes are the `Release / OBS QA / …` jobs of the newest
`obs-release-pr.yml` run for the PR's current head in which QA ran; runs for
older heads are ignored. A lane belongs to the unit whose release project
(or one of its subprojects) the lane's `Jenkins build` annotation names;
lanes without one are grouped under "Other".

During and after: passed/failed/running counts, lanes grouped by unit,
failed lanes first, each failed lane linking to its Jenkins job **(user)**;
passed lanes collapsed. A lane without a Jenkins link falls back to its
GitHub job page.

A release can have no QA tests at all: the QA run then finishes
successfully without a single lane (the fan-out ran, its matrix was empty,
and its only lane job is skipped). QA counts as finished; the step says that
no QA tests are defined, and "Release" is offered with a confirmation that
says no tests ran **(user)**.

If the QA run fails before any lane starts (the wait for the download
servers, or the matrix detection), the step shows "QA could not start" or
that the download servers did not serve the pre-release in time, with a link
to the run and a "Retry QA" button (removes and re-adds `qa-release`)
**(review)**.

Buttons:

- "Update to latest main & restart" (§ 4.7) **(user)**.
- "Release". Enabled once QA has finished. With failed lanes (or lanes that
  did not finish) it asks for confirmation, lists them, and the button reads
  "Release anyway" **(user: releasing with failures is allowed after
  confirmation)**. Without failures it asks for a plain confirmation. The
  backend enforces both rules itself.

### 4.6 Step 5: release

"Release" merges the PR with the rebase method (the only one the repository
allows; `obs-pr-cleanup.yml` tags `merge_commit_sha`), then deletes the
release branch **(review)**. One user's decision is enough; no second
reviewer **(user)**. Who approved, and any failed lanes it was released
with, go into the dashboard's audit log (§ 5.4), not into the PR.

The view then shows: PR merged, tags, one card per OBS instance for the
`obs-release.yml` copy legs (same detail as step 3), the GitHub releases
step and the pre-release deletion step.

On failure the view says "The final sync failed." and offers "Retry final
sync", which re-dispatches `obs-release.yml` with the same `pr_number` and
`tags` (documented as idempotent) **(my call)**.

A merged release stays listed as in flight until its `obs-release.yml` run
succeeds; it then appears in the history (§ 4.10).

### 4.7 Update to latest main & restart (re-cut)

Removes `obs-sync` and `qa-release` first, then dispatches the cut workflow
again for the same branch and units. The workflow regenerates the release
commit on top of the latest `main` and force-pushes. The wizard returns to
step 2 with the newly generated changelog; earlier hand edits are gone and
the user edits and approves again **(user)**. It asks for confirmation
first.

After a cut or re-cut is dispatched, the release is held at "cutting" until
GitHub lists the new run, for at most two minutes, and refuses actions
meanwhile, so the old changelog cannot be approved in that window
**(review)**. A failed re-cut is described in § 4.3.

A hand-made release (`pr-<N>`) cannot be re-cut.

### 4.8 Abandon

Deletes the release branch, which makes GitHub close the PR; obs-packaging's
PR cleanup then removes the pre-release projects. For a hand-made release it
only closes the PR. The units become selectable again **(my call)**.

Available from the moment a branch or PR exists until the PR is merged, with
one exception: **while a cut is queued or running the button is shown
disabled**, with the reason, because the running cut would re-create the
branch **(user)**. A cut stuck in GitHub's queue therefore has to finish or
be cancelled on GitHub before the release can be abandoned.

### 4.9 Two users at once

Every user sees the same state. Each action names the release and the head
commit it was issued against; if GitHub has moved on, or the release is at
another step, the backend refuses with 409 and the client refreshes **(my
call)**. A confirmation dialog acts with the head that was shown when it
opened **(review)**.

Actions run one at a time on the server. A retry (sync, QA, final sync) is
refused when the same retry was accepted for that release less than 60
seconds ago, and while the run it retries is still in progress **(review)**.

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
Pages hold 20 GitHub releases; "Load more" is offered while GitHub has more.
A load failure is shown as an error with a retry, not as an empty list.

## 5. Backend

### 5.1 GitHub access

A classic personal access token from the user's account with `repo` and
`workflow` scopes, read from config/env (`release.github_token`, env
`RELEASE_GITHUB_TOKEN`), plus `release.github_repo` (default
`percona/obs-packaging`) **(user: classic token from their account)**. All
GitHub actions therefore appear as the token owner; the real actor is
recorded only inside the dashboard (§ 5.4) **(user)**.

Checked on 2026-11-11: the repository is public, `main` has no branch
protection, rulesets or required checks, and only rebase merging is enabled.
The token's owner can therefore force-push `release/*` branches and merge
without review. Commits the dashboard creates through the API are unsigned;
the sign-off trailer is part of the commit message and is kept.

Events caused by this token trigger workflows; events caused by a workflow's
own `GITHUB_TOKEN` do not start runs that execute. Populate starts from the
`obs-sync` label and QA from the `qa-release` label, both added by the
dashboard. The PR is opened by the dashboard, not by the cut workflow, and
without labels (a PR opened with `obs-sync` already on it could populate
twice).

With no token configured the feature is off: the tab is hidden,
`GET /api/release/status` reports `{"enabled": false}` and the other release
routes return 404 **(my call)**.

The token reaches every repository its owner can access. It is handled like
the dashboard's other secrets and is to be replaced by a GitHub App
installation token if one becomes available; the code takes "a bearer token"
and nothing more **(my call)**. The token never appears in a response, a log
line or an error: GitHub client errors carry method, path and status only.

### 5.2 Routes

All under `RequireUser` and, for writes, `CheckOrigin` (existing auth
middleware).

| Route | Does |
|---|---|
| `GET /api/release/status` | `{"enabled": bool}` |
| `GET /api/release/units` | selection list with eligibility |
| `GET /api/release/releases` | snapshot: every in-flight release with its derived step and full state, plus `stale` and `error` |
| `GET /api/release/history?page=N` | finished releases and whether more exist |
| `POST /api/release/releases` | cut (body: unit roots) |
| `PUT /api/release/releases/{id}/changelog` | amend (body: head, unit, section) |
| `POST /api/release/releases/{id}/changelog/reset` | restore the generated text (body: head, unit) |
| `POST /api/release/releases/{id}/{action}` | `approve-changelog`, `retry-sync`, `approve-qa`, `retry-qa`, `recut`, `release` (with `confirm_failed`), `retry-release`, `abandon` (body: head) |

Errors: an unknown release is 404; a wrong step, stale head, fork PR or
refused state is 409; a bad changelog section or a missing confirmation is
400 with a short message; anything else is 502 with a fixed text, the real
error being logged. Every write is logged with the user's email, as the
existing protected actions are.

### 5.3 Reading state

State is derived from: open and recently merged (7 days) PRs that change a
`release.yaml`, their labels, the `release/*` branches, the runs of the cut
workflow, the `obs-release-pr.yml` runs for the PR head commit,
`obs-release.yml` runs (found by the name `Release PR #<n>`), the jobs and
annotations of those runs, the populate report and generated-changelog
artifacts, and the dashboard's own audit rows (to attribute a held cut,
§ 4.3).

The backend keeps the derived state in memory and refreshes it from GitHub
on demand: a request finds it fresh if it is younger than 15 seconds,
otherwise one refresh runs (concurrent requests share it), using conditional
requests. The Release tab polls every 15 seconds while it is visible, so
GitHub is only queried while someone is looking. No GitHub webhook and no
server push is used **(my call)**.

Results that cannot change are kept in memory: the jobs, annotations and
artifacts of completed runs, and files read by commit. They are dropped when
their release leaves the list **(review)**. With five releases in flight a
refresh makes on the order of a hundred requests, nearly all answered "not
modified", which do not count against the token's hourly limit.

If a refresh fails, the previous snapshot is served marked stale with a
sanitised error, and every action is refused until a refresh succeeds. A
single unreadable file (missing, not a regular file, too large) degrades
that one release and never fails the refresh; a fork PR never fails it
**(review)**.

Nothing about a release survives a restart except the audit log. In-memory
markers (a pending dispatch, recently cut units, retry cooldowns) are lost
on restart; GitHub state then speaks for itself.

### 5.4 Audit log

The acting user's identity never goes to GitHub: the repository is public
**(user)**. A SQLite table (`release_audit`) records one row per accepted
release action: time, user email, action (cut, save changelog, approve
changelog, retry sync, approve QA, retry QA, re-cut, release, retry release,
abandon), release branch, PR number when known, and a detail: the unit roots
for a cut or re-cut, the failed lanes for a release. This is the only
release data the dashboard stores; it is a record of who did what, not
release state. A refused or failed action writes no row.

The Release tab shows, to signed-in users, who started a release and who
performed each later step, in the wizard's activity list. Releases with no
rows (started by hand) show the starter as unknown.

## 6. What obs-packaging provides

Implemented in `percona/obs-packaging` (PR #249), specified there in
`docs/superpowers/specs/2026-11-11-dashboard-release-contract-design.md`.
Every name below lives in `backend/internal/release/contract.go` and nowhere
else in the dashboard.

1. **Cut workflow** `obs-release-cut.yml`. `workflow_dispatch` with inputs
   `units` (space-separated unit roots) and `branch` (`release/…`). It
   resets `branch` to the current `main`, runs `project release` for each
   unit and force-pushes one signed-off commit. Run name: `Cut <branch>`. It
   pushes nothing if any unit fails. It uploads the artifact
   `generated-changelogs` (one file `changelogs.json`: unit root → generated
   section). It does not open the PR.
2. **Sync gate.** `obs-release-pr.yml` populates only when the PR carries
   `obs-sync`: on `labeled` with `obs-sync`, on a push to a PR that already
   has it, and on `workflow_dispatch`. `labeled` with `qa-release` runs QA
   alone against the existing pre-release.
3. **Jenkins annotation.** Each QA lane job emits one notice annotation
   titled `Jenkins build` whose message is `<release project> <build url>`.
4. **Names.** Jobs `Populate pre-release (<instance>)`,
   `Wait for download CDN (<instance>)`, lanes starting `Release / OBS QA / `,
   `Release to OBS (<instance>)`,
   `Publish GitHub releases and delete the pre-release`,
   `Delete the pre-release (<instance>)`; `obs-release.yml` runs are named
   `Release PR #<n>`.
5. **Unit list.** `docs/versions/release-units.json` on `main`: a list of
   `{unit, members[], release_project, release_dir}`.
6. **Populate report.** Artifact `prerelease-report-<instance>`: a JSON list
   of `{project, prerelease, outcome, source_commit, removals}`, where
   `outcome` is `success`, `failure` or `skipped` (nothing of the project on
   that instance).

Behaviour seen in the rehearsal that the dashboard handles (§ 3, § 4.3): a
cut run held for approval; `action_required` PR runs with no jobs after a
re-cut; unexpanded names of skipped matrix jobs.

Not yet exercised against the real repository, because they write to
production: adding `obs-sync` (populate and its report), the retry by
re-adding the label, adding `qa-release` (the QA-only run and its
annotations), and the merge with the final sync.

## 7. Known gaps

- A GitHub 404 on a release route is not distinguished from "feature off".
- The cut progress list shows state and a link only, not per-unit progress.
- There is no `GET /api/release/releases/{id}`; the snapshot carries the
  full state of every release.
- Failed background polls in the browser are silent unless the backend marks
  the snapshot stale.
- The confirmation dialog has no focus trap.
- A cut dispatched outside the dashboard and held for approval is not shown.

## 8. Order of work

1. obs-packaging: the contract of § 6. Done (PR #249).
2. Dashboard: backend and tab. Done (PR #8).
3. A rehearsal of the sync, QA and release steps against the real
   repository, at the next real release.
