import { computed, ref } from 'vue'
import { handleUnauthorized } from './useAuth'
import { errorText } from './http'

export type ReleaseStatus = 'available' | 'applying' | 'applied' | 'failed' | 'superseded'

export interface HostInstruction {
  version: string
  host_changes: string
}

export interface ReleaseView {
  version: string
  notes: string
  url: string
  published_at: string
  status: ReleaseStatus
  requested_by: string
  requested_at: string | null
  finished_at: string | null
  error: string
  host_changes: string
  requires_host: string
  blocked: boolean
  host_instructions: HostInstruction[]
}

export interface UpgradesView {
  current: string
  host_files_version: string
  repo: string
  apply_enabled: boolean
  in_flight: string
  checked_at: string | null
  releases: ReleaseView[]
}

export const NOTICE_REFRESH_MS = 15 * 60 * 1000

// Module-level singleton shared by the Service zone, header dot and banner.
const view = ref<UpgradesView | null>(null)
const loadError = ref<string | null>(null)
let noticeTimer: ReturnType<typeof setInterval> | undefined

// Deployments without UI upgrades (apply_enabled=false) never show update notices.
const updateAvailable = computed(() => (view.value?.apply_enabled ?? false) && view.value!.releases.some((r) => r.status === 'available'))
// The newest available release, when it needs a host update first.
const blockedRelease = computed<ReleaseView | null>(() => {
  if (!view.value?.apply_enabled) return null
  const newest = view.value.releases.find((r) => r.status === 'available')
  return newest && newest.blocked ? newest : null
})

function isView(b: unknown): b is UpgradesView {
  if (typeof b !== 'object' || b === null) return false
  const v = b as Partial<UpgradesView>
  return typeof v.current === 'string' && Array.isArray(v.releases)
}

export type RefreshResult = 'ok' | 'unreachable' | 'unauthorized' | 'error'

// refreshUpgrades reloads the view. 'unreachable' covers the restart window:
// network errors, proxy 502/503/504 pages and non-JSON bodies.
export async function refreshUpgrades(): Promise<RefreshResult> {
  let res: Response
  try {
    res = await fetch('/api/upgrades')
  } catch {
    return 'unreachable'
  }
  if (res.status === 401) { handleUnauthorized(); return 'unauthorized' }
  if (res.status === 502 || res.status === 503 || res.status === 504) return 'unreachable'
  if (!res.ok) { loadError.value = await errorText(res); return 'error' }
  let body: unknown
  try {
    body = await res.json()
  } catch {
    return 'unreachable'
  }
  if (!isView(body)) { loadError.value = 'Unexpected response from /api/upgrades'; return 'error' }
  view.value = body
  loadError.value = null
  return 'ok'
}

// checkNow asks the server to check GitHub now; returns an error or null.
export async function checkNow(): Promise<string | null> {
  try {
    const res = await fetch('/api/upgrades/check', { method: 'POST' })
    if (res.status === 401) { handleUnauthorized(); return 'Session expired' }
    if (!res.ok) return await errorText(res)
    const body: unknown = await res.json()
    if (isView(body)) view.value = body
    return null
  } catch {
    return 'Network error'
  }
}

// applyUpgrade requests an upgrade; returns an error message or null.
export async function applyUpgrade(version: string): Promise<string | null> {
  try {
    const res = await fetch(`/api/upgrades/${encodeURIComponent(version)}/apply`, { method: 'POST' })
    if (res.status === 401) { handleUnauthorized(); return 'Session expired' }
    if (res.status === 409 && (res.headers.get('Content-Type') ?? '').includes('application/json')) {
      const body = (await res.json()) as { error?: string; requires_host?: string }
      if (body.error === 'host_update_required') return `Host update required: install host files ${body.requires_host} first`
    }
    if (!res.ok) return await errorText(res)
    await refreshUpgrades()
    return null
  } catch {
    return 'Network error'
  }
}

// hostUpdateCommands is the manual, tool-agnostic host-files update for v.
export function hostUpdateCommands(repo: string, v: string): string {
  const base = `https://github.com/${repo}/releases/download/${v}`
  const tar = `obs-dashboard-host-${v}.tar.gz`
  return [
    'cd "$(mktemp -d)"',
    `curl -fLO ${base}/${tar}`,
    `curl -fLO ${base}/SHA256SUMS`,
    'sha256sum -c --ignore-missing SHA256SUMS',
    `tar xzf ${tar}`,
    'install -m 0644 obs-dashboard.container /etc/containers/systemd/',
    'install -D -m 0755 apply.sh /usr/local/lib/obs-dashboard/apply.sh',
    'install -m 0644 obs-dashboard-apply.path obs-dashboard-apply.service /etc/systemd/system/',
    '# apply the release-specific host changes listed above, then:',
    "grep -q '^OBS_DASHBOARD_HOST_FILES_VERSION=' /etc/obs-dashboard/env \\",
    `  && sed -i 's/^OBS_DASHBOARD_HOST_FILES_VERSION=.*/OBS_DASHBOARD_HOST_FILES_VERSION=${v}/' /etc/obs-dashboard/env \\`,
    `  || echo 'OBS_DASHBOARD_HOST_FILES_VERSION=${v}' >> /etc/obs-dashboard/env`,
    'systemctl daemon-reload',
    'systemctl enable --now obs-dashboard-apply.path',
    'systemctl restart obs-dashboard',
  ].join('\n')
}

// startUpgradeNotices loads the view now and every 15 minutes (signed in).
export function startUpgradeNotices(): void {
  stopUpgradeNotices()
  void refreshUpgrades()
  noticeTimer = setInterval(() => { void refreshUpgrades() }, NOTICE_REFRESH_MS)
}

// stopUpgradeNotices stops refreshing and forgets the view (signed out).
export function stopUpgradeNotices(): void {
  if (noticeTimer !== undefined) clearInterval(noticeTimer)
  noticeTimer = undefined
  view.value = null
}

export function resetUpgradesForTests(): void {
  stopUpgradeNotices()
  loadError.value = null
}

export function useUpgrades() {
  return { view, loadError, updateAvailable, blockedRelease }
}
