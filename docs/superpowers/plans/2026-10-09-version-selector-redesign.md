# Version Selector Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the flat version chip row with a two-level selector (version row plus subproject row) that also surfaces the `common` pseudo-version, in the Artifacts bar, the Builds board bar, and the events filter.

**Architecture:** All key logic lives in `frontend/src/lib/versions.ts` as pure functions (derive keys, map a key to its project, split keys into selector rows). A new `VersionSelector.vue` renders those rows and is dropped into both bars. The board and events filters stop special-casing non-numeric segments and instead treat only projects outside the context prefix as always visible. No backend changes.

**Tech Stack:** Vue 3 (`<script setup>`, Composition API), TypeScript, Tailwind classes, Vite 5. Vitest 2 with `@vue/test-utils` and jsdom is added for unit tests. Go backend is untouched.

**Spec:** `docs/superpowers/specs/2026-10-08-version-selector-redesign-design.md`

## Global Constraints

- Keys stay single strings: `18`, `18:extras`, `common`, `common:tools`, `''` meaning All on the board. No new URL params, props, or types for the key.
- `containers` and `tarballs` remain absorbed into the plain key for every version including `common`; only other subprojects produce extension keys.
- Key order: numeric versions descending, then non-numeric versions alphabetically; within a version the plain key first, then extensions alphabetically.
- Projects outside the context prefix (`ppg:common:*`, `common:*`, `PR:<pr>:common*`, and the prefix root itself) are always shown on the board and in events, regardless of the selected key.
- The selector's chip styling is the Artifacts bar's existing Tailwind classes (listed in Task 4); the board's inline `tabStyle` strings are removed.
- Every commit uses `git commit -s`, never a `Co-Authored-By` trailer (CLAUDE.md).
- Run frontend commands from `frontend/`. `npm test` must pass and `npm run build` must succeed at the end of every task that touches the frontend.

**User decisions (already made):**
- Two-level chips (version row + subproject row), not a dropdown.
- The pseudo-version is the literal `common` segment; chip, key, URL, and path badge all read `common`.
- The base chip is labelled `base`.
- Scope is all three views: Artifacts, board, events.
- Legacy `ppg:staging:containers` / `ppg:staging:extras:containers` rows may show transient `containers` / `extras` chips until purged; no special case.
- Packages tab auto-switches to Containers only when the selected key has no non-container packages; it never switches back on its own.
- The production deploy is run by the user over SSH, not by the agent (permission classifier blocks it).

---

## File Structure

| File | Responsibility |
|---|---|
| `frontend/package.json`, `frontend/vite.config.ts` | Add Vitest, jsdom, test-utils; `npm test` script; vitest `include` |
| `frontend/src/lib/versions.ts` | Pure key logic: `splitVersionKey`, `deriveVersionKeys` (now prefix-based, accepts non-numeric), `matchesVersionKey` (unchanged), new `versionKeyProject`, new `selectorVersions`, new `keyForVersion` |
| `frontend/src/lib/versions.test.ts` | Unit tests for the above |
| `frontend/src/lib/project.ts` | New `isUnderPrefix(project, prefix)` |
| `frontend/src/composables/usePackages.ts` | Prefix-based always-shown rule; new derive signature |
| `frontend/src/composables/useEvents.ts` | Prefix-based always-shown rule |
| `frontend/src/composables/usePackages.test.ts`, `useEvents.test.ts` | Filter rule tests |
| `frontend/src/components/VersionSelector.vue` | Two-row chip selector |
| `frontend/src/components/VersionSelector.test.ts` | Row rendering and click mapping |
| `frontend/src/components/ContextBar.vue` | Use `VersionSelector` with All; path badge via `versionKeyProject`; drop `tabStyle` |
| `frontend/src/components/ArtifactsVersionBar.vue` | Use `VersionSelector` without All; path badge via `versionKeyProject` |
| `frontend/src/components/ArtifactsPanel.vue` | New derive signature; Packages-tab auto-switch |
| `frontend/src/composables/useArtifacts.ts` | New pure `keyHasPackages` helper |
| `frontend/src/composables/useArtifacts.test.ts` | Test for `keyHasPackages` |

---

### Task 0: Add Vitest to the frontend

**Goal:** A working `npm test` in `frontend/` that runs `src/**/*.test.ts` files, proven by one trivial test against an existing pure function.

**Files:**
- Modify: `frontend/package.json`
- Modify: `frontend/vite.config.ts`
- Create: `frontend/src/lib/versions.test.ts`

**Acceptance Criteria:**
- [ ] `npm test` runs Vitest once (not watch mode) and exits 0 with 1 passing test.
- [ ] The existing `*.test-d.ts` compile-time files are not picked up by Vitest.
- [ ] `npm run build` still succeeds.

**Verify:** `cd frontend && npm test` → `Test Files  1 passed`, `Tests  1 passed`; `npm run build` → `✓ built`.

**Steps:**

- [ ] **Step 1: Install dev dependencies**

Run from `frontend/`:

```bash
npm install -D vitest@^2 @vue/test-utils@^2 jsdom@^25
```

- [ ] **Step 2: Add the test script**

In `frontend/package.json` `scripts`, add after `"preview"`:

```json
"test": "vitest run"
```

- [ ] **Step 3: Configure Vitest in vite.config.ts**

Replace the file with:

```ts
/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    host: '0.0.0.0',
    proxy: {
      '/api': {
        target: 'http://backend:4000',
        changeOrigin: true,
      },
    },
  },
  test: {
    // Only *.test.ts files; the existing *.test-d.ts files are compile-time
    // type checks run by vue-tsc, not Vitest suites.
    include: ['src/**/*.test.ts'],
  },
})
```

- [ ] **Step 4: Write a smoke test for an existing function**

Create `frontend/src/lib/versions.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { splitVersionKey } from './versions'

describe('splitVersionKey', () => {
  it('splits an extension key and leaves a plain key whole', () => {
    expect(splitVersionKey('17:extras')).toEqual(['17', 'extras'])
    expect(splitVersionKey('17')).toEqual(['17', undefined])
  })
})
```

