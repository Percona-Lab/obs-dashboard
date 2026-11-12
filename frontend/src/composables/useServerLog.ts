import { ref, watch, type Ref } from 'vue'
import { loadAuth } from './useAuth'

export type LogLevel = 'error' | 'warn' | 'plain'
export interface LogLine { id: number; text: string; level: LogLevel }
export const MAX_LINES = 5000

const LEVEL_RE = /^\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2} (ERROR|WARN)/

export function levelOf(text: string): LogLevel {
  const m = LEVEL_RE.exec(text)
  if (!m) return 'plain'
  return m[1] === 'ERROR' ? 'error' : 'warn'
}

export function useServerLog(): {
  lines: Ref<LogLine[]>
  live: Ref<boolean>
  paused: Ref<boolean>
  start: () => void
  stop: () => void
} {
  const lines = ref<LogLine[]>([])
  const live = ref(false)
  const paused = ref(false)
  let held: LogLine[] = []
  let nextId = 1
  let es: EventSource | null = null
  let retry: ReturnType<typeof setTimeout> | null = null

  function make(text: string): LogLine {
    return { id: nextId++, text, level: levelOf(text) }
  }

  function append(batch: LogLine[]): void {
    const merged = lines.value.concat(batch)
    lines.value = merged.length > MAX_LINES ? merged.slice(merged.length - MAX_LINES) : merged
  }

  function hold(line: LogLine): void {
    held.push(line)
    if (held.length > MAX_LINES) held = held.slice(held.length - MAX_LINES)
  }

  function release(): void {
    if (held.length === 0) return
    const batch = held
    held = []
    append(batch)
  }

  watch(paused, (now) => { if (!now) release() }, { flush: 'sync' })

  function start(): void {
    if (es) return
    es = new EventSource('/api/logs/stream')
    es.onopen = (): void => { live.value = true }
    es.onerror = (): void => {
      live.value = false
      void loadAuth()
      // EventSource retries network errors itself. After an HTTP error (a
      // proxy 502 while the backend restarts, or a 401) it gives up, so
      // start over ourselves.
      if (es && es.readyState === EventSource.CLOSED) {
        es.close()
        es = null
        if (retry) clearTimeout(retry)
        retry = setTimeout(() => { retry = null; start() }, 3000)
      }
    }
    es.addEventListener('reset', (e: Event) => {
      const data = JSON.parse((e as MessageEvent).data as string) as string[]
      held = []
      const fresh = data.map(make)
      lines.value = fresh.length > MAX_LINES ? fresh.slice(fresh.length - MAX_LINES) : fresh
    })
    es.addEventListener('line', (e: Event) => {
      const line = make(JSON.parse((e as MessageEvent).data as string) as string)
      if (paused.value) {
        hold(line)
      } else {
        append([line])
      }
    })
  }

  function stop(): void {
    if (retry) clearTimeout(retry)
    retry = null
    es?.close()
    es = null
    live.value = false
  }

  return { lines, live, paused, start, stop }
}
