import { computed, readonly, ref } from 'vue'
import type { CveReport } from '../types/cveReport'
import { countNew } from '../lib/cveReport'
import { useCveSeen } from './useCveSeen'

// Module-level singleton: the header badge and the CVEs panel share one
// report and one stream subscription.
const report = ref<CveReport | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

let es: EventSource | null = null
let debounce: ReturnType<typeof setTimeout> | null = null
let started = false

async function refresh(): Promise<void> {
  loading.value = true
  try {
    const res = await fetch('/api/cve/report')
    if (!res.ok) throw new Error(res.statusText)
    report.value = await res.json() as CveReport
    error.value = null
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'failed to load CVE report'
  } finally {
    loading.value = false
  }
}

// Idempotent: the first call fetches and subscribes to the app's global
// stream; any message schedules a debounced refetch (mirrors useOverviewData).
function start(): void {
  if (started) return
  started = true
  void refresh()
  es = new EventSource('/api/stream')
  es.onmessage = () => {
    if (debounce) clearTimeout(debounce)
    debounce = setTimeout(() => { void refresh() }, 2000)
  }
}

const { isNew } = useCveSeen()
const newCount = computed(() => (report.value ? countNew(report.value.staging, isNew) : 0))

export function useCveReport() {
  return {
    report: readonly(report),
    loading: readonly(loading),
    error: readonly(error),
    refresh,
    start,
    newCount,
  }
}

export function resetCveReportForTests(): void {
  es?.close()
  es = null
  if (debounce) clearTimeout(debounce)
  debounce = null
  started = false
  report.value = null
  loading.value = false
  error.value = null
}
