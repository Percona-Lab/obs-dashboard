// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import CveStagingView from './CveStagingView.vue'
import { resetCveSeenForTests, SEEN_STORAGE_KEY, useCveSeen } from '../../composables/useCveSeen'
import { resetCveReportForTests, useCveReport } from '../../composables/useCveReport'
import { resetAuthForTests, useAuth } from '../../composables/useAuth'
import type { CveRow, CveStaging } from '../../types/cveReport'

const img = (repo: string) => ({ project: 'ppg:staging:17', package: 'postgresql', repo, arches: ['x86_64', 'aarch64'] })

function golang(): CveRow {
  return {
    key: 'ours|golang', name: 'golang', kind: 'go_runtime', worst: 'CRITICAL', cves: 2,
    fixes: [{ pkg: 'stdlib', installed: '1.22.1', fixed: '1.22.5' }, { pkg: 'golang', installed: '1.22.1', fixed: '1.22.6' }],
    entries: [
      { id: 'CVE-2024-0001', pkg: 'stdlib', severity: 'CRITICAL', title: 'Go stdlib flaw', seen_key: 'CVE-2024-0001|stdlib|' },
      { id: 'CVE-2024-0002', pkg: 'stdlib', severity: 'HIGH', title: 'Go net flaw', seen_key: 'CVE-2024-0002|stdlib|' },
    ],
    images: [img('ubi8'), img('ubi9')],
  }
}
function etcd(): CveRow {
  return {
    key: 'ours|etcd', name: 'etcd', kind: 'bundled_library', worst: 'HIGH', cves: 1,
    fixes: [{ pkg: 'grpc', installed: '1.0', fixed: '1.1' }],
    entries: [{ id: 'CVE-2024-0003', pkg: 'grpc', path: 'usr/bin/etcd', severity: 'HIGH', title: 'grpc flaw', seen_key: 'CVE-2024-0003|grpc|usr/bin/etcd' }],
    images: [img('ubi9')],
  }
}
function openssl(): CveRow {
  return {
    key: 'base_os|openssl', name: 'openssl', kind: 'base_os', worst: 'HIGH', cves: 1,
    fixes: [{ pkg: 'openssl', installed: '3.0.1', fixed: '3.0.2' }, { pkg: 'openssl-libs', installed: '3.0.1', fixed: '3.0.2' }],
    entries: [
      { id: 'CVE-2024-0004', pkg: 'openssl', severity: 'HIGH', title: 'openssl flaw', seen_key: 'CVE-2024-0004|openssl|' },
      { id: 'CVE-2024-0004', pkg: 'openssl-libs', severity: 'HIGH', title: 'openssl flaw', seen_key: 'CVE-2024-0004|openssl-libs|' },
    ],
    images: [img('ubi9')],
  }
}
function staging(unclassified: CveRow[] = []): CveStaging {
  return { ours: [etcd(), golang()], base_os: [openssl()], unclassified, cves: 4, images: 2 }
}

function mountView(props: { staging?: CveStaging; focusRowKey?: string | null } = {}) {
  return mount(CveStagingView, { props: { staging: props.staging ?? staging(), focusRowKey: props.focusRowKey ?? null } })
}
function rowOf(w: ReturnType<typeof mountView>, key: string) {
  return w.find(`[data-testid="cve-row-${key}"]`)
}
function markSeen(keys: string[]) {
  localStorage.setItem(SEEN_STORAGE_KEY, JSON.stringify(keys))
  resetCveSeenForTests()
}

beforeEach(() => {
  localStorage.clear()
  resetCveSeenForTests()
  resetCveReportForTests()
  resetAuthForTests()
})
afterEach(() => vi.unstubAllGlobals())