- [ ] **Step 5: Run the test and the build**

Run: `npm test`
Expected: `Tests  1 passed (1)`

Run: `npm run build`
Expected: `✓ built in …`

- [ ] **Step 6: Commit**

```bash
git add frontend/package.json frontend/package-lock.json frontend/vite.config.ts frontend/src/lib/versions.test.ts
git commit -s -m "test(frontend): add Vitest with a versions smoke test"
```

```json:metadata
{"files": ["frontend/package.json", "frontend/vite.config.ts", "frontend/src/lib/versions.test.ts"], "verifyCommand": "cd frontend && npm test && npm run build", "acceptanceCriteria": ["npm test runs once and passes 1 test", "*.test-d.ts files are not collected", "npm run build succeeds"], "modelTier": "mechanical"}
```

---

### Task 1: Key derivation accepts the `common` pseudo-version

**Goal:** `deriveVersionKeys` takes the context prefix, ignores projects outside it, accepts non-numeric version segments, and orders numeric versions first; plus `versionKeyProject` and the selector row helpers, all unit tested.

**Files:**
- Modify: `frontend/src/lib/versions.ts`
- Modify: `frontend/src/lib/versions.test.ts`
- Modify: `frontend/src/composables/usePackages.ts:55-59` (call site)
- Modify: `frontend/src/components/ArtifactsPanel.vue:79-86` (call site)

**Acceptance Criteria:**
- [ ] `deriveVersionKeys(projects, 'ppg:staging', ['containers','tarballs'])` on the staging corpus returns `['19','18','18:extras','17','17:extras','16','16:extras','16:tde','15','14','common','common:extras','common:tools']`.
- [ ] Projects outside the prefix (`ppg:common:deps`, `common:deps:build`) contribute no key.
- [ ] Catch-all contexts (absorbed `undefined`) still yield plain keys only.
- [ ] `versionKeyProject('ppg:staging', 'common:tools')` is `ppg:staging:common:tools`; empty key returns the prefix.
- [ ] `selectorVersions` and `keyForVersion` behave as specified in the tests below.
- [ ] `npm test` and `npm run build` pass; the two call sites compile with the new signature.

**Verify:** `cd frontend && npm test && npm run build`

**Steps:**

- [ ] **Step 1: Write the failing tests**

Replace `frontend/src/lib/versions.test.ts` with:

```ts
import { describe, it, expect } from 'vitest'
import {
  splitVersionKey,
  deriveVersionKeys,
  matchesVersionKey,
  versionKeyProject,
  selectorVersions,
  keyForVersion,
} from './versions'

const ABSORBED = ['containers', 'tarballs']

// Mirrors production ppg:staging on 2026-10-09 plus the shared trees that
// usePackages receives in the same corpus.
const STAGING = [
  'ppg:staging:14', 'ppg:staging:14:containers', 'ppg:staging:14:tarballs',
  'ppg:staging:15', 'ppg:staging:15:containers', 'ppg:staging:15:tarballs',
  'ppg:staging:16', 'ppg:staging:16:containers', 'ppg:staging:16:extras',
  'ppg:staging:16:extras:containers', 'ppg:staging:16:tarballs', 'ppg:staging:16:tde',
  'ppg:staging:17', 'ppg:staging:17:containers', 'ppg:staging:17:extras',
  'ppg:staging:17:extras:containers', 'ppg:staging:17:tarballs',
  'ppg:staging:18', 'ppg:staging:18:containers', 'ppg:staging:18:extras',
  'ppg:staging:18:extras:containers', 'ppg:staging:18:tarballs',
  'ppg:staging:19',
  'ppg:staging:common:containers', 'ppg:staging:common:tools',
  'ppg:staging:common:tools:containers', 'ppg:staging:common:extras:containers',
  'ppg:common:deps', 'ppg:common:deps:tarballs',
  'common:deps:build', 'common:containers:ubi9',
]

describe('splitVersionKey', () => {
  it('splits an extension key and leaves a plain key whole', () => {
    expect(splitVersionKey('17:extras')).toEqual(['17', 'extras'])
    expect(splitVersionKey('17')).toEqual(['17', undefined])
  })
})

describe('deriveVersionKeys', () => {
  it('derives numeric versions first, then common, each with plain key before extensions', () => {
    expect(deriveVersionKeys(STAGING, 'ppg:staging', ABSORBED)).toEqual([
      '19', '18', '18:extras', '17', '17:extras', '16', '16:extras', '16:tde', '15', '14',
      'common', 'common:extras', 'common:tools',
    ])
  })

  it('ignores projects outside the prefix', () => {
    expect(deriveVersionKeys(['ppg:common:deps', 'common:deps:build', 'ppg:staging'], 'ppg:staging', ABSORBED)).toEqual([])
  })

  it('folds absorbed subprojects into the plain key for common too', () => {
    expect(deriveVersionKeys(['ppg:staging:common:containers'], 'ppg:staging', ABSORBED)).toEqual(['common'])
  })

  it('yields an extension when a non-numeric version has a non-absorbed subproject', () => {
    expect(deriveVersionKeys(['ppg:staging:common:tools'], 'ppg:staging', ABSORBED)).toEqual(['common:tools'])
  })

  it('keeps catch-all contexts on plain keys only', () => {
    expect(deriveVersionKeys(['ppg:releases:18:extras', 'ppg:releases:17'], 'ppg:releases', undefined)).toEqual(['18', '17'])
  })

  it('surfaces legacy version-less rows as plain chips (accepted transient)', () => {
    expect(deriveVersionKeys(['ppg:staging:containers', 'ppg:staging:extras:containers'], 'ppg:staging', ABSORBED))
      .toEqual(['containers', 'extras'])
  })
})

describe('matchesVersionKey with common keys', () => {
  it('matches the common base and its absorbed containers', () => {
    expect(matchesVersionKey('ppg:staging:common', 'ppg:staging', 'common', ABSORBED)).toBe(true)
    expect(matchesVersionKey('ppg:staging:common:containers', 'ppg:staging', 'common', ABSORBED)).toBe(true)
    expect(matchesVersionKey('ppg:staging:common:tools', 'ppg:staging', 'common', ABSORBED)).toBe(false)
  })

  it('matches the whole subtree of a common extension', () => {
    expect(matchesVersionKey('ppg:staging:common:tools:containers', 'ppg:staging', 'common:tools', ABSORBED)).toBe(true)
    expect(matchesVersionKey('ppg:staging:common:extras:containers', 'ppg:staging', 'common:extras', ABSORBED)).toBe(true)
    expect(matchesVersionKey('ppg:staging:18', 'ppg:staging', 'common:tools', ABSORBED)).toBe(false)
  })
})

describe('versionKeyProject', () => {
  it('maps keys to their base project and the empty key to the prefix', () => {
    expect(versionKeyProject('ppg:staging', '18')).toBe('ppg:staging:18')
    expect(versionKeyProject('ppg:staging', '16:tde')).toBe('ppg:staging:16:tde')
    expect(versionKeyProject('ppg:staging', 'common')).toBe('ppg:staging:common')
    expect(versionKeyProject('ppg:staging', 'common:tools')).toBe('ppg:staging:common:tools')
    expect(versionKeyProject('ppg:staging', '')).toBe('ppg:staging')
  })
})

describe('selector rows', () => {
  const KEYS = ['19', '18', '18:extras', '16', '16:extras', '16:tde', 'common', 'common:extras', 'common:tools']

  it('lists distinct versions in key order', () => {
    expect(selectorVersions(KEYS)).toEqual(['19', '18', '16', 'common'])
  })

  it('picks the plain key for a version when it exists, else the first extension', () => {
    expect(keyForVersion(KEYS, '18')).toBe('18')
    expect(keyForVersion(['18:extras', '18:tde'], '18')).toBe('18:extras')
    expect(keyForVersion(KEYS, '99')).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `npm test`
Expected: FAIL. `deriveVersionKeys` tests fail on the second argument being a string; `versionKeyProject`, `selectorVersions`, `keyForVersion` are "not a function" / not exported.

- [ ] **Step 3: Implement in versions.ts**

Replace `frontend/src/lib/versions.ts` with:

```ts
// Version-key model for the <version>[:<subproject>] selector: subprojects
// of a version (extras, tde, tools, …) are "version extensions" with their
// own selector entries, while absorbed subprojects (containers, tarballs)
// fold into the plain version entry. The version segment is usually a
// major number but may be a literal such as "common" (the cross-version
// projects under ppg:staging:common). Shared by the board (usePackages),
// the artifacts panel, and the events filter so all three scope
// identically.

