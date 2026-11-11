import { computed, reactive, watch } from 'vue'
import type { ReleaseState } from '../types/release'

// Unsaved changelog text, kept outside the editor component so that leaving
// the wizard, switching the main tab or the release changing step does not
// lose it. Keyed by release id, then unit. A draft exists only while the text
// differs from the changelog on GitHub.
export interface ChangelogDraft {
  text: string // what the user typed
  synced: string // the changelog on GitHub the text was started from
}

const drafts = reactive<Record<string, Record<string, ChangelogDraft>>>({})

// The backend stores a section with every line terminated and may hold CRLF;
// a textarea returns LF. Neither difference is an edit.
const norm = (s: string) => s.replace(/\r\n/g, '\n').replace(/\n+$/, '')
export const sameText = (a: string, b: string) => norm(a) === norm(b)

export function getDraft(id: string, unit: string): ChangelogDraft | undefined {
  const d = drafts[id]?.[unit]
  return d ? { text: d.text, synced: d.synced } : undefined
}

export function setDraft(id: string, unit: string, draft: ChangelogDraft): void {
  if (!drafts[id]) drafts[id] = {}
  drafts[id][unit] = { text: draft.text, synced: draft.synced }
}

export function dropDraft(id: string, unit: string): void {
  if (!drafts[id]) return
  delete drafts[id][unit]
  if (!Object.keys(drafts[id]).length) delete drafts[id]
}

export function hasDrafts(id: string): boolean {
  return !!drafts[id] && Object.keys(drafts[id]).length > 0
}

// Called with every snapshot: drafts of a release that is gone, of a unit
// that is gone, or that equal the changelog on GitHub are dropped.
export function pruneDrafts(releases: Pick<ReleaseState, 'id' | 'units'>[]): void {
  for (const id of Object.keys(drafts)) {
    const r = releases.find((x) => x.id === id)
    for (const unit of Object.keys(drafts[id] ?? {})) {
      const u = r?.units.find((x) => x.unit === unit)
      if (!u || sameText(drafts[id][unit].text, u.changelog)) dropDraft(id, unit)
    }
  }
}

// While any draft exists, leaving or reloading the page asks first.
const anyDraft = computed(() => Object.keys(drafts).length > 0)
function onBeforeUnload(e: BeforeUnloadEvent): void {
  e.preventDefault()
  e.returnValue = ''
}
watch(anyDraft, (on) => {
  if (typeof window === 'undefined') return
  if (on) window.addEventListener('beforeunload', onBeforeUnload)
  else window.removeEventListener('beforeunload', onBeforeUnload)
}, { flush: 'sync' })

export function resetDraftsForTests(): void {
  for (const id of Object.keys(drafts)) delete drafts[id]
}
