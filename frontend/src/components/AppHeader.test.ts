// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import AppHeader from './AppHeader.vue'
import { useAuth, resetAuthForTests } from '../composables/useAuth'
import { useUpgrades, resetUpgradesForTests } from '../composables/useUpgrades'
import { useRelease, resetReleaseForTests } from '../composables/useRelease'
import { useCveReport, resetCveReportForTests } from '../composables/useCveReport'
import { resetCveSeenForTests } from '../composables/useCveSeen'

function mountHeader() {
  return mount(AppHeader, { props: { theme: 'light', mainTab: 'overview' } })
}

beforeEach(() => resetAuthForTests())

describe('AppHeader auth controls', () => {
  it('offers Sign in when signed out', () => {
    useAuth().authEnabled.value = true
    const w = mountHeader()
    expect(w.find('[data-testid="sign-in"]').exists()).toBe(true)
    expect(w.find('[data-testid="settings-tab"]').exists()).toBe(false)
    expect(w.find('[data-testid="sign-out"]').exists()).toBe(false)
  })

  it('shows the user, Settings and Sign out when signed in', async () => {
    const { authEnabled, user } = useAuth()
    authEnabled.value = true
    user.value = 'alice@percona.com'
    const w = mountHeader()
    expect(w.find('[data-testid="user-email"]').text()).toBe('alice@percona.com')
    expect(w.find('[data-testid="sign-out"]').exists()).toBe(true)
    await w.find('[data-testid="settings-tab"]').trigger('click')
    expect(w.emitted('update:main-tab')).toEqual([['settings']])
  })

  it('shows no auth controls when sign-in is disabled', () => {
    const w = mountHeader()
    expect(w.find('[data-testid="sign-in"]').exists()).toBe(false)
    expect(w.find('[data-testid="sign-out"]').exists()).toBe(false)
  })
})

describe('AppHeader update dot', () => {
  beforeEach(() => resetUpgradesForTests())
  const v = (status: 'available' | 'applied') => ({
    current: 'v0.1.0', host_files_version: '', repo: 'Percona-Lab/obs-dashboard', apply_enabled: true, in_flight: '', checked_at: null,
    releases: [{ version: 'v0.2.0', notes: '', url: '', published_at: '', status, requested_by: '', requested_at: null, finished_at: null, error: '', host_changes: '', requires_host: '', blocked: false, host_instructions: [] }],
  })
  it('shows a dot on Settings when a release is available', () => {
    useAuth().user.value = 'alice@percona.com'
    useAuth().authEnabled.value = true
    useUpgrades().view.value = v('available')
    expect(mountHeader().find('[data-testid="update-dot"]').exists()).toBe(true)
  })
  it('shows no dot when applying is not enabled', () => {
    useAuth().user.value = 'alice@percona.com'
    useUpgrades().view.value = { ...v('available'), apply_enabled: false }
    expect(mountHeader().find('[data-testid="update-dot"]').exists()).toBe(false)
  })
  it('shows no dot when nothing is available', () => {
    useAuth().user.value = 'alice@percona.com'
    useUpgrades().view.value = v('applied')
    expect(mountHeader().find('[data-testid="update-dot"]').exists()).toBe(false)
  })
})

describe('AppHeader release tab', () => {
  beforeEach(() => resetReleaseForTests())
  it('shows Release only when signed in and enabled', async () => {
    useAuth().authEnabled.value = true
    useRelease().enabled.value = true
    expect(mountHeader().find('[data-testid="release-tab"]').exists()).toBe(false)
    useAuth().user.value = 'alice@percona.com'
    const w = mountHeader()
    expect(w.find('[data-testid="release-tab"]').exists()).toBe(true)
    await w.find('[data-testid="release-tab"]').trigger('click')
    expect(w.emitted('update:main-tab')).toEqual([['release']])
    useRelease().enabled.value = false
    expect(mountHeader().find('[data-testid="release-tab"]').exists()).toBe(false)
  })
})

describe('AppHeader CVEs tab', () => {
  beforeEach(() => { localStorage.clear(); resetCveSeenForTests(); resetCveReportForTests() })
  afterEach(() => vi.unstubAllGlobals())

  async function loadReport(ids: string[]) {
    const entries = ids.map(id => ({ id, pkg: 'p', severity: 'HIGH', title: 't', seen_key: `${id}|p|` }))
    const rows = ids.length ? [{ key: 'ours|p', name: 'p', kind: 'obs_package', worst: 'HIGH', cves: ids.length, fixes: [], entries, images: [] }] : []
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, statusText: 'OK', json: async () => ({ generated_at: '', staging: { ours: rows, base_os: [], unclassified: [], cves: ids.length, images: 1 }, released: [] }) })))
    await useCveReport().refresh()
  }

  it('is shown when signed out, after Artifacts, and emits cves on click', async () => {
    const w = mountHeader()
    const tabs = w.findAll('.tab-pill').map(b => b.text())
    expect(tabs.indexOf('Artifacts')).toBeGreaterThanOrEqual(0)
    expect(w.findAll('.tab-pill')[tabs.indexOf('Artifacts') + 1].attributes('data-testid')).toBe('cves-tab')
    await w.find('[data-testid="cves-tab"]').trigger('click')
    expect(w.emitted('update:main-tab')).toEqual([['cves']])
  })

  it('shows "3 new" with three unseen entries', async () => {
    await loadReport(['CVE-1', 'CVE-2', 'CVE-3'])
    expect(mountHeader().find('[data-testid="cves-new-badge"]').text()).toBe('3 new')
  })

  it('shows no badge with none', async () => {
    await loadReport([])
    expect(mountHeader().find('[data-testid="cves-new-badge"]').exists()).toBe(false)
  })
})
