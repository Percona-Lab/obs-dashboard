import { ref, watch, type Ref, type WatchStopHandle } from 'vue'
import { handleUnauthorized } from './useAuth'
import { errorText } from './http'
import { pruneDrafts } from './useChangelogDrafts'
import type { HistoryPage, ReleaseAction, Snapshot, UnitRow } from '../types/release'

export const RELEASE_POLL_MS = 15_000
const CONFLICT_NOTICE = 'This release changed. The view has been refreshed.'
const BASE = '/api/release'

// Module-level singleton, like useAuth.
const enabled = ref(false)
const snapshot = ref<Snapshot | null>(null)
const units = ref<UnitRow[]>([])
const unitsError = ref('')
// True once the first attempt to load the unit list has finished, either way.
const unitsLoaded = ref(false)
// True when the latest refresh of the releases failed.
const loadFailed = ref(false)
const notice = ref('')
const busy = ref(false)

let timer: ReturnType<typeof setInterval> | undefined
let stopWatch: WatchStopHandle | undefined
let removeVisibility: (() => void) | undefined
let refreshing: Promise<void> | null = null

// Resolves true when the status call completed (a 200, 401 or 404). A network
// error or 5xx leaves `enabled` unchanged and resolves false.
async function loadStatus(): Promise<boolean> {
  try {
    const res = await fetch(`${BASE}/status`)
    if (res.status === 401) { handleUnauthorized(); enabled.value = false; return true }
    if (res.status === 404) { enabled.value = false; return true }
    if (!res.ok) return false
    enabled.value = ((await res.json()) as { enabled?: boolean }).enabled === true
    return true
  } catch {
    return false
  }
}

let refreshSeq = 0

// A plain refresh shares a running one. `fresh` (used after a write) always
// starts a new GET, so the snapshot reflects the write. A response is applied
// only if no newer refresh started since it was sent.
function doRefresh(fresh = false): Promise<void> {
  if (refreshing && !fresh) return refreshing
  const mine = ++refreshSeq
  const p = (async () => {
    try {
      const res = await fetch(`${BASE}/releases`)
      if (res.status === 401) { handleUnauthorized(); return }
      if (!res.ok) { if (mine === refreshSeq) loadFailed.value = true; return }
      const next = (await res.json()) as Snapshot
      if (mine === refreshSeq) {
        loadFailed.value = false
        snapshot.value = next
        pruneDrafts(next.releases ?? [])
      }
    } catch {
      // Keep the last snapshot; the next poll retries.
      if (mine === refreshSeq) loadFailed.value = true
    } finally {
      if (mine === refreshSeq) refreshing = null
    }
  })()
  refreshing = p
  return p
}

async function loadUnits(): Promise<void> {
  try {
    const res = await fetch(`${BASE}/units`)
    if (res.status === 401) { handleUnauthorized(); return }
    if (!res.ok) { unitsError.value = await errorText(res); return }
    units.value = ((await res.json()) as { units: UnitRow[] }).units
    unitsError.value = ''
  } catch {
    unitsError.value = 'Network error'
  } finally {
    unitsLoaded.value = true
  }
}

// send performs a write, then always refreshes the snapshot. Returns the
// response when it was ok, otherwise null with `notice` set.
async function send(method: string, path: string, body: unknown): Promise<Response | null> {
  busy.value = true
  notice.value = ''
  try {
    const res = await fetch(`${BASE}${path}`, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (res.status === 401) handleUnauthorized()
    if (!res.ok) {
      notice.value = res.status === 409 ? CONFLICT_NOTICE : await errorText(res)
    }
    return res.ok ? res : null
  } catch {
    notice.value = 'Network error'
    return null
  } finally {
    await doRefresh(true)
    busy.value = false
  }
}

async function cut(unitIDs: string[]): Promise<string | null> {
  const res = await send('POST', '/releases', { units: unitIDs })
  if (!res) return null
  try {
    return ((await res.json()) as { id: string }).id
  } catch {
    return null
  }
}

async function saveChangelog(id: string, head: string, unit: string, section: string): Promise<boolean> {
  return (await send('PUT', `/releases/${encodeURIComponent(id)}/changelog`, { head, unit, section })) !== null
}

async function resetChangelog(id: string, head: string, unit: string): Promise<boolean> {
  return (await send('POST', `/releases/${encodeURIComponent(id)}/changelog/reset`, { head, unit })) !== null
}

async function act(id: string, action: ReleaseAction, head: string, confirmFailed = false): Promise<boolean> {
  return (await send('POST', `/releases/${encodeURIComponent(id)}/${action}`, { head, confirm_failed: confirmFailed })) !== null
}

// null on any failure, so callers can tell an error from an empty page.
async function loadHistory(page: number): Promise<HistoryPage | null> {
  try {
    const res = await fetch(`${BASE}/history?page=${page}`)
    if (res.status === 401) { handleUnauthorized(); return null }
    if (!res.ok) return null
    const body = (await res.json()) as Partial<HistoryPage>
    return { rows: body.rows ?? [], more: body.more === true }
  } catch {
    return null
  }
}

function stop(): void {
  if (timer !== undefined) clearInterval(timer)
  timer = undefined
}

// start drives polling: every 15 s, only while `active` and the document is visible.
function start(active: Ref<boolean>): void {
  stopPolling()
  const sync = () => {
    stop()
    if (!active.value || document.visibilityState !== 'visible') return
    void doRefresh()
    timer = setInterval(() => { void doRefresh() }, RELEASE_POLL_MS)
  }
  stopWatch = watch(active, sync)
  document.addEventListener('visibilitychange', sync)
  removeVisibility = () => document.removeEventListener('visibilitychange', sync)
  sync()
}

function stopPolling(): void {
  stop()
  stopWatch?.()
  stopWatch = undefined
  removeVisibility?.()
  removeVisibility = undefined
}

export function resetReleaseForTests(): void {
  stopPolling()
  refreshing = null
  refreshSeq = 0
  enabled.value = false
  snapshot.value = null
  units.value = []
  unitsError.value = ''
  unitsLoaded.value = false
  loadFailed.value = false
  notice.value = ''
  busy.value = false
}

export function useRelease() {
  return {
    enabled, snapshot, units, unitsError, unitsLoaded, loadFailed, notice, busy,
    loadStatus, refresh: () => doRefresh(), loadUnits,
    start, stopPolling,
    cut, saveChangelog, resetChangelog, act, loadHistory,
  }
}
