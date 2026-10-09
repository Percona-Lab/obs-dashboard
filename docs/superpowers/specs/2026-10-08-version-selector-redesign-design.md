# Version selector redesign: two-level chips with a `common` pseudo-version

**Status: approved in brainstorming on 2026-10-09**, superseding the
2026-10-08 draft. The OBS layout changed between the two sessions: the
cross-version projects now live under `ppg:staging:common`, which removes
the need for a reserved pseudo-version token.

## Problem

The version selector (Artifacts bar, Builds board context bar, events
filter) is a flat chip row derived from project paths. It only accepts
numeric segments at the version position, so projects whose version
segment is `common` (`ppg:staging:common:tools`, and soon
`ppg:staging:common:containers`, `ppg:staging:common:extras:containers`,
`ppg:staging:common:tools:containers`) never get a chip and are
unreachable in Artifacts. On the board they leak into every version
because the package and event filters treat any non-numeric segment as
"common tree, always shown".

Adding them as flat chips does not scale: staging already shows ten chips
(`19 18 18:extras 17 17:extras 16 16:extras 16:tde 15 14`), and every new
version or subproject multiplies the row.

## Layout this design targets

```
ppg:staging                                 package-less root
ppg:staging:<V>                             14 15 16 17 18 19
ppg:staging:<V>:containers                  absorbed into <V>
ppg:staging:<V>:tarballs                    absorbed into <V>
ppg:staging:<V>:extras                      key <V>:extras
ppg:staging:<V>:extras:containers           under <V>:extras
ppg:staging:16:tde                          key 16:tde
ppg:staging:common                          package-less, key common
ppg:staging:common:containers               absorbed into common
ppg:staging:common:tools                    key common:tools (packages)
ppg:staging:common:tools:containers         under common:tools
ppg:staging:common:extras:containers        under common:extras
ppg:common:deps, ppg:common:deps:tarballs   shared trees, outside prefix
common:deps:*, common:containers:*          shared trees, outside prefix
```

`ppg:devel` mirrors staging without a `common` yet. `ppg:releases:<V>`
snapshots exist, and `ppg:releases:common` will appear later. Both gain
the `common` key automatically through the shared helper when their
projects show up. Old `ppg:staging:containers` and
`ppg:staging:extras:containers` rows still exist in the production DB and
will disappear when those OBS projects are deleted and the DB rows are
purged. Until then they derive to transient version chips `containers`
and `extras` with no subproject row. This is accepted: a special case to
hide them cannot be told apart from a legitimate future layout, and the
chips vanish with the rows.

## Decisions

- **Shape: two-level chips** (option A of three mockups). A version row
  plus a subproject row that appears only when the selected version has
  more than one entry. Rejected: version chips plus subproject dropdown,
  single grouped dropdown.
- **Containers and tarballs stay absorbed** for every version, `common`
  included. Only real subprojects (extras, tde, tools) get a chip.
- **Pseudo-version is the literal `common` segment.** The chip, the key,
  the URL and the path badge all say `common`. Rejected: a display label
  `cross-version` differing from the key; the name `shared`.
- **Base chip label: `base`.** Rejected: `core`, repeating the version.
- **Scope: everywhere** the shared helper is used (Artifacts, board,
  events filter), so the three views keep scoping identically.

Approved states of the Artifacts bar (PPG Staging):

| Selected key | Version row | Subproject row | Path badge |
|---|---|---|---|
| `common` | `19 18 17 16 15 14 common` | `base · extras · tools` | `ppg:staging:common` |
| `16:tde` | same | `base · extras · tde` | `ppg:staging:16:tde` |
| `19` | same | hidden | `ppg:staging:19` |

Mockups: `.superpowers/brainstorm/534111-1791467148/content/selector-layouts.html`
and `selector-a-v2.html` (drawn with the earlier `cross-version` label).

## Section 1: key model

Keys stay single strings: `18`, `18:extras`, `common`, `common:tools`,
`''` for All on the board. URL state, props and the three filters keep
their shape.

`deriveVersionKeys` in `frontend/src/lib/versions.ts`:

- The segment at the version position may be numeric or non-numeric; no
  segment value is special-cased.
