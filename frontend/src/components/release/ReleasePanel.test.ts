// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ReleasePanel from './ReleasePanel.vue'
import { useRelease, resetReleaseForTests } from '../../composables/useRelease'
import { resetAuthForTests } from '../../composables/useAuth'
import type { ReleaseState, Snapshot } from '../../types/release'

const rel = (o: Partial<ReleaseState> = {}): ReleaseState => ({
  id: 'r1', branch: 'release/r1', pr: 412, pr_url: 'https://github.com/o/r/pull/412', head_sha: 'abc', step: 'qa',
  dashboard: true, units: [{ unit: 'u', release_project: '', release_dir: '', release_id: '17.10-2', tag: '', changelog: '', generated: null }],
  cut: null, sync_state: '', sync_url: '', sync: [], removals: [], qa: null, final: null,
  started_by: '', started_at: null, actions: [], ...o,
})
const snap = (releases: ReleaseState[], o: Partial<Snapshot> = {}): Snapshot => ({ releases, stale: false, error: '', refreshed_at: '', ...o })
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200 })

beforeEach(() => {
  resetAuthForTests(); resetReleaseForTests()
  vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(json(url.includes('/history') ? { rows: [] } : { units: [] }))))
})
afterEach(() => vi.unstubAllGlobals())

describe('ReleasePanel', () => {
  it('shows loading indicators until the releases, the units and the history have loaded', async () => {
    let release!: (r: Response) => void
    const pending = new Promise<Response>((res) => { release = res })
    vi.stubGlobal('fetch', vi.fn(() => pending))
    const w = mount(ReleasePanel)
    await flushPromises()
    expect(w.get('[data-testid="in-flight"]').find('[data-testid="release-loading"]').text()).toContain('Loading releases')
    expect(w.find('[data-testid="release-in-flight"]').exists()).toBe(false)
    expect(w.get('[data-testid="new-release"]').find('[data-testid="release-loading"]').text()).toContain('Loading release units')
    expect(w.get('[data-testid="history"]').find('[data-testid="release-loading"]').text()).toContain('Loading history')
    expect(w.get('[data-testid="release-loading"]').attributes('role')).toBe('status')

    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(json(url.includes('/history') ? { rows: [], more: false } : { units: [] }))))
    release(json({ units: [] }))
    useRelease().snapshot.value = snap([])
    await flushPromises()
    await useRelease().loadUnits()
    await flushPromises()
    expect(w.get('[data-testid="in-flight"]').find('[data-testid="release-loading"]').exists()).toBe(false)
    expect(w.get('[data-testid="release-in-flight"]').text()).toContain('No releases in flight.')
    expect(w.get('[data-testid="new-release"]').find('[data-testid="release-loading"]').exists()).toBe(false)
  })

  it('says so when the first load of the releases fails', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(url.endsWith('/releases') ? new Response('github request failed', { status: 502 }) : json(url.includes('/history') ? { rows: [], more: false } : { units: [] }))))
    const w = mount(ReleasePanel)
    await useRelease().refresh()
    await flushPromises()
    const box = w.get('[data-testid="in-flight"]')
    expect(box.find('[data-testid="release-loading"]').exists()).toBe(false)
    expect(box.get('[data-testid="release-load-error"]').text()).toContain('Could not load the releases')
  })

  it('lists in-flight releases with unknown starter and opens the wizard placeholder', async () => {
    useRelease().snapshot.value = snap([rel()])
    const w = mount(ReleasePanel)
    await flushPromises()
    const li = w.get('[data-release="r1"]')
    expect(li.text()).toContain('unknown')
    expect(li.text()).toContain('17.10-2')
    expect(li.text()).toContain('QA')
    expect(li.find('a').attributes('rel')).toBe('noopener noreferrer')
    await w.get('[data-testid="open-in-flight"]').trigger('click')
    expect(w.find('[data-testid="release-wizard"]').exists()).toBe(true)
    await w.get('[data-testid="release-back"]').trigger('click')
    expect(w.find('[data-testid="release-wizard"]').exists()).toBe(false)
  })

  it('shows a cutting release without a PR link and returns to landing when it disappears', async () => {
    useRelease().snapshot.value = snap([rel({ step: 'cutting', pr: 0, pr_url: '', units: [], cut: { state: 'failure', url: '' } })])
    const w = mount(ReleasePanel)
    await flushPromises()
    const li = w.get('[data-release="r1"]')
    expect(li.find('a').exists()).toBe(false)
    expect(li.text()).toContain('Cut failed')
    await w.get('[data-testid="open-in-flight"]').trigger('click')
    useRelease().snapshot.value = snap([])
    await flushPromises()
    expect(w.find('[data-testid="release-wizard"]').exists()).toBe(false)
  })

  it('shows the stale warning and a dismissible notice', async () => {
    useRelease().snapshot.value = snap([], { stale: true, error: 'github down' })
    useRelease().notice.value = 'hello'
    const w = mount(ReleasePanel)
    await flushPromises()
    expect(w.get('[data-testid="release-stale"]').text()).toContain('github down')
    await w.get('[data-testid="release-notice"] button').trigger('click')
    expect(w.find('[data-testid="release-notice"]').exists()).toBe(false)
  })

  it('keeps a just-cut release selected until it is listed', async () => {
    vi.useFakeTimers()
    try {
      const { snapshot, cut } = useRelease()
      snapshot.value = snap([])
      vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => Promise.resolve(
        init?.method === 'POST' ? json({ id: 'r9' })
          : url.includes('/history') ? json({ rows: [] }) : url.includes('/units') ? json({ units: [{ unit: 'ppg:staging:18', members: [], release_project: '', release_dir: '', last_release: '', built: 1, total: 1, tickable: true }] })
          : json(snap([])))))
      const w = mount(ReleasePanel)
      await flushPromises()
      void cut
      await w.get('[data-unit="ppg:staging:18"] input').setValue(true)
      await w.get('[data-testid="cut-release"]').trigger('click')
      await flushPromises()
      expect(w.get('[data-testid="release-starting"]').text()).toContain('Starting the cut')
      snapshot.value = snap([])
      await flushPromises()
      expect(w.find('[data-testid="release-starting"]').exists()).toBe(true)
      snapshot.value = snap([rel({ id: 'r9', step: 'cutting', pr: 0, pr_url: '', units: [], cut: { state: 'queued', url: '' } })])
      await flushPromises()
      expect(w.find('[data-testid="release-starting"]').exists()).toBe(false)
      expect(w.find('[data-testid="release-wizard"]').exists()).toBe(true)
    } finally { vi.useRealTimers() }
  })

  it('gives up after 90 seconds when the cut never appears', async () => {
    vi.useFakeTimers()
    try {
      const { snapshot } = useRelease()
      snapshot.value = snap([])
      vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => Promise.resolve(
        init?.method === 'POST' ? json({ id: 'r9' })
          : url.includes('/history') ? json({ rows: [] }) : url.includes('/units') ? json({ units: [{ unit: 'ppg:staging:18', members: [], release_project: '', release_dir: '', last_release: '', built: 1, total: 1, tickable: true }] })
          : json(snap([])))))
      const w = mount(ReleasePanel)
      await flushPromises()
      await w.get('[data-unit="ppg:staging:18"] input').setValue(true)
      await w.get('[data-testid="cut-release"]').trigger('click')
      await flushPromises()
      expect(w.find('[data-testid="release-starting"]').exists()).toBe(true)
      await vi.advanceTimersByTimeAsync(91_000)
      await flushPromises()
      expect(w.find('[data-testid="release-starting"]').exists()).toBe(false)
      expect(useRelease().notice.value).toBe('The cut did not start. Check the Actions tab of the repository.')
    } finally { vi.useRealTimers() }
  })

  it('passes Open in Builds on to the app', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(json(url.includes('/history') ? { rows: [], more: false }
      : { units: [{ unit: 'ppg:staging:18', members: [], release_project: '', release_dir: '', last_release: '', built: 0, total: 1, tickable: false, blocked: { reason: 'not_built', text: 'Not fully built', packages: ['pg'] } }] }))))
    useRelease().snapshot.value = snap([])
    const w = mount(ReleasePanel)
    await flushPromises()
    await w.get('[data-testid="open-builds"]').trigger('click')
    expect(w.emitted('open-builds')).toHaveLength(1)
  })

  it('the in-flight list labels a held cut as waiting for approval', async () => {
    useRelease().snapshot.value = snap([rel({ step: 'cutting', pr: 0, pr_url: '', units: [], cut: { state: 'approval', url: 'https://run' } })])
    const w = mount(ReleasePanel)
    await flushPromises()
    const li = w.get('[data-release="r1"]')
    expect(li.text()).toContain('Waiting for approval')
    expect(li.text()).not.toContain('Cutting')
    expect(li.text()).not.toContain('Cut failed')
    expect(li.find('.text-fail').exists()).toBe(false)
  })
})
