// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import AppHeader from './AppHeader.vue'
import { useAuth, resetAuthForTests } from '../composables/useAuth'

function mountHeader() {
  return mount(AppHeader, { props: { theme: 'light', mainTab: 'overview' } })
}

beforeEach(() => resetAuthForTests())

describe('AppHeader auth controls', () => {
  it('offers Sign in when signed out', () => {
    useAuth().authEnabled.value = true
    const w = mountHeader()
    expect(w.find('[data-testid="sign-in"]').exists()).toBe(true)
    expect(w.find('[data-testid="settings-tab"]').exists()).toBe(false)
    expect(w.find('[data-testid="sign-out"]').exists()).toBe(false)
  })

  it('shows the user, Settings and Sign out when signed in', async () => {
    const { authEnabled, user } = useAuth()
    authEnabled.value = true
    user.value = 'alice@percona.com'
    const w = mountHeader()
    expect(w.find('[data-testid="user-email"]').text()).toBe('alice@percona.com')
    expect(w.find('[data-testid="sign-out"]').exists()).toBe(true)
    await w.find('[data-testid="settings-tab"]').trigger('click')
    expect(w.emitted('update:main-tab')).toEqual([['settings']])
  })

  it('shows no auth controls when sign-in is disabled', () => {
    const w = mountHeader()
    expect(w.find('[data-testid="sign-in"]').exists()).toBe(false)
    expect(w.find('[data-testid="sign-out"]').exists()).toBe(false)
  })
})