/** Split "17:extras" into ["17", "extras"]; "17" into ["17", undefined]. */
export function splitVersionKey(key: string): [string, string | undefined] {
  const idx = key.indexOf(':')
  return idx < 0 ? [key, undefined] : [key.slice(0, idx), key.slice(idx + 1)]
}

const isNumeric = (s: string): boolean => /^\d+$/.test(s)

/** Numeric versions first (descending), then non-numeric alphabetically. */
function compareVersions(a: string, b: string): number {
  const an = isNumeric(a)
  const bn = isNumeric(b)
  if (an && bn) return parseInt(b, 10) - parseInt(a, 10)
  if (an !== bn) return an ? -1 : 1
  return a.localeCompare(b)
}

/** Derive selector keys from project paths under prefix. Projects outside
 *  prefix (shared common trees) contribute nothing. absorbed lists the
 *  subprojects folded into the plain version entry (undefined = catch-all
 *  context: plain keys only). Order: numeric versions descending, then
 *  non-numeric alphabetically; within a version the plain key first, then
 *  extensions alphabetical. */
export function deriveVersionKeys(
  projects: Iterable<string>,
  prefix: string,
  absorbed: string[] | undefined,
): string[] {
  const depth = prefix.split(':').length
  const plain = new Set<string>()
  const extensions = new Set<string>()
  for (const project of projects) {
    if (!project.startsWith(prefix + ':')) continue
    const parts = project.split(':')
    const ver = parts[depth]
    if (!ver) continue
    const sub = parts[depth + 1]
    if (!sub || absorbed === undefined || absorbed.includes(sub)) {
      plain.add(ver)
    } else {
      extensions.add(`${ver}:${sub}`)
    }
  }
  const versions = new Set<string>(plain)
  for (const ext of extensions) versions.add(splitVersionKey(ext)[0])
  const keys: string[] = []
  for (const ver of [...versions].sort(compareVersions)) {
    if (plain.has(ver)) keys.push(ver)
    keys.push(...[...extensions].filter(e => e.startsWith(ver + ':')).sort())
  }
  return keys
}

/** Does a project belong under prefix at the selected version key?
 *  Plain key "17": the version root plus absorbed subprojects only (when
 *  absorbed is defined; catch-all contexts match the whole subtree).
 *  Extension key "17:extras": the prefix:17:extras subtree (catch-all,
 *  containers beneath included). */
export function matchesVersionKey(
  project: string,
  prefix: string,
  key: string,
  absorbed: string[] | undefined,
): boolean {
  const [ver, sub] = splitVersionKey(key)
  const base = sub ? `${prefix}:${ver}:${sub}` : `${prefix}:${ver}`
  if (project === base) return true
  if (!project.startsWith(base + ':')) return false
  if (sub || absorbed === undefined) return true
  const first = project.slice(base.length + 1).split(':')[0]
  return absorbed.includes(first)
}

/** The base OBS project a key stands for: "ppg:staging:16:tde" for
 *  "16:tde", "ppg:staging:common" for "common", prefix for the empty key. */
export function versionKeyProject(prefix: string, key: string): string {
  if (!key) return prefix
  const [ver, sub] = splitVersionKey(key)
  return sub ? `${prefix}:${ver}:${sub}` : `${prefix}:${ver}`
}

