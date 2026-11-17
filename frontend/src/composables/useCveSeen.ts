import { readonly, ref } from 'vue'

export const SEEN_STORAGE_KEY = 'obsboard.cve.seen.v1'

// Module-level singleton: the set of CVE seen keys this browser has
// acknowledged. When storage is unusable, markers are disabled entirely
// (available false, nothing is new) rather than flagging everything.
const seen = ref<Set<string>>(new Set())
const available = ref(true)

function load(): void {
  try {
    const raw = localStorage.getItem(SEEN_STORAGE_KEY)
    if (raw === null) {
      seen.value = new Set()
      available.value = true
      return
    }
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed) || !parsed.every(k => typeof k === 'string')) {
      throw new Error('seen set is not an array of strings')
    }
    seen.value = new Set(parsed as string[])
    available.value = true
  } catch {
    seen.value = new Set()
    available.value = false
  }
}

load()

function isNew(seenKey: string): boolean {
  return available.value && !seen.value.has(seenKey)
}

function markAllSeen(seenKeys: string[]): void {
  if (!available.value) return
  const next = new Set(seen.value)
  for (const k of seenKeys) next.add(k)
  try {
    localStorage.setItem(SEEN_STORAGE_KEY, JSON.stringify([...next]))
    seen.value = next
  } catch {
    seen.value = new Set()
    available.value = false
  }
}

export function useCveSeen() {
  return { available: readonly(available), isNew, markAllSeen }
}

export function resetCveSeenForTests(): void {
  load()
}
