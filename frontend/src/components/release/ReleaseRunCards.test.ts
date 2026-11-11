// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import ReleaseRunCards from './ReleaseRunCards.vue'
import type { InstanceCard } from '../../types/release'

const card = (o: Partial<InstanceCard> = {}): InstanceCard => ({
  instance: 'obs-a', state: 'running', url: 'https://github.com/o/r/actions/runs/1', started_at: null, finished_at: null, cdn: '', errors: [], units: [], nothing_to_sync: false, ...o,
})

describe('ReleaseRunCards', () => {
  it('lists every unit of an instance with its outcome', () => {
    const w = mount(ReleaseRunCards, { props: { cards: [
      card({ instance: 'boo', state: 'success', units: [{ project: 'ppg:releases:18', outcome: 'success' }, { project: 'ppg:releases:16:tde', outcome: 'success' }] }),
      card({ instance: 'labs', state: 'success', units: [{ project: 'ppg:releases:18', outcome: 'success' }, { project: 'ppg:releases:16:tde', outcome: 'skipped' }] }),
      card({ instance: 'bad', state: 'failure', units: [{ project: 'ppg:releases:18', outcome: 'failure' }], errors: ['boom'] }),
      card({ instance: 'busy', state: 'running', units: [] }),
    ] } })
    const [boo, labs, bad, busy] = w.findAll('[data-testid="run-card"]')
    const rows = (c: typeof boo) => c.findAll('[data-testid="card-unit"]').map((r) => `${r.attributes('data-outcome')}|${r.findAll('span').map((x) => x.text()).join(' ')}`)
    expect(rows(boo)).toEqual(['success|✓ ppg:releases:18 synced', 'success|✓ ppg:releases:16:tde synced'])
    expect(rows(labs)).toEqual(['success|✓ ppg:releases:18 synced', 'skipped|– ppg:releases:16:tde not on this instance'])
    expect(labs.get('[data-testid="card-state"]').text()).toBe('Done')
    expect(rows(bad)).toEqual(['failure|✗ ppg:releases:18 failed'])
    expect(bad.text()).toContain('boom')
    expect(busy.find('[data-testid="card-unit"]').exists()).toBe(false)
    w.unmount()
  })

  it('says so when an instance had nothing to sync', () => {
    const w = mount(ReleaseRunCards, { props: { cards: [
      card({ instance: 'labs', state: 'success', nothing_to_sync: true, units: [{ project: 'ppg:releases:16:tde', outcome: 'skipped' }] }),
    ] } })
    const labs = w.get('[data-testid="run-card"]')
    expect(labs.get('[data-testid="card-state"]').text()).toBe('Nothing to sync')
    expect(labs.get('[data-testid="card-unit"]').text()).toContain('not on this instance')
    w.unmount()
  })

  it('spins on cards that are queued or running, not on finished ones', () => {
    const w = mount(ReleaseRunCards, { props: { cards: [card({ instance: 'a', state: 'running' }), card({ instance: 'b', state: 'queued' }), card({ instance: 'c', state: 'success' }), card({ instance: 'd', state: 'failure' })] } })
    const spins = w.findAll('[data-testid="run-card"]').map((c) => c.find('[data-testid="spinner"]').exists())
    expect(spins).toEqual([true, true, false, false])
    w.unmount()
  })

  it('shows failed units and errors on a failed card', () => {
    const w = mount(ReleaseRunCards, { props: { cards: [card({ state: 'failure', units: [{ project: 'ppg-18', outcome: 'failure' }], errors: ['boom'] })] } })
    expect(w.text()).toContain('ppg-18')
    expect(w.text()).toContain('boom')
    const a = w.get('a')
    expect(a.text()).toBe('View log on GitHub')
    expect(a.attributes('rel')).toBe('noopener noreferrer')
  })

  it('formats elapsed time and omits unsafe links', () => {
    const w = mount(ReleaseRunCards, { props: { cards: [card({ state: 'success', url: 'javascript:x', started_at: '2026-01-01T00:00:00Z', finished_at: '2026-01-01T00:12:00Z' })] } })
    expect(w.get('[data-testid="card-elapsed"]').text()).toBe('12 min')
    expect(w.find('a').exists()).toBe(false)
  })

  it('the timer runs only while a card is queued or running', async () => {
    vi.useFakeTimers()
    try {
      const w = mount(ReleaseRunCards, { props: { cards: [card({ state: 'success' }), card({ state: 'failure' })] } })
      expect(vi.getTimerCount()).toBe(0)
      await w.setProps({ cards: [card({ state: 'running' })] })
      expect(vi.getTimerCount()).toBe(1)
      await w.setProps({ cards: [card({ state: 'success' })] })
      expect(vi.getTimerCount()).toBe(0)
      w.unmount()
    } finally { vi.useRealTimers() }
  })

  it('shows the download server state when it is not done', () => {
    const w = mount(ReleaseRunCards, { props: { cards: [card({ state: 'success', cdn: 'failure' }), card({ instance: 'b', state: 'success', cdn: 'success' }), card({ instance: 'c', state: 'success', cdn: 'skipped' })] } })
    const t = w.findAll('[data-testid="run-card"]').map((c) => c.text())
    expect(t[0]).toContain('Download servers: failure')
    expect(t[1]).not.toContain('Download servers')
    expect(t[2]).not.toContain('Download servers')
  })
})

afterEach(() => { vi.useRealTimers() })