/** Distinct version tokens of a key list, in list order (the selector's
 *  first row). */
export function selectorVersions(keys: string[]): string[] {
  const out: string[] = []
  for (const key of keys) {
    const ver = splitVersionKey(key)[0]
    if (!out.includes(ver)) out.push(ver)
  }
  return out
}

/** The key selected when a version chip is clicked: the plain key when it
 *  exists, otherwise the version's first extension (keys are ordered plain
 *  first). undefined when the version has no keys. */
export function keyForVersion(keys: string[], version: string): string | undefined {
  return keys.find(k => splitVersionKey(k)[0] === version)
}
```

- [ ] **Step 4: Update the two call sites**

In `frontend/src/composables/usePackages.ts`, replace the `availableVersions` computed body:

```ts
  const availableVersions: ComputedRef<string[]> = computed(() => {
    const ctx = toValue(context)
    return deriveVersionKeys(
      data.value.map(p => p.project),
      ctx.prefix,
      ctx.allowedSubprojects,
    )
  })
```

In `frontend/src/components/ArtifactsPanel.vue`, replace the `availableVersions` computed body:

```ts
const availableVersions = computed<string[]>(() => {
  const ctx = props.artifactsContext
  return deriveVersionKeys(
    artifactsPackages.value.map(p => p.project),
    ctx.prefix,
    ctx.allowedSubprojects,
  )
})
```

- [ ] **Step 5: Run tests and build**

Run: `npm test`
Expected: all tests pass.

Run: `npm run build`
Expected: `✓ built`.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/lib/versions.ts frontend/src/lib/versions.test.ts frontend/src/composables/usePackages.ts frontend/src/components/ArtifactsPanel.vue
git commit -s -m "feat(versions): derive common pseudo-version keys and selector row helpers"
```

```json:metadata
{"files": ["frontend/src/lib/versions.ts", "frontend/src/lib/versions.test.ts", "frontend/src/composables/usePackages.ts", "frontend/src/components/ArtifactsPanel.vue"], "verifyCommand": "cd frontend && npm test && npm run build", "acceptanceCriteria": ["staging corpus derives the 13 keys in spec order", "projects outside the prefix contribute no key", "catch-all contexts yield plain keys only", "versionKeyProject maps keys to base projects", "selectorVersions/keyForVersion match tests"], "modelTier": "mechanical"}
```

---

### Task 2: Board and events scope `common` projects to their key

**Goal:** `usePackages` and `useEvents` always show projects outside the context prefix and run every project inside the prefix through `matchesVersionKey`, so `ppg:staging:common:*` stops appearing under every version.

**Files:**
- Modify: `frontend/src/lib/project.ts`
- Modify: `frontend/src/composables/usePackages.ts:62-76`
- Modify: `frontend/src/composables/useEvents.ts:41-47`
- Create: `frontend/src/composables/usePackages.test.ts`
- Create: `frontend/src/composables/useEvents.test.ts`

**Acceptance Criteria:**
- [ ] With key `18` on staging, a `ppg:staging:common:tools` package is hidden and a `ppg:common:deps` package is shown.
- [ ] With key `common:tools`, `ppg:staging:common:tools` and `ppg:staging:common:tools:containers` packages are shown, `ppg:staging:18` is hidden, `ppg:common:deps` is shown.
- [ ] With the empty key everything non-release is shown.
- [ ] Events follow the same rules, and an event on the prefix root `ppg:staging` is always shown.
- [ ] `npm test` and `npm run build` pass.

**Verify:** `cd frontend && npm test && npm run build`

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `frontend/src/composables/usePackages.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { ref } from 'vue'
import { usePackages } from './usePackages'
import { PPG_STAGING_CONTEXT } from '../lib/contexts'
import type { Package } from '../types/api'

function pkg(project: string, extra: Partial<Package> = {}): Package {
  return {
    project,
    name: project.replace(/:/g, '-'),
    rollup_state: 'succeeded',
    ok_targets: 1,
    total_targets: 1,
    targets: [],
    updated_at: '2026-10-09T00:00:00Z',
    ...extra,
  } as Package
}

const CORPUS = [
  pkg('ppg:staging:18'),
  pkg('ppg:staging:18:containers', { is_container: true }),
  pkg('ppg:staging:18:extras'),
  pkg('ppg:staging:common:tools'),
  pkg('ppg:staging:common:tools:containers', { is_container: true }),
  pkg('ppg:common:deps'),
  pkg('common:deps:build'),
]

function projectsFor(key: string): string[] {
  const version = ref(key)
  const { data, rawData } = usePackages('/api/products/ppg/staging', version, ref(PPG_STAGING_CONTEXT))
  rawData.value = CORPUS
  return data.value.map(p => p.project).sort()
}

describe('usePackages version scoping', () => {
  it('hides common projects under a numeric key but keeps shared trees', () => {
    expect(projectsFor('18')).toEqual([
      'common:deps:build', 'ppg:common:deps', 'ppg:staging:18', 'ppg:staging:18:containers',
    ])
  })

  it('shows only the common:tools subtree plus shared trees under common:tools', () => {
    expect(projectsFor('common:tools')).toEqual([
      'common:deps:build', 'ppg:common:deps', 'ppg:staging:common:tools', 'ppg:staging:common:tools:containers',
    ])
  })

  it('shows everything under the empty key', () => {
    expect(projectsFor('')).toHaveLength(CORPUS.length)
  })
})
```

