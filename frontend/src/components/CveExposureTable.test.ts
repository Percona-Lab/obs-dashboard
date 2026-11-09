// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import CveExposureTable from './CveExposureTable.vue'
import { useAuth, resetAuthForTests } from '../composables/useAuth'
import type { OverviewProject } from '../types/overview'

const vulnerable: OverviewProject[] = [{
  project: 'ppg:releases:17',
  rebuilds: 0,
  images: [{ project: 'isv:percona:ppg:17', name: 'img', repo: 'images', base_os: '', critical: 1, high: 0, oldest_open_days: 2, avg_fix_hours: 0 }],
}]

function mountTable(projects = vulnerable) {
  return mount(CveExposureTable, { props: { projects, accentOf: () => '#000' } })
}

beforeEach(() => { resetAuthForTests() })
afterEach(() => { vi.unstubAllGlobals() })

describe('CveExposureTable rescan button', () => {
  it('is hidden when signed out', () => {
    expect(mountTable().find('[data-testid="cve-rescan"]').exists()).toBe(false)
  })

  it('is hidden when no image has CVEs', () => {
    useAuth().user.value = 'alice@percona.com'
    const clean = [{ ...vulnerable[0], images: [{ ...vulnerable[0].images[0], critical: 0 }] }]
    expect(mountTable(clean).find('[data-testid="cve-rescan"]').exists()).toBe(false)
  })

  it('posts the rescan and shows the queued count', async () => {
    useAuth().user.value = 'alice@percona.com'
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ queued: 3 }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    const w = mountTable()
    await w.find('[data-testid="cve-rescan"]').trigger('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledWith('/api/cve/rescan', { method: 'POST' })
    expect(w.find('[data-testid="cve-rescan-status"]').text()).toBe('Queued 3 images')
    expect(w.find('[data-testid="cve-rescan"]').attributes('disabled')).toBeDefined()
  })

  it('shows the server error and re-enables the button', async () => {
    useAuth().user.value = 'alice@percona.com'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('boom', { status: 500 })))
    const w = mountTable()
    await w.find('[data-testid="cve-rescan"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="cve-rescan-status"]').text()).toBe('Rescan failed: boom')
    expect(w.find('[data-testid="cve-rescan"]').attributes('disabled')).toBeUndefined()
  })

  it('signs the user out on 401', async () => {
    useAuth().user.value = 'alice@percona.com'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 401 })))
    const w = mountTable()
    await w.find('[data-testid="cve-rescan"]').trigger('click')
    await flushPromises()
    expect(useAuth().signedIn.value).toBe(false)
    expect(w.find('[data-testid="cve-rescan"]').exists()).toBe(false)
  })
})