- Plain versus extension is decided exactly as today by the next segment
  and the context's absorbed list. So `ppg:staging:common:containers`
  yields plain `common`, `ppg:staging:common:tools` yields `common:tools`,
  `ppg:staging:common:tools:containers` yields `common:tools`,
  `ppg:staging:common:extras:containers` yields `common:extras`.
- Order: numeric versions descending, then non-numeric versions
  alphabetically. Within a version: plain key first, extensions
  alphabetical. Catch-all contexts (no absorbed list, i.e. Releases) keep
  producing plain keys only, as today.

`matchesVersionKey` needs no change: `common` and `common:tools` resolve
to `prefix:common` and `prefix:common:tools` through the existing code.

A new helper `versionKeyProject(prefix, key)` returns the base project of
a key (`ppg:staging:common`, `ppg:staging:16:tde`). It is used for the
path badge and the Packages-tab rule below.

Behaviour fix in `usePackages.ts` (`sorted`) and `useEvents.ts`
(`matchesEventVersion`): the current "non-numeric segment at the version
position is always shown" rule becomes "a project outside the context
prefix is always shown; a project inside the prefix goes through
`matchesVersionKey`". Outside-prefix projects are the shared trees
(`ppg:common:*`, `common:*`, `PR:<pr>:common`), which `projectInContext`
already recognises. This stops `common` projects from showing under every
version while keeping the shared trees visible everywhere.

## Section 2: selector component

New `frontend/src/components/VersionSelector.vue`, used by
`ContextBar.vue` (board) and `ArtifactsVersionBar.vue` (Artifacts).

Props: `keys: string[]` (flat key list from `deriveVersionKeys`),
`modelValue: string` (selected key), `allowAll: boolean`. Emits
`update:modelValue`.

- Version row: the distinct first tokens of `keys` in list order. When
  `allowAll` is true an `All` chip bound to the empty key follows, as the
  board has today. Artifacts passes `allowAll` false.
- Subproject row: rendered only when the selected version has more than
  one key. It lists `base` for the plain key when it exists, then the
  extension names in list order. Hidden when `All` is selected.
- Clicking a version chip selects that version's plain key if it exists,
  otherwise its first extension. Clicking a subproject chip selects that
  key. A selected key is therefore always one of `keys`, so the existing
  snap-to-first watchers in `App.vue` and `ArtifactsPanel.vue` are
  unchanged.
- The monospace path badge in both bars shows
  `versionKeyProject(prefix, key)` (prefix alone when the key is empty).
- Both bars drop their inline chip loops. The chip styles come from the
  Artifacts bar (the Tailwind classes), replacing the board's inline style
  strings.

## Section 3: fetching, tab behaviour, backend, testing

- Repos fetch in `ArtifactsPanel.vue` is unchanged: `common` is a real
  path segment, so `/api/products/ppg/staging/common/repos?subproject=tools`
  already builds the right prefix.
- Packages tab: when the selected key changes and the active tab is
  Packages, and no non-container package matches the new key, the panel
  switches to Containers. It never switches back on its own. This covers
  `common` base (package-less, images only) but not `common:tools`.
- Backend: no API or store changes. The overview already splits these
  projects into their own rows (`ppg:staging:common:tools` and
  `ppg:staging:common:extras` rows via `logicalProject`, commit f8e2aee).
- Error handling: unknown or stale URL keys fall through the existing
  snap-to-first watchers. An empty `keys` list hides the selector, as
  today.
- Testing: add Vitest as a dev dependency with a `test` script (the
  frontend currently has only compile-time `.test-d.ts` checks). Unit
  tests cover `deriveVersionKeys` (numeric, common, ordering, catch-all), `versionKeyProject`, the revised package and event
  filter rules, and the selector's row splitting and click mapping via
  `@vue/test-utils`. Then the production build and a visual check on prod
  of the three approved states.

## Out of scope

- Any change to the overview tables, backend classifier, or tags.
- Removing the legacy `ppg:staging:containers` rows from the DB.
- PR contexts: they pick up `common` keys automatically through the
  shared helper if a PR ever carries such projects; no PR-specific work.

## Next step

Invoke the writing-plans skill against this spec.
