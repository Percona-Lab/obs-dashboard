# Freeze and unfreeze release units from the Releases tab

Date: 2026-11-26

## Intent

obs-packaging gained a build freeze for release units
([percona/obs-packaging#355](https://github.com/percona/obs-packaging/pull/355)).
A frozen unit ignores dependency updates (a new OS base package no longer
starts a wave of rebuilds), while a fix merged to `main` still builds. Today a
freeze is done from the CLI or by starting the `obs-freeze.yml` workflow by
hand on GitHub.

The people who prepare releases already work in the dashboard's Releases tab.
They should be able to see what is frozen, freeze units and unfreeze them
there, and be told when they cut a release of a unit that is not frozen.

## Goals

- Show which release units are frozen now, on which OBS instances, and who
  froze them from the dashboard.
- Freeze one or more units, chosen from a list.
- Unfreeze a frozen unit.
- Warn, on the cut list, about a ticked unit that is not frozen.

## Non-goals

- The dashboard does not suggest freezing and offers no Freeze button outside
  the Build freeze card. The warning on the cut list only informs.
- A cut does not freeze, and a finished release does not unfreeze. Both stay
  manual. *(my call; not discussed)*
- No UI for `build propagate` or for the freeze dry run.
- No history view of past freezes. The audit rows are written; nothing lists
  them.

## Anti-goals

- Showing "frozen" when OBS says otherwise. The state on screen comes from
  OBS, never from what the dashboard remembers having asked for.
- Blocking a cut because a unit is not frozen.

## What exists today

- `backend/internal/release` drives the obs-packaging workflows through the
  GitHub API: `Client.Dispatch`, `Client.WorkflowRuns`, `Client.Jobs`. Workflow
  and job names live in `contract.go`. `Service.Cut` is the model for an action
  that dispatches a workflow, remembers the dispatch until GitHub lists the
  run (`notePendingCut`), and writes an audit row (`recordRow`,
  table `release_audit`).
- `backend/internal/obs`: `Fleet` holds the OBS instances; an `Instance` maps a
  logical project name to its name on that instance (`Root + ":" + logical`).
  `Client.ProjectPublishFlags` already fetches and parses a project `_meta`.
- Routes are in `backend/internal/api/release_routes.go`, behind `RequireUser`;
  writes also pass the `origin` check. `relEmail` gives the signed-in user.
- Frontend: `ReleasePanel.vue` renders three cards (In flight, New release,
  History). `ReleaseUnitList.vue` is the cut list: rows grouped by PostgreSQL
  major, a tick box, a "n / m built" pill, and an alert strip under a blocked
  row. `useRelease.ts` polls the snapshot every 15 s while the tab is visible.
  `ReleaseConfirm.vue` is the confirmation used by the wizard.

## The obs-packaging contract

- Workflow `obs-freeze.yml`, `workflow_dispatch` only, inputs:
  - `units`: unit roots separated by spaces (`ppg:staging:18 ppg:staging:17:extras`)
  - `action`: `freeze` or `unfreeze`
- Run name: `<action> <units>`.
- One job per OBS instance, named `<action> units (<instance>)`; jobs are
  serialised per instance and not cancelled.
- The marker: a unit is frozen on an instance when every `<repository>` of the
  `_meta` of each of its OBS projects carries `rebuild="local"`. The projects
  of a unit are its root and the children `<root>:containers` and
  `<root>:tarballs` that exist. Per project the state is `frozen`, `partial`,
  `unfrozen`, or `empty` (no repositories; counts as neither).
- Freezing an already frozen unit, or unfreezing an unfrozen one, is a no-op.

## Approaches

How the dashboard learns what is frozen:

1. **Read the marker from OBS** on every instance. True state; sees freezes
   done from the CLI or GitHub and a freeze that failed on one instance. Costs
   one `_meta` read per unit project per instance.
2. **Infer from the workflow run history.** No OBS reads; wrong after a CLI
   freeze or a failed instance job; cannot show a partial freeze.
3. **Keep the state in the dashboard database.** Simplest; drifts like 2 and
   also misses runs started on GitHub.

**Chosen: 1** (by Ricardo). The warning on the cut list is only worth showing
if it is right. The run history is still used, but only for "a freeze is
running now" and "the last one failed".

## Design

### Layout (decided)

A new card, **Build freeze**, between *In flight* and *New release*.

```
┌ ❄ Build freeze  [3 frozen]                         [Freeze units…] ┐
│ A frozen unit ignores dependency updates. Fixes merged to main      │
│ still build.                                                        │
│ ┌ ppg:staging:18                                       [Unfreeze] ┐ │
│ │ frozen by maria@… · 2 days ago · all instances                  │ │
│ ├ ppg:staging:17:extras          [Freeze everywhere]   [Unfreeze] ┤ │
│ │ frozen by ricardo@… · 5 hours ago · labs only — not frozen on   │ │
│ │ build.opensuse.org                                              │ │
│ ├ ppg:staging:16                             [Unfreeze, disabled] ┤ │
│ │ ◌ Freezing… started by ricardo@… · workflow run                 │ │
│ └─────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────┘
```

- The card lists every unit that is frozen or partly frozen on at least one
  instance, plus every unit with a freeze or unfreeze run in progress.
  With none: "No frozen units."
- **Freeze units…** opens a dialog with all release units, grouped by
  PostgreSQL major like the cut list, each with a tick box. Units already
  fully frozen, or with a run in progress, are shown ticked-off and disabled
  with the reason. The "n / m built" pill is shown for information; being not
  built, in flight or unchanged does not prevent freezing. Footer: count,
  Cancel, "Freeze n unit(s)". The dialog carries the note "Unfreezing later
  lets the postponed rebuilds start."
- **Unfreeze** asks for confirmation first (reuse `ReleaseConfirm`): the
  postponed rebuilds will start; if the unit has a release in flight, say so.
  It is not blocked. *(my call)*
- **Freeze everywhere** appears on a partly frozen unit and dispatches a
  freeze of that unit.
- A unit whose most recent freeze/unfreeze run failed shows
  "The last <action> failed on <instances>" with a link to the run, until a
  newer run for that unit exists. *(my call)*

Cut list (*New release* card):

- Every row of a fully frozen unit gets a `❄ frozen` pill beside
  "n / m built"; a partly frozen one gets `❄ partly frozen`.
- A **ticked** unit that is not fully frozen gets the existing alert strip
  under its row, warn colour:
  - not frozen: "Not frozen: a dependency update can start rebuilds while this
    release is prepared."
  - partly: "Frozen on <instances> only: …" with the same ending.
- No button in the strip. Cut release stays enabled. No strip while the
  freeze state is not known (loading or unreadable).

### Backend

- `contract.go`: the workflow name, the two action words, the run-name shape
  and the job-name prefix.
- **Freeze state.** For each unit of the release units file and each fleet
  instance: read `_meta` of the root and the two children on that instance.
  A project that does not exist there is skipped; a unit with no project on an
  instance has no state there and that instance is left out. Instance state
  for a unit: `frozen` when every existing non-empty project is `frozen`,
  `unfrozen` when none has any frozen repository, else `partial`. Unit state:
  `frozen` when frozen on every instance that hosts it, `unfrozen` when
  unfrozen on all, else `partial`. An instance that cannot be read gives
  `unknown` for that instance and the unit shows "could not read <instance>";
  the other instances still count.
- **Caching.** The reads are cached; the same `_meta` is not fetched more
  than once a minute per instance in steady state, and the cache is dropped
  when a freeze run is dispatched or seen to finish. *(my call)*
- **Runs.** The recent `obs-freeze.yml` runs give, per unit: a run in progress
  (action, URL), or the failure of its latest run (failed instances from the
  job names, URL). A dispatch not yet listed by GitHub is remembered the way a
  pending cut is, so the row shows "Freezing…" at once.
- **Who and when.** From the newest `release_audit` row with action `freeze`
  naming the unit. Shown only while the unit is frozen or partly frozen.
- **Actions.** `Freeze(email, units)` and `Unfreeze(email, units)`:
  every unit must be in the units file; a unit with a run in progress is
  refused (409, like the other conflicts); then dispatch, remember, write one
  audit row (action `freeze` / `unfreeze`, detail: the units), drop the cache.
- **Routes**, same middleware as the other release routes, so the same people
  who can cut can freeze:
  - `GET /api/release/freeze` → one entry per unit: unit, state, per-instance
    states, frozen_by, frozen_at, pending run, last failure.
  - `POST /api/release/freeze` with `{action, units}`.

### Frontend

- `useRelease.ts`: load the freeze state with the same 15 s poll; `freeze`
  and `unfreeze` actions that use the shared `busy` / `notice` handling.
- New components under `components/release/`: the card and the picker dialog.
  `ReleasePanel.vue` mounts the card; `ReleaseUnitList.vue` takes the freeze
  state as a prop for the pill and the strip.
- Types in `types/release.ts`.

### Details settled after review

From the code:

- A unit's `unit` in the release units file is the unit root the workflow
  takes; `Cut` already passes it unchanged.
- No name mapping between the dashboard's instances and the workflow's is
  needed: per-instance freeze state is labelled with the dashboard's
  `Instance.Name`; a failed instance is labelled with the text taken from the
  job name, as the sync cards do.

My calls:

- A run is matched to units by splitting its name on spaces: first token the
  action, the rest whole unit names. Never by substring.
- Runs are listed with the same limit as the cut and release runs
  (`runListLimit`). A failure older than that window is not shown.
- A failed run that named several units marks each of them with the failure.
  Which units were actually changed is shown by the state read from OBS.
- The per-instance `_meta` read is a new fleet-level read that keeps
  instances apart, with its own one-minute cache; the publish-flags cache is
  not reused. The existing OBS rate limiter is the only cap.
- The freeze endpoint lists the `obs-freeze.yml` runs itself on each
  read (the GitHub client already answers unchanged lists from its ETag
  cache). A run that was in progress on the previous read and
  is finished on this one drops the `_meta` cache.
- The freeze endpoint fails on its own: if it cannot be loaded the card shows
  an error, the cut list shows no pill and no strip, and cutting is
  unaffected.
- `Freeze` of an already frozen unit and `Unfreeze` of an unfrozen one are
  accepted and dispatched; the workflow makes them no-ops.
- "Frozen by" is hidden when a newer `unfreeze` audit row names the unit.
- A unit whose existing projects on an instance are all `empty` has no state
  on that instance, like a unit that instance does not host. A unit with no
  state on any instance is not listed in the card, gets no pill and no strip,
  and is disabled in the picker.

## Known limits

- "Frozen by" is what the dashboard last recorded. A unit frozen from the
  dashboard, then unfrozen and frozen again from the CLI, still shows the
  dashboard user. A unit never frozen from the dashboard shows no name.
- The dashboard checks the instances it is configured with; the workflow
  acts on the `OBS_INSTANCES` of the obs-packaging repository. If they
  differ, a unit can look fully frozen here and not be on an instance the
  dashboard does not know.

## Left to the builder

- Shapes of the Go types and JSON fields, the cache implementation, and where
  the `_meta` parsing lives (next to `ProjectPublishFlags` is the obvious
  place).
- Whether the dialog is a native `<dialog>` or follows an existing modal in
  the frontend.
- Exact wording beyond the strings quoted above.
- Tests: backend with the existing fake GitHub and a fake OBS; frontend
  component tests like the existing `Release*.test.ts`.
