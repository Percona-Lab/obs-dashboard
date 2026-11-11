// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { nextTick, ref } from 'vue'
import {
  useAuth, loadAuth, handleUnauthorized, consumeAuthError, guardSettingsTab, logout, resetAuthForTests,
} from './useAuth'
import type { MainTab } from './useUrlState'

function stubFetch(impl: (url: string, init?: RequestInit) => Promise<Response>) {
  const fn = vi.fn(impl)
  vi.stubGlobal('fetch', fn)
  return fn
}
const json = (body: unknown, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

beforeEach(() => resetAuthForTests())
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

describe('useAuth', () => {
  it('loads the signed-in user', async () => {
    stubFetch(() => json({ email: 'alice@percona.com', auth_enabled: true }))
    await loadAuth()
    const { user, authEnabled, signedIn, loaded } = useAuth()
    expect(user.value).toBe('alice@percona.com')
    expect(authEnabled.value).toBe(true)
    expect(signedIn.value).toBe(true)
    expect(loaded.value).toBe(true)
  })

  it('stays signed out but loaded when /api/me fails', async () => {
    stubFetch(() => Promise.reject(new Error('down')))
    await loadAuth()
    const { signedIn, loaded } = useAuth()
    expect(signedIn.value).toBe(false)
    expect(loaded.value).toBe(true)
  })

  it('handleUnauthorized clears the user and explains why', () => {
    const { user, notice } = useAuth()
    user.value = 'alice@percona.com'
    handleUnauthorized()
    expect(user.value).toBeNull()
    expect(notice.value).toBe('Session expired, sign in again')
  })

  it('handleUnauthorized is silent when nobody was signed in', () => {
    handleUnauthorized()
    expect(useAuth().notice.value).toBeNull()
  })

  it('logout posts and clears the user', async () => {
    const fetchFn = stubFetch(() => Promise.resolve(new Response(null, { status: 204 })))
    const { user } = useAuth()
    user.value = 'alice@percona.com'
    await logout()
    expect(fetchFn).toHaveBeenCalledWith('/auth/logout', { method: 'POST' })
    expect(user.value).toBeNull()
  })

  it('consumeAuthError shows the refused email and cleans the URL', () => {
    window.history.replaceState(null, '', '/?tab=board&auth_error=not_allowed&email=eve%40percona.com')
    consumeAuthError()
    expect(useAuth().notice.value).toBe('eve@percona.com is not allowed to sign in')
    expect(window.location.search).toBe('?tab=board')
  })

  it('consumeAuthError reports a generic failure', () => {
    window.history.replaceState(null, '', '/?auth_error=failed')
    consumeAuthError()
    expect(useAuth().notice.value).toBe('Sign-in failed')
    expect(window.location.search).toBe('')
  })

  it('notice clears itself after 6 seconds', () => {
    vi.useFakeTimers()
    const { user, notice } = useAuth()
    user.value = 'a@percona.com'
    handleUnauthorized()
    vi.advanceTimersByTime(6000)
    expect(notice.value).toBeNull()
  })

  it('guardSettingsTab sends signed-out visitors to overview once loaded', async () => {
    const tab = ref<MainTab>('settings')
    guardSettingsTab(tab)
    expect(tab.value).toBe('settings') // not loaded yet
    stubFetch(() => json({ email: null, auth_enabled: true }))
    await loadAuth()
    await nextTick()
    expect(tab.value).toBe('overview')
  })

  it('guardSettingsTab keeps settings for a signed-in user', async () => {
    const tab = ref<MainTab>('settings')
    guardSettingsTab(tab)
    stubFetch(() => json({ email: 'alice@percona.com', auth_enabled: true }))
    await loadAuth()
    await nextTick()
    expect(tab.value).toBe('settings')
  })

  it('guardSettingsTab sends signed-out visitors off the release tab', async () => {
    const tab = ref<MainTab>('release')
    guardSettingsTab(tab)
    stubFetch(() => json({ email: null, auth_enabled: true }))
    await loadAuth()
    await nextTick()
    expect(tab.value).toBe('overview')
  })
})
