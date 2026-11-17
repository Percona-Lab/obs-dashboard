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

function stubFetch(reportOk: boolean) {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.startsWith('/api/cve/report')) {
      return reportOk
        ? { ok: true, statusText: 'OK', json: async () => ({ generated_at: '', staging: { ours: [], base_os: [], unclassified: [], cves: 0, images: 0 }, released: [] }) }
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
    // the panel itself does not start the fetch; trigger it through Retry-less path
    const { useCveReport } = await import('../../composables/useCveReport')
    await useCveReport().refresh()
    await flushPromises()
    const err = w.find('[data-testid="cve-report-error"]')
    expect(err.exists()).toBe(true)
    expect(err.find('button').text()).toBe('Retry')
    await w.setProps({ view: 'images' })
    await flushPromises()
    expect(w.findComponent({ name: 'CveExposureTable' }).exists()).toBe(true)
  })
})
