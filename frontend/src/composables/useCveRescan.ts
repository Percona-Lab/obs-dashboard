import { onUnmounted, ref } from 'vue'
import { handleUnauthorized } from './useAuth'
import { errorText } from './http'

const RESCAN_COOLDOWN_MS = 10_000

// POST /api/cve/rescan with a busy/cooldown state and a status message.
// Results arrive through the scan events on the global stream, which already
// trigger refetches, so callers need no result handling.
export function useCveRescan() {
  const rescanBusy = ref(false)
  const rescanMsg = ref<string | null>(null)
  const rescanFailed = ref(false)
  let rescanTimer: ReturnType<typeof setTimeout> | null = null
  onUnmounted(() => { if (rescanTimer) clearTimeout(rescanTimer) })

  async function rescan() {
    if (rescanBusy.value) return
    rescanBusy.value = true
    rescanFailed.value = false
    rescanMsg.value = null
    let cooldown = false
    try {
      const res = await fetch('/api/cve/rescan', { method: 'POST' })
      if (res.status === 401) {
        handleUnauthorized()
        return
      }
      if (!res.ok) throw new Error(await errorText(res))
      const { queued } = await res.json() as { queued: number }
      rescanMsg.value = queued > 0
        ? `Queued ${queued} image${queued === 1 ? '' : 's'}`
        : 'Nothing queued: rescans already pending'
      cooldown = true
    } catch (e) {
      rescanFailed.value = true
      rescanMsg.value = `Rescan failed: ${e instanceof Error ? e.message : 'network error'}`
    } finally {
      if (cooldown) {
        rescanTimer = setTimeout(() => { rescanBusy.value = false; rescanMsg.value = null }, RESCAN_COOLDOWN_MS)
      } else {
        rescanBusy.value = false
      }
    }
  }

  return { rescanBusy, rescanMsg, rescanFailed, rescan }
}
