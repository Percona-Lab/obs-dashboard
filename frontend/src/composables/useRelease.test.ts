// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ref, nextTick } from 'vue'
import { useRelease, resetReleaseForTests } from './useRelease'
import { resetAuthForTests } from './useAuth'
import { getDraft, hasDrafts, resetDraftsForTests, setDraft } from './useChangelogDrafts'

const snap = { releases: [], stale: false, error: '', refreshed_at: '2026-11-11T00:00:00Z' }
const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status })

beforeEach(() => { vi.useFakeTimers(); resetAuthForTests(); resetReleaseForTests(); resetDraftsForTests() })
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

  it('loadStatus keeps enabled on network error or 5xx and reports completion', async () => {
    const { enabled, loadStatus } = useRelease()
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ enabled: true }))))
    expect(await loadStatus()).toBe(true)
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('net'))))
    expect(await loadStatus()).toBe(false)
    expect(enabled.value).toBe(true)
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('x', { status: 503 }))))
    expect(await loadStatus()).toBe(false)
    expect(enabled.value).toBe(true)
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ enabled: false }))))
    expect(await loadStatus()).toBe(true)
    expect(enabled.value).toBe(false)
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ enabled: true }))))
    await loadStatus()
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('no', { status: 404 }))))
    expect(await loadStatus()).toBe(true)
    expect(enabled.value).toBe(false)
  })

  it('loadHistory returns null on failure', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('x', { status: 500 }))))
    expect(await useRelease().loadHistory(1)).toBeNull()
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('net'))))
    expect(await useRelease().loadHistory(1)).toBeNull()
  })

  it('names the action in flight until the view has been refreshed', async () => {
    let finishPost!: (r: Response) => void
    let finishGet!: (r: Response) => void
    vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) => new Promise<Response>((res) => {
      if (init?.method === 'POST') finishPost = res
      else finishGet = res
    })))
    const r = useRelease()
    expect(r.pending.value).toBe('')
    const done = r.act('r1', 'approve-qa', 'abc')
    expect(r.pending.value).toBe('approve-qa')
    finishPost(new Response(null, { status: 204 }))
    await Promise.resolve(); await Promise.resolve()
    // the write is done, the refresh is not: still pending
    expect(r.pending.value).toBe('approve-qa')
    await vi.waitFor(() => expect(finishGet).toBeTypeOf('function'))
    finishGet(json(snap))
    await done
    expect(r.pending.value).toBe('')
    expect(r.busy.value).toBe(false)
  })

  it('keeps the action pending until the release changes on screen', async () => {
    const rel = (sync_state: string) => ({ id: 'r1', step: 'sync', head_sha: 'abc', cut: null, sync_state, sync: [], qa: null, final: null })
    let state = 'failure'
    const f = vi.fn((_url: string, init?: RequestInit) =>
      Promise.resolve(init?.method === 'POST' ? new Response(null, { status: 204 }) : json({ ...snap, releases: [rel(state)] })))
    vi.stubGlobal('fetch', f)
    const r = useRelease()
    await r.refresh()
    let finished = false
    const done = r.act('r1', 'retry-sync', 'abc').then((ok) => { finished = true; return ok })
    // the write and the first refresh are done, but GitHub still shows the failed run
    await vi.advanceTimersByTimeAsync(100)
    expect(r.pending.value).toBe('retry-sync')
    expect(r.busy.value).toBe(true)
    expect(finished).toBe(false)
    // it keeps looking every few seconds
    await vi.advanceTimersByTimeAsync(3000)
    expect(r.pending.value).toBe('retry-sync')
    // the new run shows up
    state = 'queued'
    await vi.advanceTimersByTimeAsync(3000)
    expect(await done).toBe(true)
    expect(r.pending.value).toBe('')
    expect(r.busy.value).toBe(false)
  })

  it('stops waiting for a change after 90 seconds', async () => {
    const rel = { id: 'r1', step: 'sync', head_sha: 'abc', cut: null, sync_state: 'failure', sync: [], qa: null, final: null }
    vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) =>
      Promise.resolve(init?.method === 'POST' ? new Response(null, { status: 204 }) : json({ ...snap, releases: [rel] }))))
    const r = useRelease()
    await r.refresh()
    const done = r.act('r1', 'retry-sync', 'abc')
    await vi.advanceTimersByTimeAsync(89_000)
    expect(r.pending.value).toBe('retry-sync')
    await vi.advanceTimersByTimeAsync(4_000)
    expect(await done).toBe(true)
    expect(r.pending.value).toBe('')
    expect(r.notice.value).toBe('GitHub has not shown a change yet. The view keeps refreshing.')
  })

  it('does not wait when the action failed or the release was not on screen', async () => {
    const rel = { id: 'r1', step: 'sync', head_sha: 'abc', cut: null, sync_state: 'failure', sync: [], qa: null, final: null }
    vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) =>
      Promise.resolve(init?.method === 'POST' ? new Response('conflict', { status: 409 }) : json({ ...snap, releases: [rel] }))))
    const r = useRelease()
    await r.refresh()
    expect(await r.act('r1', 'retry-sync', 'abc')).toBe(false)
    expect(r.pending.value).toBe('')
    vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) =>
      Promise.resolve(init?.method === 'POST' ? new Response(null, { status: 204 }) : json({ ...snap, releases: [rel] }))))
    expect(await r.act('other', 'abandon', 'abc')).toBe(true)
    expect(r.pending.value).toBe('')
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
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ rows: [{ tag: 't' }], more: true }))))
    expect(await useRelease().loadHistory(2)).toEqual({ rows: [{ tag: 't' }], more: true })
    expect((fetch as unknown as ReturnType<typeof vi.fn>).mock.calls[0][0]).toBe('/api/release/history?page=2')
  })

  // A fetch mock whose GET /releases calls stay pending until resolved by the test.
  function pendingGets() {
    const gets: ((s: unknown) => void)[] = []
    const f = vi.fn((_url: string, init?: RequestInit) => {
      if (init?.method === 'POST') return Promise.resolve(json({ id: 'r9' }, 201))
      return new Promise<Response>(res => { gets.push(s => res(json(s))) })
    })
    vi.stubGlobal('fetch', f)
    const getCount = () => (f.mock.calls as unknown as [string, RequestInit?][])
      .filter(c => c[0] === '/api/release/releases' && !c[1]?.method).length
    return { gets, getCount }
  }
  const snapA = { ...snap, error: 'A' }
  const snapB = { ...snap, error: 'B' }

  it('an action refresh does not reuse a poll that started before the write', async () => {
    const { gets, getCount } = pendingGets()
    const r = useRelease()
    void r.refresh() // poll in flight
    await vi.advanceTimersByTimeAsync(0)
    const done = r.cut(['u1'])
    await vi.advanceTimersByTimeAsync(0)
    expect(getCount()).toBe(2)
    gets[0](snapA)
    await vi.advanceTimersByTimeAsync(0)
    gets[1](snapB)
    await done
    expect(r.snapshot.value?.error).toBe('B')
  })

  it('a late older refresh does not overwrite a newer snapshot', async () => {
    const { gets } = pendingGets()
    const r = useRelease()
    void r.refresh()
    await vi.advanceTimersByTimeAsync(0)
    const done = r.cut(['u1'])
    await vi.advanceTimersByTimeAsync(0)
    gets[1](snapB)
    await done
    gets[0](snapA)
    await vi.advanceTimersByTimeAsync(0)
    expect(r.snapshot.value?.error).toBe('B')
  })

  it('loadHistory reads an answer without "more" as the last page', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ rows: [] }))))
    expect(await useRelease().loadHistory(1)).toEqual({ rows: [], more: false })
  })

  it('sends retry-qa like the other actions', async () => {
    const f = vi.fn((_url: string, _init?: RequestInit) => Promise.resolve(json(snap)))
    vi.stubGlobal('fetch', f)
    expect(await useRelease().act('r1', 'retry-qa', 'h')).toBe(true)
    expect(f.mock.calls[0][0]).toBe('/api/release/releases/r1/retry-qa')
    expect(f.mock.calls[0][1]!.method).toBe('POST')
  })

  it('a refresh drops the drafts of releases that left the snapshot, and drafts equal to it', async () => {
    const unit = (name: string, changelog: string) => ({ unit: name, release_project: '', release_dir: '', release_id: '1', tag: '', changelog, generated: null })
    setDraft('gone', 'u', { text: 'mine', synced: 'orig' })
    setDraft('here', 'u', { text: 'mine', synced: 'orig' })
    setDraft('here', 'same', { text: 'saved\n', synced: 'orig' })
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ ...snap, releases: [{ id: 'here', units: [unit('u', 'orig'), unit('same', 'saved')] }] }))))
    await useRelease().refresh()
    expect(hasDrafts('gone')).toBe(false)
    expect(getDraft('here', 'u')).toEqual({ text: 'mine', synced: 'orig' })
    expect(getDraft('here', 'same')).toBeUndefined()
  })

  it('a failed refresh keeps every draft', async () => {
    setDraft('r1', 'u', { text: 'mine', synced: 'orig' })
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('x', { status: 502 }))))
    await useRelease().refresh()
    expect(hasDrafts('r1')).toBe(true)
  })
})