Create `frontend/src/composables/useEvents.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { ref } from 'vue'
import { useEvents } from './useEvents'
import { PPG_STAGING_CONTEXT } from '../lib/contexts'
import type { Event } from '../types/api'

function ev(project: string): Event {
  return { project, package: 'p', tags: [] } as unknown as Event
}

const CORPUS = [
  ev('ppg:staging'),
  ev('ppg:staging:18'),
  ev('ppg:staging:18:extras'),
  ev('ppg:staging:common:tools'),
  ev('ppg:staging:common:tools:containers'),
  ev('ppg:common:deps'),
  ev('common:deps:build'),
]

function projectsFor(key: string): string[] {
  const { data, filterEvents } = useEvents('/api/products/ppg/staging', ref(key))
  data.value = CORPUS
  return filterEvents([], key, PPG_STAGING_CONTEXT).map(e => e.project).sort()
}

describe('useEvents version scoping', () => {
  it('hides common projects under a numeric key but keeps the root and shared trees', () => {
    expect(projectsFor('18')).toEqual([
      'common:deps:build', 'ppg:common:deps', 'ppg:staging', 'ppg:staging:18',
    ])
  })

  it('shows the common:tools subtree under common:tools', () => {
    expect(projectsFor('common:tools')).toEqual([
      'common:deps:build', 'ppg:common:deps', 'ppg:staging', 'ppg:staging:common:tools', 'ppg:staging:common:tools:containers',
    ])
  })

  it('shows everything under the empty key', () => {
    expect(projectsFor('')).toHaveLength(CORPUS.length)
  })
})
```

Before running, check the `Event` type's required field names in `frontend/src/types/api.ts` (search `export interface Event`). The cast via `unknown` keeps the test compiling whatever they are; only `project`, `tags`, and `instance` are read by the filter.

- [ ] **Step 2: Run the tests to see them fail**

Run: `npm test`
Expected: FAIL. The numeric-key cases include `ppg:staging:common:tools` and `ppg:staging:common:tools:containers` because the old rule treats the non-numeric `common` segment as always shown.

- [ ] **Step 3: Add isUnderPrefix to project.ts**

Append to `frontend/src/lib/project.ts`:

```ts
// Is project strictly below prefix (prefix:<more>)? The prefix root itself
// and anything outside it (shared common trees) are not under the prefix,
// and the version filters always show those.
export function isUnderPrefix(project: string, prefix: string): boolean {
  return project.startsWith(prefix + ':')
}
```

- [ ] **Step 4: Apply the rule in usePackages**

In `frontend/src/composables/usePackages.ts`, add the import:

```ts
import { isUnderPrefix } from '../lib/project'
```

Replace the `sorted` computed's filter callback with:

```ts
      .filter(pkg => {
        if (pkg.is_release) return false
        if (!ver) return true
        // Shared common trees live outside the context prefix and are
        // always shown; everything under the prefix scopes to the key.
        if (!isUnderPrefix(pkg.project, ctx.prefix)) return true
        return matchesVersionKey(pkg.project, ctx.prefix, ver, ctx.allowedSubprojects)
      })
```

Remove the now-unused `depth` constant above it if TypeScript flags it under `noUnusedLocals`.

- [ ] **Step 5: Apply the rule in useEvents**

In `frontend/src/composables/useEvents.ts`, extend the existing import from `../lib/project` to include `isUnderPrefix` (it already imports `projectInContext`), then replace `matchesEventVersion`:

```ts
  function matchesEventVersion(event: Event, key: string, ctx: Context): boolean {
    if (!key) return true
    // The prefix root (project-level events) and the shared common trees
    // are always shown; projects under the prefix scope to the key.
    if (!isUnderPrefix(event.project, ctx.prefix)) return true
    return matchesVersionKey(event.project, ctx.prefix, key, ctx.allowedSubprojects)
  }
```

- [ ] **Step 6: Run tests and build**

Run: `npm test`
Expected: all pass.

Run: `npm run build`
Expected: `✓ built`.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/lib/project.ts frontend/src/composables/usePackages.ts frontend/src/composables/useEvents.ts frontend/src/composables/usePackages.test.ts frontend/src/composables/useEvents.test.ts
git commit -s -m "fix(board): scope common projects to their version key"
```

```json:metadata
{"files": ["frontend/src/lib/project.ts", "frontend/src/composables/usePackages.ts", "frontend/src/composables/useEvents.ts", "frontend/src/composables/usePackages.test.ts", "frontend/src/composables/useEvents.test.ts"], "verifyCommand": "cd frontend && npm test && npm run build", "acceptanceCriteria": ["key 18 hides ppg:staging:common:tools and shows ppg:common:deps", "key common:tools shows its subtree and shared trees only", "empty key shows all", "events: prefix root always shown"], "modelTier": "standard"}
```

---

### Task 3: VersionSelector component

**Goal:** A `VersionSelector.vue` that renders the version row (optionally with All) and the conditional subproject row, emitting a valid key on every click, with component tests.

**Files:**
- Create: `frontend/src/components/VersionSelector.vue`
- Create: `frontend/src/components/VersionSelector.test.ts`

**Acceptance Criteria:**
- [ ] Version row shows `selectorVersions(keys)`; with `allowAll` an extra `All` chip follows.
- [ ] Subproject row renders only when the selected version has more than one key, labelled `base` for the plain key and the subproject name otherwise. Hidden when the empty key is selected.
- [ ] Clicking a version chip emits `keyForVersion(keys, version)`; clicking a subproject chip emits that key; clicking All emits `''`.
- [ ] Chip classes match the Artifacts bar's existing ones (listed in Step 3).
- [ ] `npm test` and `npm run build` pass.

**Verify:** `cd frontend && npm test && npm run build`

**Steps:**

- [ ] **Step 1: Write the failing component test**

Create `frontend/src/components/VersionSelector.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import VersionSelector from './VersionSelector.vue'

const KEYS = ['19', '18', '18:extras', '16', '16:extras', '16:tde', 'common', 'common:extras', 'common:tools']

function chips(wrapper: ReturnType<typeof mount>, row: 'version' | 'subproject'): string[] {
  return wrapper.findAll(`[data-row="${row}"] button`).map(b => b.text())
}

