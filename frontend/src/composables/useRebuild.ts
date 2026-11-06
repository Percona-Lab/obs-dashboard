import { ref, onUnmounted } from 'vue'
import { handleUnauthorized } from './useAuth'

export function useRebuild() {
  const loadingMap = ref(new Map<string, boolean>())
  const errorMap = ref(new Map<string, string>())
  const timerMap = new Map<string, ReturnType<typeof setTimeout>>()

  onUnmounted(() => {
    timerMap.forEach(clearTimeout)
    timerMap.clear()
  })

  function key(repo: string, arch: string): string {
    return `${repo}/${arch}`
  }

  function scheduleErrorClear(k: string) {
    const prev = timerMap.get(k)
    if (prev !== undefined) clearTimeout(prev)
    const t = setTimeout(() => {
      const m = new Map(errorMap.value)
      m.delete(k)
      errorMap.value = m
      timerMap.delete(k)
    }, 4000)
    timerMap.set(k, t)
  }

  async function trigger(project: string, pkg: string, repo: string, arch: string): Promise<void> {
    const k = key(repo, arch)
    loadingMap.value = new Map(loadingMap.value).set(k, true)
    const cleared = new Map(errorMap.value)
    cleared.delete(k)
    errorMap.value = cleared

    try {
      const res = await fetch('/api/rebuild', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ project, repo, arch, package: pkg }),
      })
      if (res.status === 401) {
        handleUnauthorized()
        return
      }
      if (!res.ok) {
        let msg = `HTTP ${res.status}`
        try {
          const text = (await res.text()).trim()
          if (text) msg = text
        } catch {
          // body read failed; keep the HTTP status message
        }
        const m = new Map(errorMap.value)
        m.set(k, msg)
        errorMap.value = m
        scheduleErrorClear(k)
      }
    } catch {
      const m = new Map(errorMap.value)
      m.set(k, 'Network error')
      errorMap.value = m
      scheduleErrorClear(k)
    } finally {
      const m = new Map(loadingMap.value)
      m.set(k, false)
      loadingMap.value = m
    }
  }

  function isLoading(repo: string, arch: string): boolean {
    return loadingMap.value.get(key(repo, arch)) ?? false
  }

  function errorFor(repo: string, arch: string): string | null {
    return errorMap.value.get(key(repo, arch)) ?? null
  }

  return { trigger, isLoading, errorFor }
}
