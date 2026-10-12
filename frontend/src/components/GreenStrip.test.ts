// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import GreenStrip from './GreenStrip.vue'
import type { Package } from '../types/api'

function pkg(name: string, version?: string): Package {
  return {
    project: 'ppg:staging:18',
    name,
    version,
    rollup_state: 'published',
    ok_targets: 1,
    total_targets: 1,
    targets: [],
    updated_at: '2026-10-12T00:00:00Z',
  } as Package
}

describe('GreenStrip chips', () => {
  it('shows the built version next to the package name', () => {
    const w = mount(GreenStrip, { props: { packages: [pkg('percona-patroni', '4.1.5-7')] } })
    const chip = w.find('.pkg-pill')
    expect(chip.text()).toContain('percona-patroni')
    expect(chip.find('[data-role="version"]').text()).toBe('4.1.5-7')
  })

  it('shows only the name when the package has no version', () => {
    const w = mount(GreenStrip, { props: { packages: [pkg('etcd')] } })
    const chip = w.find('.pkg-pill')
    expect(chip.text()).toBe('etcd')
    expect(chip.find('[data-role="version"]').exists()).toBe(false)
  })
})
