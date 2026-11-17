// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { resetCveReportForTests, useCveReport } from './useCveReport'
import { resetCveSeenForTests, useCveSeen } from './useCveSeen'
import type { CveReport, CveRow } from '../types/cveReport'

function row(name: string, ids: string[]): CveRow {
  return {
    key: `ours|${name}`, name, kind: 'obs_package', worst: 'HIGH', cves: ids.length, fixes: [], images: [],
    entries: ids.map(id => ({ id, pkg: name, severity: 'HIGH' as const, title: 't', seen_key: `${id}|${name}|` })),
  }
}

export const fixtureReport: CveReport = {
  generated_at: '2026-11-17T00:00:00Z',
  staging: { ours: [row('etcd', ['CVE-1', 'CVE-2'])], base_os: [row('openssl', ['CVE-3'])], unclassified: [], cves: 3, images: 2 },
  released: [],
}

class FakeES { static last: FakeES | null = null; onmessage: (() => void) | null = null; constructor() { FakeES.last = this } close() {} }

let fetchMock: ReturnType<typeof vi.fn>
beforeEach(() => {
  localStorage.clear()
  resetCveSeenForTests()
  resetCveReportForTests()
  fetchMock = vi.fn(async () => ({ ok: true, statusText: 'OK', json: async () => fixtureReport }))
  vi.stubGlobal('fetch', fetchMock)
  vi.stubGlobal('EventSource', FakeES)
})
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

describe('useCveReport', () => {
  it('start() fetches once even when called twice', async () => {
    const r = useCveReport()
    r.start(); r.start()
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][0]).toBe('/api/cve/report')
    expect(r.report.value).toEqual(fixtureReport)
  })

  it('a 500 sets error and leaves report null; refresh() clears it on success', async () => {
    fetchMock.mockResolvedValueOnce({ ok: false, statusText: 'Internal Server Error', json: async () => ({}) })
    const r = useCveReport()
    await r.refresh()
    expect(r.error.value).toBe('Internal Server Error')
    expect(r.report.value).toBeNull()
    await r.refresh()
    expect(r.error.value).toBeNull()
    expect(r.report.value).not.toBeNull()
  })

  it('newCount counts distinct unseen keys, and is 0 after markAllSeen', async () => {
    const r = useCveReport()
    expect(r.newCount.value).toBe(0)
    await r.refresh()
    expect(r.newCount.value).toBe(3)
    useCveSeen().markAllSeen(['CVE-1|etcd|', 'CVE-2|etcd|', 'CVE-3|openssl|'])
    expect(r.newCount.value).toBe(0)
  })

  it('refetches 2 s after a stream message', async () => {
    vi.useFakeTimers()
    const r = useCveReport()
    r.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    FakeES.last!.onmessage!()
    await vi.advanceTimersByTimeAsync(2000)
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})
