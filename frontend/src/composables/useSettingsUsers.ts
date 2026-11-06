import { ref } from 'vue'
import { handleUnauthorized } from './useAuth'
import { errorText } from './http'

export interface AllowedUser {
  email: string
  added_by: string
  added_at: string
}

export function useSettingsUsers() {
  const admins = ref<string[]>([])
  const users = ref<AllowedUser[]>([])
  const loadError = ref<string | null>(null)

  async function list(): Promise<void> {
    loadError.value = null
    try {
      const res = await fetch('/api/settings/users')
      if (res.status === 401) { handleUnauthorized(); return }
      if (!res.ok) { loadError.value = await errorText(res); return }
      const body = await res.json() as { admins: string[]; users: AllowedUser[] }
      admins.value = body.admins
      users.value = body.users
    } catch {
      loadError.value = 'Network error'
    }
  }

  // add returns an error message, or null on success.
  async function add(email: string): Promise<string | null> {
    try {
      const res = await fetch('/api/settings/users', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email: email.trim().toLowerCase() }),
      })
      if (res.status === 401) { handleUnauthorized(); return 'Session expired' }
      if (!res.ok) return await errorText(res)
      await list()
      return null
    } catch {
      return 'Network error'
    }
  }

  // remove returns an error message, or null on success.
  async function remove(email: string): Promise<string | null> {
    try {
      const res = await fetch(`/api/settings/users/${encodeURIComponent(email)}`, { method: 'DELETE' })
      if (res.status === 401) { handleUnauthorized(); return 'Session expired' }
      if (!res.ok) return await errorText(res)
      await list()
      return null
    } catch {
      return 'Network error'
    }
  }

  return { admins, users, loadError, list, add, remove }
}
