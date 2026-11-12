// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useServerLog, levelOf, MAX_LINES } from './useServerLog'
import { resetAuthForTests } from './useAuth'

class FakeEventSource {
  static last: FakeEventSource
  url: string
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  listeners: Record<string, (e: MessageEvent) => void> = {}
  close = vi.fn()
  constructor(url: string) {
    this.url = url
    FakeEventSource.last = this
  }
  addEventListener(name: string, fn: (e: MessageEvent) => void): void {
    this.listeners[name] = fn
  }
  emit(name: string, data: unknown): void {
    this.listeners[name]({ data: JSON.stringify(data) } as MessageEvent)
  }
}

let fetchFn: ReturnType<typeof vi.fn>

beforeEach(() => {
  resetAuthForTests()
  fetchFn = vi.fn(() => Promise.resolve(new Response('{}', { status: 401 })))
  vi.stubGlobal('fetch', fetchFn)
  vi.stubGlobal('EventSource', FakeEventSource)
})
afterEach(() => vi.unstubAllGlobals())

const ts = '2026/11/12 14:03:11'

describe('levelOf', () => {
  it('classifies lines', () => {
    expect(levelOf(`${ts} WARN x`)).toBe('warn')
    expect(levelOf(`${ts} ERROR x`)).toBe('error')
    expect(levelOf(`${ts} INFO x`)).toBe('plain')
    expect(levelOf('goroutine 1 [running]:')).toBe('plain')
    expect(levelOf('ERROR at start')).toBe('plain')
  })
})

describe('useServerLog', () => {
  it('opens the log stream', () => {
    useServerLog().start()
    expect(FakeEventSource.last.url).toBe('/api/logs/stream')
  })

  it('reset replaces, line appends', () => {
    const log = useServerLog()
    log.start()
    const es = FakeEventSource.last
    es.emit('reset', ['a', 'b'])
    es.emit('line', 'c')
    expect(log.lines.value.map(l => l.text)).toEqual(['a', 'b', 'c'])
    es.emit('reset', ['z'])
    expect(log.lines.value.map(l => l.text)).toEqual(['z'])
  })

  it('caps at MAX_LINES', () => {
    const log = useServerLog()
    log.start()
    const es = FakeEventSource.last
    es.emit('reset', Array.from({ length: MAX_LINES }, (_, i) => `r${i}`))
    for (let i = 0; i < 10; i++) es.emit('line', `n${i}`)
    expect(log.lines.value).toHaveLength(MAX_LINES)
    expect(log.lines.value[MAX_LINES - 1].text).toBe('n9')
  })

  it('pause holds, resume releases', () => {
    const log = useServerLog()
    log.start()
    const es = FakeEventSource.last
    es.emit('reset', ['a'])
    log.paused.value = true
    es.emit('line', 'b')
    es.emit('line', 'c')
    expect(log.lines.value).toHaveLength(1)
    log.paused.value = false
    expect(log.lines.value.map(l => l.text)).toEqual(['a', 'b', 'c'])
  })

  it('live follows open and error', () => {
    const log = useServerLog()
    log.start()
    const es = FakeEventSource.last
    es.onopen!()
    expect(log.live.value).toBe(true)
    es.onerror!()
    expect(log.live.value).toBe(false)
    expect(fetchFn).toHaveBeenCalledWith('/api/me')
  })

  it('stop closes', () => {
    const log = useServerLog()
    log.start()
    log.stop()
    expect(FakeEventSource.last.close).toHaveBeenCalled()
  })
})
