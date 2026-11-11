// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ReleaseHistory from './ReleaseHistory.vue'
import { resetReleaseForTests } from '../../composables/useRelease'
import { resetAuthForTests } from '../../composables/useAuth'
import type { HistoryRow } from '../../types/release'

const hr = (i: number, o: Partial<HistoryRow> = {}): HistoryRow => ({
  tag: `t${i}`, release_id: `18.6-${i}`, unit: 'ppg:staging:18', released_at: '2026-11-11T10:00:00Z',
  release_url: `https://github.com/o/r/releases/tag/t${i}`, pr_url: `https://github.com/o/r/pull/${i}`,
  notes: '## x\n- y', released_by: '', failed_lanes: [], ...o,
})
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200 })

beforeEach(() => { resetAuthForTests(); resetReleaseForTests() })
afterEach(() => vi.unstubAllGlobals())

describe('ReleaseHistory', () => {
  it('renders rows with release and PR links, omits a missing PR link', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ rows: [hr(1), hr(2, { pr_url: '', failed_lanes: ['a'], released_by: 'bob@x.io' })] }))))
    const w = mount(ReleaseHistory)
    await flushPromises()
    const rows = w.findAll('[data-testid="history-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('18.6-1')
    const a0 = rows[0].findAll('a')
    expect(a0.map((a) => a.text())).toEqual(['GitHub release', 'PR'])
    expect(a0[0].attributes('rel')).toBe('noopener noreferrer')
    expect(a0[0].attributes('target')).toBe('_blank')
    expect(rows[1].findAll('a').map((a) => a.text())).toEqual(['GitHub release'])
    expect(rows[1].text()).toContain('released with failed QA')
    expect(rows[1].text()).toContain('bob@x.io')
  })

  it('loads more pages and expands notes', async () => {
    const page1 = Array.from({ length: 20 }, (_, i) => hr(i))
    const f = vi.fn((url: string) => Promise.resolve(json({ rows: url.endsWith('page=1') ? page1 : [hr(99)] })))
    vi.stubGlobal('fetch', f)
    const w = mount(ReleaseHistory)
    await flushPromises()
    await w.get('[data-testid="history-more"]').trigger('click')
    await flushPromises()
    expect(w.findAll('[data-testid="history-row"]')).toHaveLength(21)
    expect(w.find('[data-testid="history-more"]').exists()).toBe(false)
    await w.findAll('[data-testid="history-toggle"]')[0].trigger('click')
    expect(w.find('[data-testid="history-notes"]').text()).toContain('y')
  })

  it('shows an error with Retry when the first page fails', async () => {
    let ok = false
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(ok ? json({ rows: [hr(1)] }) : new Response('x', { status: 500 }))))
    const w = mount(ReleaseHistory)
    await flushPromises()
    expect(w.get('[data-testid="history-error"]').text()).toContain('Could not load the release history.')
    expect(w.text()).not.toContain('No finished releases yet.')
    ok = true
    await w.get('[data-testid="history-retry"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="history-error"]').exists()).toBe(false)
    expect(w.findAll('[data-testid="history-row"]')).toHaveLength(1)
  })

  it('keeps Load more with an inline error when a later page fails', async () => {
    let fail = true
    const page1 = Array.from({ length: 20 }, (_, i) => hr(i))
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(
      url.endsWith('page=1') ? json({ rows: page1 }) : fail ? new Response('x', { status: 500 }) : json({ rows: [hr(99)] }))))
    const w = mount(ReleaseHistory)
    await flushPromises()
    await w.get('[data-testid="history-more"]').trigger('click')
    await flushPromises()
    expect(w.findAll('[data-testid="history-row"]')).toHaveLength(20)
    expect(w.find('[data-testid="history-more-error"]').exists()).toBe(true)
    fail = false
    await w.get('[data-testid="history-more"]').trigger('click')
    await flushPromises()
    expect(w.findAll('[data-testid="history-row"]')).toHaveLength(21)
    expect(w.find('[data-testid="history-more-error"]').exists()).toBe(false)
  })
})
