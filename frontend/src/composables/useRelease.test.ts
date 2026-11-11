// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ref, nextTick } from 'vue'
import { useRelease, resetReleaseForTests } from './useRelease'
import { resetAuthForTests } from './useAuth'

const snap = { releases: [], stale: false, error: '', refreshed_at: '2026-11-11T00:00:00Z' }
const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status })

beforeEach(() => { vi.useFakeTimers(); resetAuthForTests(); resetReleaseForTests() })
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

describe('useRelease', () => {
  it('polls every 15s only while active', async () => {
    const f = vi.fn(() => Promise.resolve(json(snap)))
    vi.stubGlobal('fetch', f)
    const active = ref(true)
    useRelease().start(active)
    await vi.advanceTimersByTimeAsync(0)
    expect(f).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(15000)
    expect(f).toHaveBeenCalledTimes(2)
    active.value = false
    await nextTick()
    await vi.advanceTimersByTimeAsync(30000)
    expect(f).toHaveBeenCalledTimes(2)
  })

  it('does not poll while the document is hidden', async () => {
    const f = vi.fn(() => Promise.resolve(json(snap)))
    vi.stubGlobal('fetch', f)
    const vis = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    useRelease().start(ref(true))
    await vi.advanceTimersByTimeAsync(30000)
    expect(f).toHaveBeenCalledTimes(0)
    vis.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(0)
    expect(f).toHaveBeenCalledTimes(1)
    vis.mockRestore()
  })

  it('loadStatus treats non-200 as disabled', async () => {
    const { enabled, loadStatus } = useRelease()
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ enabled: true }))))
    await loadStatus()
    expect(enabled.value).toBe(true)
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('no', { status: 401 }))))
    await loadStatus()
    expect(enabled.value).toBe(false)
  })

  it('act refreshes and reports 409', async () => {
    const f = vi.fn((_url: string, init?: RequestInit) =>
      Promise.resolve(init?.method === 'POST' ? new Response('conflict', { status: 409 }) : json(snap)))
    vi.stubGlobal('fetch', f)
    const r = useRelease()
    expect(await r.act('r1', 'release', 'abc', true)).toBe(false)
    expect(r.notice.value).toBe('This release changed. The view has been refreshed.')
    const calls = f.mock.calls as unknown as [string, RequestInit?][]
    expect(calls[0][0]).toBe('/api/release/releases/r1/release')
    expect(JSON.parse(calls[0][1]!.body as string)).toEqual({ head: 'abc', confirm_failed: true })
    expect(calls[1][0]).toBe('/api/release/releases')
  })

  it('act surfaces other failure text and succeeds on 204', async () => {
    const r = useRelease()
    vi.stubGlobal('fetch', vi.fn((_u: string, init?: RequestInit) =>
      Promise.resolve(init?.method === 'POST' ? new Response('bad head', { status: 400 }) : json(snap))))
    expect(await r.act('r1', 'abandon', 'abc')).toBe(false)
    expect(r.notice.value).toBe('bad head')
    vi.stubGlobal('fetch', vi.fn((_u: string, init?: RequestInit) =>
      Promise.resolve(init?.method === 'POST' ? new Response(null, { status: 204 }) : json(snap))))
    expect(await r.act('r1', 'abandon', 'abc')).toBe(true)
    expect(r.notice.value).toBe('')
  })

  it('cut posts the unit list', async () => {
    const f = vi.fn((_u: string, init?: RequestInit) =>
      Promise.resolve(init?.method === 'POST' ? json({ id: 'r9' }, 201) : json(snap)))
    vi.stubGlobal('fetch', f)
    expect(await useRelease().cut(['ppg:staging:18'])).toBe('r9')
    const call = (f.mock.calls as unknown as [string, RequestInit][])[0]
    expect(call[0]).toBe('/api/release/releases')
    expect(JSON.parse(call[1].body as string)).toEqual({ units: ['ppg:staging:18'] })
  })

  it('saveChangelog PUTs and resetChangelog POSTs', async () => {
    const f = vi.fn((_u: string, init?: RequestInit) =>
      Promise.resolve(init?.method === 'GET' || !init ? json(snap) : json({ head: 'new' })))
    vi.stubGlobal('fetch', f)
    const r = useRelease()
    expect(await r.saveChangelog('r1', 'h', 'u', 's')).toBe(true)
    expect(await r.resetChangelog('r1', 'h', 'u')).toBe(true)
    const calls = f.mock.calls as unknown as [string, RequestInit?][]
    expect(calls[0][0]).toBe('/api/release/releases/r1/changelog')
    expect(calls[0][1]!.method).toBe('PUT')
    expect(JSON.parse(calls[0][1]!.body as string)).toEqual({ head: 'h', unit: 'u', section: 's' })
    const reset = calls.find(c => c[0] === '/api/release/releases/r1/changelog/reset')!
    expect(JSON.parse(reset[1]!.body as string)).toEqual({ head: 'h', unit: 'u' })
  })

  it('loadHistory returns rows', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ rows: [{ tag: 't' }] }))))
    expect(await useRelease().loadHistory(2)).toEqual([{ tag: 't' }])
    expect((fetch as unknown as ReturnType<typeof vi.fn>).mock.calls[0][0]).toBe('/api/release/history?page=2')
  })
})
