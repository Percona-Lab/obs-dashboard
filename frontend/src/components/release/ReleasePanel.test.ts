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
  cut: null, sync_state: '', sync: [], removals: [], qa: null, final: null,
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
})