describe('CveStagingView', () => {
  it('shows the header line', () => {
    expect(mountView().find('[data-testid="cve-staging-header"]').text()).toBe('2 things to bump · 4 CVEs · 2 images')
  })

  it('renders Ours and Base OS, and Not classified only with rows', () => {
    const w = mountView()
    expect(w.text()).toContain('Ours to bump')
    expect(w.text()).toContain('Base OS')
    expect(w.text()).not.toContain('Not classified yet')
    const u: CveRow = { ...etcd(), key: 'unclassified|thing', name: 'thing', kind: 'unclassified', vendor: 'Acme' }
    const w2 = mountView({ staging: staging([u]) })
    expect(w2.text()).toContain('Not classified yet')
    expect(w2.text()).toContain('Acme')
  })

  it('shows a one-line empty state for an empty Ours or Base OS list', () => {
    const w = mountView({ staging: { ours: [], base_os: [], unclassified: [], cves: 0, images: 0 } })
    expect(w.findAll('[data-testid="cve-empty"]')).toHaveLength(2)
    expect(w.find('[data-testid="cve-staging-header"]').text()).toBe('0 things to bump · 0 CVEs · 0 images')
  })

  it('flags rows with new entries and sorts them first', () => {
    markSeen(['CVE-2024-0003|grpc|usr/bin/etcd'])
    const w = mountView()
    expect(rowOf(w, 'ours|golang').find('[data-testid="cve-new-tag"]').exists()).toBe(true)
    expect(rowOf(w, 'base_os|openssl').find('[data-testid="cve-new-tag"]').exists()).toBe(true)
    expect(rowOf(w, 'ours|etcd').find('[data-testid="cve-new-tag"]').exists()).toBe(false)
    const rows = w.findAll('[data-testid^="cve-row-"]').map(r => r.attributes('data-testid'))
    expect(rows.indexOf('cve-row-ours|golang')).toBeLessThan(rows.indexOf('cve-row-ours|etcd'))
  })

  it('expands a row to its entries and images, tagging only new entries', async () => {
    markSeen(['CVE-2024-0002|stdlib|'])
    const w = mountView()
    const btn = rowOf(w, 'ours|golang')
    expect(btn.attributes('aria-expanded')).toBe('false')
    await btn.trigger('click')
    expect(btn.attributes('aria-expanded')).toBe('true')
    const region = w.find(`#${btn.attributes('aria-controls')}`)
    expect(region.text()).toContain('CVE-2024-0001')
    expect(region.text()).toContain('Go stdlib flaw')
    expect(region.text()).toContain('CVE-2024-0002')
    expect(region.text()).toContain('Go net flaw')
    expect(region.text()).toContain('ppg:staging:17')
    expect(region.text()).toContain('postgresql')
    expect(region.text()).toContain('ubi8')
    expect(region.text()).toContain('x86_64')
    expect(region.text()).toContain('aarch64')
    const entries = region.findAll('[data-testid="cve-entry"]')
    expect(entries).toHaveLength(2)
    expect(entries[0].find('[data-testid="cve-new-tag"]').exists()).toBe(true)
    expect(entries[1].find('[data-testid="cve-new-tag"]').exists()).toBe(false)
  })

  it('does not clear markers on opening or remounting', async () => {
    const w = mountView()
    await rowOf(w, 'ours|golang').trigger('click')
    w.unmount()
    const w2 = mountView()
    expect(w2.findAll('[data-testid="cve-new-tag"]').length).toBeGreaterThan(0)
    expect(localStorage.getItem(SEEN_STORAGE_KEY)).toBeNull()
  })

  it('Mark all as seen clears every marker and the header count', async () => {
    const { newCount } = useCveReport()
    const w = mountView()
    await w.find('[data-testid="cve-mark-seen"]').trigger('click')
    expect(w.findAll('[data-testid="cve-new-tag"]')).toHaveLength(0)
    expect(newCount.value).toBe(0)
  })

  it('has no markers and no Mark all as seen when storage is unavailable', () => {
    localStorage.setItem(SEEN_STORAGE_KEY, 'not json')
    resetCveSeenForTests()
    expect(useCveSeen().available.value).toBe(false)
    const w = mountView()
    expect(w.findAll('[data-testid="cve-new-tag"]')).toHaveLength(0)
    expect(w.find('[data-testid="cve-mark-seen"]').exists()).toBe(false)
  })

  it('shows Rescan only when signed in', () => {
    expect(mountView().find('[data-testid="cve-rescan"]').exists()).toBe(false)
    useAuth().user.value = 'alice@percona.com'
    expect(mountView().find('[data-testid="cve-rescan"]').exists()).toBe(true)
  })

  it('posts the rescan like the by-image table does', async () => {
    const fetchMock = vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ queued: 2 }) }))
    vi.stubGlobal('fetch', fetchMock)
    useAuth().user.value = 'alice@percona.com'
    const w = mountView()
    await w.find('[data-testid="cve-rescan"]').trigger('click')
    await vi.waitFor(() => expect(w.find('[data-testid="cve-rescan-status"]').text()).toBe('Queued 2 images'))
    expect(fetchMock).toHaveBeenCalledWith('/api/cve/rescan', { method: 'POST' })
  })

  it('shows kind labels and every fix line', () => {
    const w = mountView()
    expect(rowOf(w, 'ours|golang').text()).toContain('Go runtime')
    expect(rowOf(w, 'ours|etcd').text()).toContain('bundled library')
    const text = rowOf(w, 'ours|golang').text()
    expect(text).toContain('stdlib 1.22.1 → 1.22.5')
    expect(text).toContain('golang 1.22.1 → 1.22.6')
  })

  it('renders the focused row expanded', () => {
    const w = mountView({ focusRowKey: 'ours|etcd' })
    expect(rowOf(w, 'ours|etcd').attributes('aria-expanded')).toBe('true')
    expect(rowOf(w, 'ours|golang').attributes('aria-expanded')).toBe('false')
  })
})
