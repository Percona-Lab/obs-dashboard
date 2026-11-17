// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CvePanel from './CvePanel.vue'
import { resetCveReportForTests } from '../../composables/useCveReport'
import { resetAuthForTests } from '../../composables/useAuth'

const overview = {
  window: '24h', previous_window_rebuild_total: 0, top_repo: null, fix_time: null, by_instance: [],
  projects: [{
    project: 'ppg:releases:17', rebuilds: 0,
    images: [{ project: 'isv:percona:ppg:17', name: 'img', repo: 'images', base_os: '', critical: 1, high: 0, oldest_open_days: 2, avg_fix_hours: 0 }],
  }],
}

class FakeES { onmessage = null; close() {} }

const golangRow = {
  key: 'ours|golang', name: 'golang', kind: 'go_runtime', worst: 'CRITICAL', cves: 1,
  fixes: [{ pkg: 'golang', installed: '1.22.1', fixed: '1.22.5' }],
  entries: [{ id: 'CVE-2025-0002', pkg: 'golang', severity: 'CRITICAL', title: 't', seen_key: 'CVE-2025-0002|golang|' }],
  images: [{ project: 'ppg:staging', package: 'p', repo: 'ubi9', arches: ['x86_64'] }],
}
const releasedImage = {
  project: 'ppg:releases:18:containers', package: 'p', repo: 'ubi9', version: '1', staging_project: 'ppg:staging', staging_version: '2',
  total: 1, fixed_in_staging: 0,
  cves: [{ id: 'CVE-2025-0002', pkg: 'golang', installed: '1.22.1', fixed: '1.22.5', severity: 'CRITICAL', title: 't', status: 'open', staging_row_key: 'ours|golang' }],
}

function stubFetch(reportOk: boolean, withRows = false) {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.startsWith('/api/cve/report')) {
      return reportOk
        ? { ok: true, statusText: 'OK', json: async () => withRows
          ? ({ generated_at: '', staging: { ours: [golangRow], base_os: [], unclassified: [], cves: 1, images: 1 }, released: [releasedImage] })
          : ({ generated_at: '', staging: { ours: [], base_os: [], unclassified: [], cves: 0, images: 0 }, released: [] }) }
        : { ok: false, statusText: 'Internal Server Error', json: async () => ({}) }
    }
    return { ok: true, statusText: 'OK', json: async () => overview }
  }))
}

beforeEach(() => {
  resetAuthForTests()
  resetCveReportForTests()
  vi.stubGlobal('EventSource', FakeES)
})
afterEach(() => vi.unstubAllGlobals())

describe('CvePanel', () => {
  it('renders the by-image table for view=images', async () => {
    stubFetch(true)
    const w = mount(CvePanel, { props: { view: 'images' } })
    await flushPromises()
    expect(w.text()).toContain('img')
    expect(w.findComponent({ name: 'CveExposureTable' }).exists()).toBe(true)
  })

  it('fetches overview data only while the By image view is shown', async () => {
    stubFetch(true)
    const overviewCalls = () => (fetch as unknown as ReturnType<typeof vi.fn>).mock.calls.filter(c => String(c[0]).startsWith('/api/overview')).length
    const w = mount(CvePanel, { props: { view: 'staging' } })
    await flushPromises()
    expect(overviewCalls()).toBe(0)
    await w.setProps({ view: 'images' })
    await flushPromises()
    expect(overviewCalls()).toBeGreaterThan(0)
  })

  it('emits update:view when a segment is clicked', async () => {
    stubFetch(true)
    const w = mount(CvePanel, { props: { view: 'staging' } })
    await flushPromises()
    const btn = w.findAll('button').find(b => b.text() === 'Released')!
    await btn.trigger('click')
    expect(w.emitted('update:view')).toEqual([['released']])
  })

  it('shows an error with Retry on staging, while By image still renders', async () => {
    stubFetch(false)
    const w = mount(CvePanel, { props: { view: 'staging' } })
    await w.vm.$nextTick()
    // the panel does not start the fetch itself; trigger the first (failing) load here
    const { useCveReport } = await import('../../composables/useCveReport')
    await useCveReport().refresh()
    await flushPromises()
    const err = w.find('[data-testid="cve-report-error"]')
    expect(err.exists()).toBe(true)
    expect(err.find('button').text()).toBe('Retry')
    stubFetch(true)
    await err.find('button').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="cve-report-error"]').exists()).toBe(false)
    expect(w.findComponent({ name: 'CveStagingView' }).exists()).toBe(true)
    await w.setProps({ view: 'images' })
    await flushPromises()
    expect(w.findComponent({ name: 'CveExposureTable' }).exists()).toBe(true)
  })

  it('jumps from a released CVE to its expanded staging row', async () => {
    stubFetch(true, true)
    const w = mount(CvePanel, { props: { view: 'released' } })
    const { useCveReport } = await import('../../composables/useCveReport')
    await useCveReport().refresh()
    await flushPromises()
    const rv = w.findComponent({ name: 'CveReleasedView' })
    expect(rv.exists()).toBe(true)
    rv.vm.$emit('open-staging-row', 'ours|golang')
    expect(w.emitted('update:view')).toEqual([['staging']])
    await w.setProps({ view: 'staging' })
    await flushPromises()
    expect(w.find('[data-testid="cve-row-ours|golang"]').attributes('aria-expanded')).toBe('true')
  })

  it('never shows the released view while the report is missing', async () => {
    stubFetch(false)
    const w = mount(CvePanel, { props: { view: 'released' } })
    const { useCveReport } = await import('../../composables/useCveReport')
    await useCveReport().refresh()
    await flushPromises()
    expect(w.findComponent({ name: 'CveReleasedView' }).exists()).toBe(false)
  })
})