describe('VersionSelector', () => {
  it('renders distinct versions and no subproject row for a lone version', () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: '19' } })
    expect(chips(w, 'version')).toEqual(['19', '18', '16', 'common'])
    expect(w.find('[data-row="subproject"]').exists()).toBe(false)
  })

  it('renders base plus extensions for the selected version', () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: '16:tde' } })
    expect(chips(w, 'subproject')).toEqual(['base', 'extras', 'tde'])
  })

  it('renders base, extras, tools for common', () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: 'common' } })
    expect(chips(w, 'subproject')).toEqual(['base', 'extras', 'tools'])
  })

  it('appends an All chip when allowAll and hides subprojects while All is selected', () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: '', allowAll: true } })
    expect(chips(w, 'version')).toEqual(['19', '18', '16', 'common', 'All'])
    expect(w.find('[data-row="subproject"]').exists()).toBe(false)
  })

  it('emits the plain key when a version chip is clicked', async () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: '19' } })
    await w.findAll('[data-row="version"] button')[1].trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([['18']])
  })

  it('emits the first extension when the version has no plain key', async () => {
    const w = mount(VersionSelector, { props: { keys: ['19', '18:extras', '18:tde'], modelValue: '19' } })
    await w.findAll('[data-row="version"] button')[1].trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([['18:extras']])
  })

  it('emits the subproject key and the empty key for All', async () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: 'common', allowAll: true } })
    await w.findAll('[data-row="subproject"] button')[2].trigger('click')
    await w.findAll('[data-row="version"] button')[4].trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([['common:tools'], ['']])
  })

  it('marks the selected version and subproject chips as active', () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: '16:tde' } })
    const active = w.findAll('button.font-bold').map(b => b.text())
    expect(active).toEqual(['16', 'tde'])
  })
})
```

- [ ] **Step 2: Run the test to see it fail**

Run: `npm test`
Expected: FAIL with "Failed to resolve import ./VersionSelector.vue".

- [ ] **Step 3: Create the component**

Create `frontend/src/components/VersionSelector.vue`:

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { splitVersionKey, selectorVersions, keyForVersion } from '../lib/versions'

// Two-level version selector: a version row (major numbers, then literal
// pseudo-versions such as "common", optionally followed by All) and a
// subproject row for the selected version, shown only when it has more
// than one key. Keys are the flat list from deriveVersionKeys; the emitted
// value is always one of them (or '' for All).
const props = defineProps<{
  keys: string[]
  modelValue: string
  allowAll?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [key: string]
}>()

const versions = computed(() => selectorVersions(props.keys))
const selectedVersion = computed(() => (props.modelValue ? splitVersionKey(props.modelValue)[0] : ''))
const subKeys = computed(() => props.keys.filter(k => splitVersionKey(k)[0] === selectedVersion.value))
const showSubprojects = computed(() => selectedVersion.value !== '' && subKeys.value.length > 1)

function subLabel(key: string): string {
  return splitVersionKey(key)[1] ?? 'base'
}

function selectVersion(version: string) {
  const key = keyForVersion(props.keys, version)
  if (key !== undefined) emit('update:modelValue', key)
}

const CHIP = 'px-3 py-1 rounded-[7px] border text-[13px] cursor-pointer [font-family:inherit]'
const ACTIVE = 'bg-bg-card text-text-primary font-bold border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)]'
const INACTIVE = 'bg-transparent text-text-muted font-medium border-transparent'

function chipClass(active: boolean): string {
  return `${CHIP} ${active ? ACTIVE : INACTIVE}`
}
</script>

<template>
  <div class="flex items-center gap-2 sm:gap-4 flex-wrap">
    <div class="flex items-center gap-[6px]" data-row="version">
      <span class="text-[11px] text-text-muted font-semibold uppercase [letter-spacing:0.06em] mr-[2px]">Version</span>
      <div class="flex gap-[3px] bg-bg-muted p-[3px] rounded-[9px] border border-border">
        <button
          v-for="v in versions"
          :key="v"
          type="button"
          :class="chipClass(v === selectedVersion)"
          @click="selectVersion(v)"
        >{{ v }}</button>
        <button
          v-if="allowAll"
          type="button"
          :class="chipClass(modelValue === '')"
          @click="emit('update:modelValue', '')"
        >All</button>
      </div>
    </div>

    <div v-if="showSubprojects" class="flex items-center gap-[6px]" data-row="subproject">
      <span class="text-[11px] text-text-muted font-semibold uppercase [letter-spacing:0.06em] mr-[2px]">Subproject</span>
      <div class="flex gap-[3px] bg-bg-muted p-[3px] rounded-[9px] border border-border">
        <button
          v-for="k in subKeys"
          :key="k"
          type="button"
          :class="chipClass(k === modelValue)"
          @click="emit('update:modelValue', k)"
        >{{ subLabel(k) }}</button>
      </div>
    </div>
  </div>
</template>
```

- [ ] **Step 4: Run tests and build**

Run: `npm test`
Expected: all pass.

Run: `npm run build`
Expected: `✓ built`.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/VersionSelector.vue frontend/src/components/VersionSelector.test.ts
git commit -s -m "feat(ui): add two-level VersionSelector component"
```

```json:metadata
{"files": ["frontend/src/components/VersionSelector.vue", "frontend/src/components/VersionSelector.test.ts"], "verifyCommand": "cd frontend && npm test && npm run build", "acceptanceCriteria": ["version row lists distinct versions plus All when allowed", "subproject row only with >1 key, base label for plain key", "clicks emit plain key, first extension, subproject key, or empty for All", "selected chips carry font-bold"], "modelTier": "standard"}
```

---

### Task 4: Wire the selector into both bars

**Goal:** `ContextBar.vue` and `ArtifactsVersionBar.vue` render `VersionSelector` instead of their inline chip loops, and their path badges show the selected key's base project.

**Files:**
- Modify: `frontend/src/components/ContextBar.vue:44-50` (remove `tabStyle`), `:69-92` (badge and version tabs)
- Modify: `frontend/src/components/ArtifactsVersionBar.vue:1-19` (imports), `:26-29` (badge), `:45-59` (version chips)

**Acceptance Criteria:**
- [ ] Board bar: `VersionSelector` with `allow-all`, bound to `version`, emitting `update:version`. The single-context `<code>` badge shows `versionKeyProject(selectedContext.prefix, version)`. `tabStyle` is deleted; `tagStyle` stays.
- [ ] Artifacts bar: `VersionSelector` without All, bound to `version`, emitting `update:version`. The single-context `<code>` badge shows `versionKeyProject(selectedContext.prefix, version)`. The tab switcher keeps `ml-auto` on the right.
- [ ] Both bars hide the selector when `availableVersions` is empty, as before.
- [ ] `npm test` and `npm run build` pass. `vue-tsc` reports no unused symbols.

**Verify:** `cd frontend && npm test && npm run build`

**Steps:**

- [ ] **Step 1: Update ContextBar.vue script**

Add imports at the top of the `<script setup>` block:

```ts
import VersionSelector from './VersionSelector.vue'
import { versionKeyProject } from '../lib/versions'
```

Delete the whole `tabStyle` function (the one returning the two inline style strings for version tabs). Keep `tagStyle`.

- [ ] **Step 2: Update ContextBar.vue template**

Replace the single-context `<code>` badge:

```vue
      <code
        v-else
        class="[font-family:var(--font-mono)] text-[12.5px] text-text-secondary bg-bg-muted px-[10px] py-[5px] rounded-[7px]"
      >{{ versionKeyProject(selectedContext.prefix, version) }}</code>
