// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import ServerLogPanel from './ServerLogPanel.vue'
import { resetAuthForTests } from '../composables/useAuth'

class FakeEventSource {
  static last: FakeEventSource
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  listeners: Record<string, (e: MessageEvent) => void> = {}
  close = vi.fn()
  constructor(public url: string) {
    FakeEventSource.last = this
  }
  addEventListener(name: string, fn: (e: MessageEvent) => void): void {
    this.listeners[name] = fn
  }
  emit(name: string, data: unknown): void {
    this.listeners[name]({ data: JSON.stringify(data) } as MessageEvent)
  }
}

beforeEach(() => {
  resetAuthForTests()
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('{}', { status: 401 }))))
  vi.stubGlobal('EventSource', FakeEventSource)
})
afterEach(() => vi.unstubAllGlobals())

const ts = '2026/11/12 14:03:11'
const info = `${ts} INFO hello`
const warn = `${ts} WARN careful`
const err = `${ts} ERROR boom`

describe('ServerLogPanel', () => {
  it('renders lines with level colours', async () => {
    const w = mount(ServerLogPanel)
    FakeEventSource.last.emit('reset', [info, warn, err])
    await nextTick()
    const rows = w.findAll('[data-testid="log-line"]')
    expect(rows).toHaveLength(3)
    expect(rows[1].classes()).toContain('text-warn')
    expect(rows[2].classes()).toContain('text-fail')
  })

  it('errors and warnings only', async () => {
    const w = mount(ServerLogPanel)
    const es = FakeEventSource.last
    es.emit('reset', [info, warn, err])
    await w.find('[data-testid="log-errors-only"]').setValue(true)
    expect(w.findAll('[data-testid="log-line"]')).toHaveLength(2)
    es.emit('line', info)
    await nextTick()
    expect(w.findAll('[data-testid="log-line"]')).toHaveLength(2)
    es.emit('line', err)
    await nextTick()
    expect(w.findAll('[data-testid="log-line"]')).toHaveLength(3)
  })

  it('shows the stream state', async () => {
    const w = mount(ServerLogPanel)
    expect(w.find('[data-testid="log-state"]').text()).toBe('Reconnecting')
    FakeEventSource.last.onopen!()
    await nextTick()
    expect(w.find('[data-testid="log-state"]').text()).toBe('Live')
  })

  it('pause button', async () => {
    const w = mount(ServerLogPanel)
    const pause = w.find('[data-testid="log-pause"]')
    await pause.trigger('click')
    expect(pause.text()).toBe('Resume')
    FakeEventSource.last.emit('line', info)
    await nextTick()
    expect(w.findAll('[data-testid="log-line"]')).toHaveLength(0)
    await pause.trigger('click')
    expect(w.findAll('[data-testid="log-line"]')).toHaveLength(1)
  })

  it('shows HTML as text', async () => {
    const w = mount(ServerLogPanel)
    FakeEventSource.last.emit('reset', ['<script>alert(1)</script>'])
    await nextTick()
    expect(w.find('[data-testid="log-line"]').text()).toBe('<script>alert(1)</script>')
    expect(w.find('script').exists()).toBe(false)
  })

  it('closes the stream on unmount', () => {
    const w = mount(ServerLogPanel)
    w.unmount()
    expect(FakeEventSource.last.close).toHaveBeenCalled()
  })
})
