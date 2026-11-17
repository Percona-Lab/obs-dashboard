// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import CveReleasedView from './CveReleasedView.vue'
import type { ReleasedImage } from '../../types/cveReport'

const image: ReleasedImage = {
  project: 'ppg:releases:18:containers',
  package: 'percona-distribution-postgresql',
  repo: 'ubi9',
  version: '18.4-1-1.1',
  staging_project: 'ppg:staging:18:containers',
  staging_version: '18.4-1-2.34',
  total: 3,
  fixed_in_staging: 1,
  cves: [
    { id: 'CVE-2025-0001', pkg: 'openssl', installed: '3.0.1', fixed: '3.0.2', severity: 'HIGH', title: 't1', status: 'fixed' },
    { id: 'CVE-2025-0002', pkg: 'golang', installed: '1.22.1', fixed: '1.22.5', severity: 'CRITICAL', title: 't2', status: 'open', staging_row_key: 'ours|golang' },
    { id: 'CVE-2025-0003', pkg: 'zlib', installed: '1.2.1', fixed: '1.2.2', severity: 'HIGH', title: 't3', status: 'no_staging' },
  ],
}

describe('CveReleasedView', () => {
  it('shows the fixed count, both versions and the PG major with repo', () => {
    const w = mount(CveReleasedView, { props: { released: [image] } })
    const text = w.text()
    expect(text).toContain('1 of 3')
    expect(text).toContain('18.4-1-1.1')
    expect(text).toContain('18.4-1-2.34')
    expect(text).toContain('18 · ubi9')
  })

  it('expands to the status of each CVE and links an open one to its staging row', async () => {
    const w = mount(CveReleasedView, { props: { released: [image] } })
    const head = w.find('button[aria-expanded]')
    expect(head.attributes('aria-expanded')).toBe('false')
    await head.trigger('click')
    expect(head.attributes('aria-expanded')).toBe('true')
    const text = w.text()
    expect(text).toContain('Fixed in staging')
    expect(text).toContain('Still open in staging')
    expect(text).toContain('No staging image to compare')
    expect(text).toContain('CVE-2025-0002')
    const link = w.findAll('button').find(b => b.text() === 'golang')!
    expect(link).toBeTruthy()
    await link.trigger('click')
    expect(w.emitted('open-staging-row')).toEqual([['ours|golang']])
  })

  it('shows a dash when there is no staging version', () => {
    const w = mount(CveReleasedView, { props: { released: [{ ...image, staging_version: undefined, fixed_in_staging: 0 }] } })
    expect(w.text()).toContain('—')
  })

  it('has no new markers and no mark-all-as-seen', async () => {
    const w = mount(CveReleasedView, { props: { released: [image] } })
    await w.find('button[aria-expanded]').trigger('click')
    expect(w.find('[data-testid="cve-new-tag"]').exists()).toBe(false)
    expect(w.find('[data-testid="cve-mark-seen"]').exists()).toBe(false)
  })

  it('renders an empty state for no released images', () => {
    const w = mount(CveReleasedView, { props: { released: [] } })
    expect(w.find('[data-testid="cve-empty"]').exists()).toBe(true)
  })
})