```

Replace the entire `<!-- Version tabs ... -->` block (the `div` containing the `Version` label, the `v-for` buttons and the `All` button) with:

```vue
      <!-- Version selector: hidden when no versioned packages exist in the context -->
      <VersionSelector
        v-if="availableVersions.length > 0"
        :keys="availableVersions"
        :model-value="version"
        allow-all
        @update:model-value="emit('update:version', $event)"
      />
```

- [ ] **Step 3: Update ArtifactsVersionBar.vue**

Add imports to the `<script setup>` block:

```ts
import VersionSelector from './VersionSelector.vue'
import { versionKeyProject } from '../lib/versions'
```

Replace the single-context `<code>` badge content so it reads:

```vue
      <code v-if="contexts.length <= 1" class="font-mono text-[12.5px] text-text-secondary bg-bg-muted px-[10px] py-[5px] rounded-[7px]">
        {{ versionKeyProject(selectedContext.prefix, version) }}
      </code>
```

Replace the entire `<!-- Version segment control -->` block with:

```vue
      <!-- Version selector -->
      <VersionSelector
        v-if="availableVersions.length > 0"
        :keys="availableVersions"
        :model-value="version"
        @update:model-value="emit('update:version', $event)"
      />
```

Leave the tab switcher block (`<!-- Tab switcher -->`, with `ml-auto`) untouched.

- [ ] **Step 4: Run tests and build**

Run: `npm test`
Expected: all pass.

Run: `npm run build`
Expected: `✓ built` with no `vue-tsc` errors (an unused `tabStyle` would fail `noUnusedLocals`).

- [ ] **Step 5: Smoke-check in the dev server (optional but recommended)**

Run `npm run dev` from `frontend/` with the backend reachable, open the Builds board and the Artifacts tab, and confirm the two rows render, All works on the board, and the badge reads e.g. `ppg:staging:16:tde` when that key is selected. Stop the dev server afterwards.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/ContextBar.vue frontend/src/components/ArtifactsVersionBar.vue
git commit -s -m "feat(ui): use the two-level VersionSelector in the board and artifacts bars"
```

```json:metadata
{"files": ["frontend/src/components/ContextBar.vue", "frontend/src/components/ArtifactsVersionBar.vue"], "verifyCommand": "cd frontend && npm test && npm run build", "acceptanceCriteria": ["board bar uses VersionSelector with allow-all and versionKeyProject badge", "artifacts bar uses VersionSelector without All and versionKeyProject badge", "tabStyle removed", "build passes"], "modelTier": "standard"}
```

---

### Task 5: Artifacts Packages tab auto-switches for package-less keys

**Goal:** When the selected Artifacts key changes while the Packages tab is active and no non-container package matches the new key, the panel switches to the Containers tab. It never switches back by itself.

**Files:**
- Modify: `frontend/src/composables/useArtifacts.ts` (export `keyHasPackages`)
- Create: `frontend/src/composables/useArtifacts.test.ts`
- Modify: `frontend/src/components/ArtifactsPanel.vue:202-204` (`onVersionChange`)

**Acceptance Criteria:**
- [ ] `keyHasPackages(packages, ctx, 'common')` is false when only container images match; true for `common:tools` when a package matches; false for the empty key list.
- [ ] Selecting `common` while on Packages switches the tab to Containers; selecting `18` afterwards leaves the tab on Containers; selecting `common` while on Tarballs leaves it on Tarballs.
- [ ] Release contexts are unaffected (their packages come from a different source).
- [ ] `npm test` and `npm run build` pass.

**Verify:** `cd frontend && npm test && npm run build`

**Steps:**

- [ ] **Step 1: Write the failing test**

Create `frontend/src/composables/useArtifacts.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { keyHasPackages } from './useArtifacts'
import { PPG_STAGING_CONTEXT } from '../lib/contexts'
import type { Package } from '../types/api'

function pkg(project: string, extra: Partial<Package> = {}): Package {
  return {
    project,
    name: project.replace(/:/g, '-'),
    rollup_state: 'succeeded',
    ok_targets: 1,
    total_targets: 1,
    targets: [],
    updated_at: '2026-10-09T00:00:00Z',
    ...extra,
  } as Package
}

const CORPUS = [
  pkg('ppg:staging:18'),
  pkg('ppg:staging:18:containers', { is_container: true }),
  pkg('ppg:staging:common:containers', { is_container: true }),
  pkg('ppg:staging:common:tools'),
  pkg('ppg:staging:common:tools:containers', { is_container: true }),
  pkg('ppg:staging:common:extras:containers', { is_container: true }),
]

describe('keyHasPackages', () => {
  it('is true for a version with real packages', () => {
    expect(keyHasPackages(CORPUS, PPG_STAGING_CONTEXT, '18')).toBe(true)
  })

  it('is false for the common base, which only holds images', () => {
    expect(keyHasPackages(CORPUS, PPG_STAGING_CONTEXT, 'common')).toBe(false)
  })

  it('is true for common:tools and false for common:extras', () => {
    expect(keyHasPackages(CORPUS, PPG_STAGING_CONTEXT, 'common:tools')).toBe(true)
    expect(keyHasPackages(CORPUS, PPG_STAGING_CONTEXT, 'common:extras')).toBe(false)
  })

  it('is false when there are no packages at all', () => {
    expect(keyHasPackages([], PPG_STAGING_CONTEXT, '18')).toBe(false)
  })
})
```

