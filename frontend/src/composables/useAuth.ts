import { computed, ref, watch, type Ref } from 'vue'
import type { MainTab } from './useUrlState'

// Module-level singleton: one session per page.
const user = ref<string | null>(null)
const authEnabled = ref(false)
const loaded = ref(false)
const notice = ref<string | null>(null)
const signedIn = computed(() => user.value !== null)
let noticeTimer: ReturnType<typeof setTimeout> | undefined

export function showNotice(text: string): void {
  notice.value = text
  if (noticeTimer !== undefined) clearTimeout(noticeTimer)
  noticeTimer = setTimeout(() => {
    notice.value = null
    noticeTimer = undefined
  }, 6000)
}

export function dismissNotice(): void {
  if (noticeTimer !== undefined) clearTimeout(noticeTimer)
  noticeTimer = undefined
  notice.value = null
}

export async function loadAuth(): Promise<void> {
  try {
    const res = await fetch('/api/me')
    if (res.ok) {
      const body = await res.json() as { email: string | null; auth_enabled: boolean }
      user.value = body.email
      authEnabled.value = body.auth_enabled
    }
  } catch {
    // Backend unreachable: stay signed out.
  } finally {
    loaded.value = true
  }
}

// consumeAuthError reports a failed sign-in redirect (?auth_error=...) once
// and removes its parameters from the address bar.
export function consumeAuthError(): void {
  const url = new URL(window.location.href)
  const code = url.searchParams.get('auth_error')
  if (!code) return
  const email = url.searchParams.get('email')
  if (code === 'not_allowed') {
    showNotice(email ? `${email} is not allowed to sign in` : 'This account is not allowed to sign in')
  } else {
    showNotice('Sign-in failed')
  }
  url.searchParams.delete('auth_error')
  url.searchParams.delete('email')
  window.history.replaceState(window.history.state, '', url.pathname + url.search + url.hash)
}

export function login(): void {
  window.location.assign('/auth/login')
}

export async function logout(): Promise<void> {
  try {
    await fetch('/auth/logout', { method: 'POST' })
  } catch {
    // The session cookie may survive; the UI still signs out.
  }
  user.value = null
}

// handleUnauthorized is called when an action gets a 401: the session ended.
export function handleUnauthorized(): void {
  if (user.value !== null) showNotice('Session expired, sign in again')
  user.value = null
}

// guardSettingsTab keeps signed-out visitors off the Settings and Release tabs.
export function guardSettingsTab(mainTab: Ref<MainTab>): void {
  watch([mainTab, signedIn, loaded], () => {
    if (loaded.value && !signedIn.value && (mainTab.value === 'settings' || mainTab.value === 'release')) mainTab.value = 'overview'
  }, { immediate: true })
}

export function useAuth() {
  return { user, authEnabled, loaded, signedIn, notice }
}

export function resetAuthForTests(): void {
  dismissNotice()
  user.value = null
  authEnabled.value = false
  loaded.value = false
}
