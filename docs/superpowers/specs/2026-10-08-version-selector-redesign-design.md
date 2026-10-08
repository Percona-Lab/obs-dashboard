# Version selector redesign: two-level chips with a cross-version pseudo-version

**Status: DRAFT, brainstorming paused on 2026-10-08.** Sections 1 and 2
were approved in discussion. Section 3 was presented but not yet approved.
The OBS project layout is about to change again (see "Pending layout
change"), so re-validate the key model against the new layout before
implementing.

## Problem

The version selector (Artifacts bar, Builds board context bar, events
filter) is a flat chip row derived from project paths. It only accepts
numeric segments at the version position, so the version-independent
container projects `ppg:staging:containers` and
`ppg:staging:extras:containers` never get a chip and are unreachable in
Artifacts. On the board they leak into every version because the package
and event filters treat any non-numeric segment as "common, always
shown".

Adding them as flat chips does not scale: staging already shows ten chips
(`19 18 18:extras 17 17:extras 16 16:extras 16:tde 15 14`), and every new
version or subproject multiplies the row.

## Decisions made

- **Shape: two-level chips** (option A of three mockups). A version row
  plus a subproject row that appears only when the selected version has
  more than one entry. Rejected: version chips plus subproject dropdown
  (B), single grouped dropdown (C).
- **Containers stay absorbed.** The version-independent projects behave
  like any other version: their own `:containers` (and `:tarballs`) fold
  into the base entry. Only real subprojects (extras, tde, ...) get a
  subproject chip. This preserves today's behaviour where `18:containers`
  is part of `18`.
- **Pseudo-version name: `cross-version`** ("shared" was rejected).
- **Base chip label: `base`** (rejected: `core`, repeating the version).
- **Scope: everywhere** the shared helper is used (Artifacts, board,
  events filter), so the three views keep scoping identically.

Approved states of the Artifacts bar (PPG Staging, today's data):

| Selected key | Version row | Subproject row | Path badge |
|---|---|---|---|
| `cross-version` | `19 18 17 16 15 14 cross-version` | `base · extras` | `ppg:staging:containers` |
| `16:tde` | same | `base · extras · tde` | `ppg:staging:16:tde` |
| `19` | same | hidden | `ppg:staging:19` |

Mockups are persisted under `.superpowers/brainstorm/534111-1791467148/content/`
(`selector-layouts.html`, `selector-a-v2.html`).

## Section 1: key model (approved)

Keys stay single strings: `18`, `18:extras`, `''` for All on the board.
URL state, props and the three filters keep their shape.

- Reserved pseudo-version `cross-version`. Base key `cross-version`,
  extensions `cross-version:<sub>` (e.g. `cross-version:extras`).
- Key to project base mapping. Numeric keys map as today.
  `cross-version` maps to `<prefix>:<absorbed>` subtrees
  (`ppg:staging:containers`, `ppg:staging:tarballs`).
  `cross-version:extras` maps to the `ppg:staging:extras` subtree.
- `deriveVersionKeys` (frontend/src/lib/versions.ts): a non-numeric
  segment at the version position is no longer dropped. If it is an
  absorbed name it yields the plain key `cross-version`; otherwise it
  yields `cross-version:<segment>`. Catch-all contexts (no absorbed list,
  i.e. Releases) keep producing plain keys only. Order: numeric
  descending as today, then `cross-version` and its extensions last.
- `matchesVersionKey` gains the cross-version branch above.
- Behaviour fix: in `usePackages.ts` and `useEvents.ts` the rule becomes
  "no segment at the version position = common tree outside the prefix,
  always shown; a segment present goes through the key matcher". This
  stops cross-version projects from showing under every version.

## Section 2: selector component (approved)

New `frontend/src/components/VersionSelector.vue`, used by
`ContextBar.vue` and `ArtifactsVersionBar.vue`. Props: flat key list,
selected key, `allowAll` flag. Emits the new key.

- Version row: distinct first tokens of the keys in list order. On the
  board an `All` chip (empty key) follows, as today. Artifacts has no All.
- Subproject row: shown only when the selected version has more than one
  key. `base` for the plain key when it exists, then extension names in
  list order.
- Clicking a version chip selects its plain key if one exists, otherwise
  its first extension. Clicking a subproject chip selects that key. A key
  is therefore always valid and the existing snap-to-first watchers in
  `App.vue` and `ArtifactsPanel.vue` need no change.
- The monospace path badge shows the key's base project via a small
  key-to-project helper (shared with the matcher).
- Both bars drop their inline chip loops; the Artifacts bar's chip styles
  become the shared ones.

## Section 3: fetching, tab behaviour, testing (presented, NOT yet approved)

- Repos fetch in `ArtifactsPanel.vue`: derive the repos URL from the key's
  base project instead of sending the literal `cross-version` token. For
  `cross-version` base request subproject `containers`; for
  `cross-version:extras` request `extras`. Uses the existing
  `?subproject=` parameter. No backend change.
- Empty Packages tab: when the selected key changes to a cross-version
  key and the active tab is Packages, switch to Containers. Do not switch
  back automatically. **Needs revisiting** once `staging:tools` exists,
  because that cross-version subproject holds packages, so the rule
  should be "switch only when the key has no packages", or be dropped.
- Backend: no API or store changes. The overview already splits these
  projects into their own rows (commit f8e2aee, deployed 2026-10-07).
- Error handling: unknown or stale URL keys fall through the existing
  snap-to-first watchers.
- Testing: add Vitest as a dev dependency with a `test` script (the
  frontend currently has only compile-time `.test-d.ts` checks). Unit
  tests for key derivation, matching, key-to-project mapping, and the
  selector's row splitting and click mapping. Then production build and a
  visual check on prod of the three approved states.

## Pending layout change (2026-10-09)

The user plans to add `ppg:staging:tools`, a subproject holding packages
that are cross-version components. Under the current model it derives to
key `cross-version:tools` with base project `ppg:staging:tools`, which
fits without changes to the key model. Expected subproject row for
`cross-version`: `base · extras · tools`. The user may also change the
layout further; re-check the real project list on prod before
implementing:

```
ssh <prod-host> 'cd ~/percona-obs-dashboard && python3 -c "import sqlite3; c=sqlite3.connect(\"data/obsboard.db\"); [print(r[0]) for r in c.execute(\"select distinct project from packages where project like \x27ppg:staging:%\x27 order by 1\")]"'
```

## Open questions to resolve on resume

1. Does `staging:tools` (or any other new subproject) change the
   cross-version model, e.g. are there cross-version projects whose own
   `:containers` should NOT be absorbed?
2. Section 3 approval, with the Packages-tab auto-switch rule revised for
   subprojects that do hold packages.
3. Whether PR contexts (`PR:<pr>:ppg:staging:...`) should also surface
   cross-version keys; the shared helper would do so automatically.

## Next step

Resume brainstorming at the open questions, then finish section 3, mark
this spec approved, and invoke the writing-plans skill.