- [ ] **Step 2: Run the test to see it fail**

Run: `npm test`
Expected: FAIL, `keyHasPackages` is not exported from `./useArtifacts`.

- [ ] **Step 3: Add keyHasPackages to useArtifacts.ts**

Add this exported function to `frontend/src/composables/useArtifacts.ts`, directly above `export function useArtifacts(` (the file already imports `matchesVersionKey`, `Package`, and `Context`):

```ts
/** Does any non-container package of the corpus match the key in this
 *  context? Used by the Artifacts panel to leave an empty Packages tab
 *  for keys that only hold images (e.g. the common base). */
export function keyHasPackages(packages: Package[], ctx: Context, key: string): boolean {
  if (!key) return false
  return packages.some(p =>
    p.is_container !== true && matchesVersionKey(p.project, ctx.prefix, key, ctx.allowedSubprojects),
  )
}
```

- [ ] **Step 4: Switch tabs in ArtifactsPanel.vue**

Extend the `useArtifacts` import line to:

```ts
import { useArtifacts, keyHasPackages } from '../composables/useArtifacts'
```

Replace `onVersionChange`:

```ts
function onVersionChange(v: string) {
  emit('update:artifactsVersion', v)
  // A key that holds no packages (the common base holds only images)
  // would leave the Packages tab empty; move to Containers. Never move
  // back automatically. Release contexts source packages elsewhere.
  if (
    props.artifactsTab === 'packages' &&
    !isReleaseContext.value &&
    !keyHasPackages(artifactsPackages.value, props.artifactsContext, v)
  ) {
    emit('update:artifactsTab', 'containers')
  }
}
```

- [ ] **Step 5: Run tests and build**

Run: `npm test`
Expected: all pass.

Run: `npm run build`
Expected: `✓ built`.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/composables/useArtifacts.ts frontend/src/composables/useArtifacts.test.ts frontend/src/components/ArtifactsPanel.vue
git commit -s -m "feat(artifacts): switch to Containers when the selected key has no packages"
```

```json:metadata
{"files": ["frontend/src/composables/useArtifacts.ts", "frontend/src/composables/useArtifacts.test.ts", "frontend/src/components/ArtifactsPanel.vue"], "verifyCommand": "cd frontend && npm test && npm run build", "acceptanceCriteria": ["keyHasPackages false for common base, true for common:tools, true for 18, false for empty corpus", "onVersionChange switches packages->containers only when key has no packages and context is not a release", "never switches back"], "modelTier": "standard"}
```

---

### Task 6: Push, deploy, and verify the three approved states on prod

**Goal:** The redesign is live on production and the three approved Artifacts bar states render as specified.

**Files:**
- None modified. Verification only.

**Acceptance Criteria:**
- [ ] `origin/main` contains all commits from Tasks 0 to 5.
- [ ] Production container runs the new commit (`git log -1` on the server matches the pushed HEAD).
- [ ] Artifacts, PPG Staging, key `common`: version row `19 18 17 16 15 14 common` (plus transient `containers`/`extras` chips if the legacy rows still exist), subproject row `base · extras · tools`, badge `ppg:staging:common`, Containers tab showing images.
- [ ] Key `16:tde`: subproject row `base · extras · tde`, badge `ppg:staging:16:tde`.
- [ ] Key `19`: no subproject row, badge `ppg:staging:19`.
- [ ] Builds board, key `18`: no `ppg:staging:common:*` package cards; `ppg:common:deps` cards still present.

**Verify:** Visual check in the browser at the production URL, plus the API spot check below.

**Steps:**

- [ ] **Step 1: Push**

```bash
cd /home/rdias/Work/percona-obs-dashboard && git push origin main
```

- [ ] **Step 2: Deploy (run by the user)**

The agent cannot run this; hand the command to the user:

```bash
ssh <prod-host> 'cd ~/percona-obs-dashboard && git stash -q && git pull --ff-only && git stash pop -q && docker compose -f docker-compose.prod.yml up -d --build'
```

The stash keeps the server's uncommitted compose change (service renamed `obs_app`, external network).

- [ ] **Step 3: Confirm the deployed commit and health**

```bash
ssh <prod-host> 'cd ~/percona-obs-dashboard && git log --oneline -1 && docker compose -f docker-compose.prod.yml ps --format "{{.Name}} {{.Status}}" && curl -s -o /dev/null -w "overview http %{http_code}\n" "localhost:4000/api/overview?window=24h"'
```

Expected: the pushed HEAD hash, `Up …`, `overview http 200`.

- [ ] **Step 4: Visual check**

Open the production dashboard in the browser (Claude in Chrome may be used). On the Artifacts tab with PPG Staging, select `common`, `16:tde`, and `19` in turn and compare against the acceptance criteria. On the Builds board select `18` and confirm no `ppg:staging:common:*` cards appear while `ppg:common:deps` cards do. Record what was seen in the final report, including whether the transient `containers`/`extras` chips were present.

```json:metadata
{"files": [], "verifyCommand": "ssh <prod-host> 'cd ~/percona-obs-dashboard && git log --oneline -1'", "acceptanceCriteria": ["origin/main pushed", "prod runs the pushed HEAD", "common state renders base/extras/tools", "16:tde state renders base/extras/tde", "19 state has no subproject row", "board key 18 hides common projects"], "modelTier": "standard"}
```
