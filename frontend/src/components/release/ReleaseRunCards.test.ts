// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ReleaseRunCards from './ReleaseRunCards.vue'
import type { InstanceCard } from '../../types/release'

const card = (o: Partial<InstanceCard> = {}): InstanceCard => ({
  instance: 'obs-a', state: 'running', url: 'https://github.com/o/r/actions/runs/1', started_at: null, finished_at: null, cdn: '', failed_units: [], errors: [], ...o,
})

describe('ReleaseRunCards', () => {
  it('shows failed units and errors on a failed card', () => {
    const w = mount(ReleaseRunCards, { props: { cards: [card({ state: 'failure', failed_units: ['ppg-18'], errors: ['boom'] })] } })
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
})
