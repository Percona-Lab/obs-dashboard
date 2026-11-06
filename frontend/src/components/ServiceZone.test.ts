// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ServiceZone from './ServiceZone.vue'
import { resetAuthForTests, useAuth } from '../composables/useAuth'
import { resetUpgradesForTests, type UpgradesView, type ReleaseView } from '../composables/useUpgrades'

function release(over: Partial<ReleaseView>): ReleaseView {
  return {
    version: 'v0.2.0', notes: 'Fixed things', url: 'https://h/v0.2.0', published_at: '2026-11-02T10:00:00Z',
    status: 'available', requested_by: '', requested_at: null, finished_at: null, error: '',
    host_changes: '', requires_host: '', blocked: false, host_instructions: [], ...over,
  }
}

function view(over: Partial<UpgradesView> = {}): UpgradesView {
  return {
    current: 'v0.1.0', host_files_version: 'v0.1.0', repo: 'Percona-Lab/obs-dashboard', apply_enabled: true,
    in_flight: '', checked_at: '2026-11-06T12:00:00Z',
    releases: [release({})], ...over,
  }
}

type Handler = (url: string, init?: RequestInit) => Promise<Response> | Response
let handler: Handler
let fetchFn: ReturnType<typeof vi.fn>
const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

beforeEach(() => {
  resetAuthForTests()
  resetUpgradesForTests()
  useAuth().user.value = 'alice@percona.com'
  handler = () => json(view())
  fetchFn = vi.fn((url: string, init?: RequestInit) => Promise.resolve(handler(url, init)))
  vi.stubGlobal('fetch', fetchFn)
})
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

async function mounted() {
  const w = mount(ServiceZone)
  await flushPromises()
  return w
}

describe('ServiceZone', () => {
  it('shows the current version, releases with notes, and history', async () => {
    handler = () => json(view({ releases: [release({}), release({ version: 'v0.0.9', status: 'failed', error: 'rolled back', finished_at: '2026-11-01T10:00:00Z' })] }))
    const w = await mounted()
    expect(w.find('[data-testid="current-version"]').text()).toBe('v0.1.0')
    expect(w.findAll('[data-testid="release-row"]')).toHaveLength(1)
    expect(w.find('[data-testid="notes"]').text()).toBe('Fixed things')
    const hist = w.findAll('[data-testid="history-row"]')
    expect(hist).toHaveLength(1)
    expect(hist[0].text()).toContain('rolled back')
  })

  it('confirms, applies, shows Restarting during the restart, then offers Reload', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    const w = await mounted()
    await w.find('[data-testid="upgrade"]').trigger('click')
    expect(w.find('[data-testid="confirm"]').exists()).toBe(true)
    handler = (url, init) => {
      if (url.endsWith('/apply') && init?.method === 'POST') return json(release({ status: 'applying' }), 202)
      return json(view({ in_flight: 'v0.2.0', releases: [release({ status: 'applying' })] }))
    }
    await w.find('[data-testid="confirm"]').trigger('click')
    await flushPromises()
    expect(fetchFn.mock.calls.some(([u, i]) => u === '/api/upgrades/v0.2.0/apply' && i?.method === 'POST')).toBe(true)
    expect(w.find('[data-testid="progress"]').text()).toContain('Upgrading to v0.2.0')

    handler = () => new Response('<html>502</html>', { status: 502 })
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(w.find('[data-testid="progress"]').text()).toContain('Restarting')

    handler = () => json(view({ current: 'v0.2.0', releases: [release({ status: 'applied', finished_at: '2026-11-06T12:03:00Z' })] }))
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(w.find('[data-testid="progress"]').text()).toContain('Upgraded to v0.2.0')
    expect(w.find('[data-testid="reload"]').exists()).toBe(true)
  })

  it('shows the failure when the upgrade is rolled back', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    handler = () => json(view({ in_flight: 'v0.2.0', releases: [release({ status: 'applying' })] }))
    const w = await mounted()
    handler = () => json(view({ releases: [release({ status: 'failed', error: 'health check timed out; rolled back to v0.1.0' })] }))
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(w.find('[data-testid="failure"]').text()).toContain('rolled back to v0.1.0')
  })

  it('disables Upgrade when applying is not enabled', async () => {
    handler = () => json(view({ apply_enabled: false }))
    const w = await mounted()
    expect(w.find('[data-testid="apply-disabled"]').exists()).toBe(true)
    expect((w.find('[data-testid="upgrade"]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('shows host update steps on a blocked release', async () => {
    handler = () => json(view({
      host_files_version: '',
      releases: [release({ version: 'v0.3.0', blocked: true, requires_host: 'v0.3.0', host_changes: 'Add FOO=1 to /etc/obs-dashboard/env', host_instructions: [{ version: 'v0.3.0', host_changes: 'Add FOO=1 to /etc/obs-dashboard/env' }] })],
    }))
    const w = await mounted()
    const btn = w.find('[data-testid="upgrade"]')
    expect(btn.text()).toBe('Host update required')
    expect((btn.element as HTMLButtonElement).disabled).toBe(true)
    expect(w.find('[data-testid="host-instruction"]').text()).toContain('Add FOO=1')
    expect(w.find('[data-testid="host-commands"]').text()).toContain('obs-dashboard-host-v0.3.0.tar.gz')
    expect(w.find('[data-testid="host-files-version"]').text()).toContain('unknown')
  })

  it('reports a failed check', async () => {
    const w = await mounted()
    handler = (_url, init) => (init?.method === 'POST' ? new Response('release check failed: HTTP 403', { status: 502 }) : json(view()))
    await w.find('[data-testid="check-now"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="action-error"]').text()).toContain('release check failed')
  })
})
