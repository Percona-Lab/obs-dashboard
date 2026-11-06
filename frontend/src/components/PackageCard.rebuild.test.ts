// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import PackageCard from './PackageCard.vue'
import { useRebuild } from '../composables/useRebuild'
import { useAuth, resetAuthForTests } from '../composables/useAuth'
import type { Package } from '../types/api'

const failing: Package = {
  project: 'ppg:staging:18',
  name: 'percona-pg_tde',
  rollup_state: 'failed',
  ok_targets: 0,
  total_targets: 1,
  targets: [{ repo: 'Debian_13', arch: 'x86_64', state: 'failed' }],
  updated_at: '2026-11-06T00:00:00Z',
} as Package

beforeEach(() => {
  resetAuthForTests()
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))))
})
afterEach(() => vi.unstubAllGlobals())

describe('Rebuild button', () => {
  it('is hidden when signed out', () => {
    const w = mount(PackageCard, { props: { pkg: failing } })
    expect(w.find('[aria-label="Retrigger build"]').exists()).toBe(false)
  })

  it('is shown when signed in', () => {
    useAuth().user.value = 'alice@percona.com'
    const w = mount(PackageCard, { props: { pkg: failing } })
    expect(w.find('[aria-label="Retrigger build"]').exists()).toBe(true)
  })
})

describe('useRebuild on 401', () => {
  it('signs the user out instead of recording an error', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('unauthorized', { status: 401 }))))
    const { user, notice } = useAuth()
    user.value = 'alice@percona.com'
    const { trigger, errorFor, isLoading } = useRebuild()
    await trigger('ppg:staging:18', 'percona-pg_tde', 'Debian_13', 'x86_64')
    await flushPromises()
    expect(user.value).toBeNull()
    expect(notice.value).toBe('Session expired, sign in again')
    expect(errorFor('Debian_13', 'x86_64')).toBeNull()
    expect(isLoading('Debian_13', 'x86_64')).toBe(false)
  })
})
