// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import HostUpdateBanner from './HostUpdateBanner.vue'
import { useAuth, resetAuthForTests } from '../composables/useAuth'
import { useUpgrades, resetUpgradesForTests, type ReleaseView, type UpgradesView } from '../composables/useUpgrades'

function rel(over: Partial<ReleaseView>): ReleaseView {
  return {
    version: 'v0.3.0', notes: '', url: '', published_at: '2026-11-03T00:00:00Z', status: 'available',
    requested_by: '', requested_at: null, finished_at: null, error: '', host_changes: 'Add FOO=1',
    requires_host: 'v0.3.0', blocked: true, host_instructions: [{ version: 'v0.3.0', host_changes: 'Add FOO=1' }], ...over,
  }
}
function setView(releases: ReleaseView[]) {
  const v: UpgradesView = { current: 'v0.1.0', host_files_version: 'v0.1.0', repo: 'Percona-Lab/obs-dashboard', apply_enabled: true, in_flight: '', checked_at: null, releases }
  useUpgrades().view.value = v
}

beforeEach(() => { resetAuthForTests(); resetUpgradesForTests() })

describe('HostUpdateBanner', () => {
  it('shows the steps when the newest release is blocked', () => {
    useAuth().user.value = 'alice@percona.com'
    setView([rel({}), rel({ version: 'v0.2.0', blocked: false, requires_host: '', host_instructions: [] })])
    const w = mount(HostUpdateBanner)
    const b = w.find('[data-testid="host-update-banner"]')
    expect(b.exists()).toBe(true)
    expect(b.attributes('role')).toBe('alert')
    expect(b.text()).toContain('v0.3.0 needs a host update before it can be installed from the UI.')
    expect(b.text()).toContain('Add FOO=1')
    expect(w.find('button').exists()).toBe(false)
  })
  it('is hidden when signed out', () => {
    setView([rel({})])
    expect(mount(HostUpdateBanner).find('[data-testid="host-update-banner"]').exists()).toBe(false)
  })
  it('is hidden when applying is not enabled', () => {
    useAuth().user.value = 'alice@percona.com'
    setView([rel({})])
    useUpgrades().view.value!.apply_enabled = false
    expect(mount(HostUpdateBanner).find('[data-testid="host-update-banner"]').exists()).toBe(false)
  })
  it('is hidden when the newest release is not blocked', () => {
    useAuth().user.value = 'alice@percona.com'
    setView([rel({ blocked: false }), rel({ version: 'v0.2.0' })])
    expect(mount(HostUpdateBanner).find('[data-testid="host-update-banner"]').exists()).toBe(false)
  })
})
