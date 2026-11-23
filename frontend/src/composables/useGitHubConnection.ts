import { ref } from 'vue'
import { handleUnauthorized } from './useAuth'
import { errorText } from './http'
import type { GitHubConnection } from '../types/release'

export function useGitHubConnection() {
  const status = ref<GitHubConnection | null>(null)
  const loadError = ref<string | null>(null)
  const loading = ref(false)

  async function load(): Promise<void> {
    loadError.value = null
    loading.value = true
    try {
      const res = await fetch('/api/release/github')
      if (res.status === 401) { handleUnauthorized(); return }
      if (!res.ok) { loadError.value = await errorText(res); return }
      status.value = await res.json() as GitHubConnection
    } catch {
      loadError.value = 'Network error'
    } finally {
      loading.value = false
    }
  }

  return { status, loadError, loading, load }
}
