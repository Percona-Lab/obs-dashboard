// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import GitHubConnectionCard from './GitHubConnectionCard.vue'
import { resetAuthForTests } from '../composables/useAuth'
import type { GitHubConnection } from '../types/release'

const healthy: GitHubConnection = {
  mode: 'app', repo: 'percona/obs-packaging', ok: true, error: '', failed_step: '',
  app: { id: '123456', slug: 'percona-obs-release', name: 'Percona OBS Release' },
  installation_id: 7654321,
  permissions: [
    { name: 'contents', required: 'write', granted: 'write', ok: true },
    { name: 'checks', required: 'read', granted: 'read', ok: true },
  ],
  token: { minted_at: '2026-11-23T14:02:00Z', expires_at: '2026-11-23T15:02:00Z', last_error: '', last_error_at: null },
  checked_at: '2026-11-23T14:31:07Z',
}

let fetchFn: ReturnType<typeof vi.fn>
function answer(body: unknown) {
  fetchFn = vi.fn(() => Promise.resolve(new Response(JSON.stringify(body), { status: 200 })))
  vi.stubGlobal('fetch', fetchFn)
}
async function mounted() {
  const w = mount(GitHubConnectionCard)
  await flushPromises()
  return w
}
const t = (w: ReturnType<typeof mount>, id: string) => w.find(`[data-testid="${id}"]`)

beforeEach(() => resetAuthForTests())
afterEach(() => vi.unstubAllGlobals())

describe('GitHubConnectionCard', () => {
  it('shows a connected app', async () => {
    answer(healthy)
    const w = await mounted()
    expect(t(w, 'github-state').text()).toBe('Connected')
    const text = w.text()
    expect(text).toContain('Percona OBS Release (percona-obs-release) · app 123456')
    expect(text).toContain('percona/obs-packaging · installation 7654321')
    expect(text).toContain('issued 14:02, expires 15:02')
    expect(text).toContain('14:31:07 UTC')
    const perms = w.findAll('[data-testid="github-perm"]')
    expect(perms).toHaveLength(2)
    expect(perms[0].text()).toBe('contents write ✓')
    for (const id of ['github-perm-warning', 'github-error', 'github-off']) expect(t(w, id).exists()).toBe(false)
  })

  it('explains an app that is not installed', async () => {
    const error = 'github GET /repos/percona/obs-packaging/installation: HTTP 404'
    answer({ ...healthy, ok: false, failed_step: 'installation', installation_id: undefined, permissions: undefined, error })
    const w = await mounted()
    expect(t(w, 'github-state').text()).toBe('Not connected')
    expect(t(w, 'github-reason').text()).toBe('The app is not installed on percona/obs-packaging.')
    expect(t(w, 'github-error').text()).toBe(error)
  })

  it.each([
    ['app', 'github GET /app: HTTP 401', "GitHub rejected the app's key or ID."],
    ['installation', 'github GET /repos/percona/obs-packaging/installation: HTTP 500', "Could not look up the app's installation."],
    ['token', 'github POST /app/installations/7654321/access_tokens: HTTP 403', 'GitHub would not issue a token.'],
  ])('names the failed step %s', async (step, error, copy) => {
    answer({ ...healthy, ok: false, failed_step: step, error })
    const w = await mounted()
    expect(t(w, 'github-reason').text()).toBe(copy)
    expect(t(w, 'github-error').text()).toBe(error)
  })

  it('shows a configuration error once', async () => {
    const error = 'RELEASE_GITHUB_APP_PRIVATE_KEY is not valid base64'
    answer({ ...healthy, ok: false, failed_step: 'config', error })
    const w = await mounted()
    expect(t(w, 'github-reason').text()).toBe(error)
    expect(t(w, 'github-error').exists()).toBe(false)
  })

  it('warns about a missing permission', async () => {
    answer({ ...healthy, permissions: [
      { name: 'checks', required: 'read', granted: 'none', ok: false },
      { name: 'contents', required: 'write', granted: 'write', ok: true },
    ] })
    const w = await mounted()
    const first = w.findAll('[data-testid="github-perm"]')[0]
    expect(first.text()).toBe('checks none')
    expect(first.text()).not.toContain('✓')
    expect(first.classes()).toContain('text-warn')
    expect(t(w, 'github-perm-warning').exists()).toBe(true)
    expect(t(w, 'github-state').text()).toBe('Connected')
  })

  it('shows token mode', async () => {
    answer({ mode: 'token', repo: 'percona/obs-packaging', ok: true, error: '', failed_step: '', checked_at: '2026-11-23T14:31:07Z' })
    const w = await mounted()
    expect(t(w, 'github-token-mode').text()).toBe('Using a personal access token. Set RELEASE_GITHUB_APP_ID and RELEASE_GITHUB_APP_PRIVATE_KEY to switch to the GitHub App.')
    expect(w.text()).toContain('percona/obs-packaging')
    expect(w.findAll('[data-testid="github-perm"]')).toHaveLength(0)
  })

  it('shows off mode', async () => {
    answer({ mode: 'off', repo: '', ok: false, error: '', failed_step: '', checked_at: '2026-11-23T14:31:07Z' })
    const w = await mounted()
    const off = t(w, 'github-off').text()
    expect(off).toContain('Not configured. The Release tab is hidden.')
    expect(off).toContain('RELEASE_GITHUB_APP_ID')
    expect(off).toContain('RELEASE_GITHUB_APP_PRIVATE_KEY')
    expect(t(w, 'github-state').exists()).toBe(false)
  })

  it('re-tests on click', async () => {
    const pending: ((r: Response) => void)[] = []
    fetchFn = vi.fn(() => new Promise<Response>(resolve => { pending.push(resolve) }))
    vi.stubGlobal('fetch', fetchFn)
    const w = mount(GitHubConnectionCard)
    pending[0](new Response(JSON.stringify(healthy), { status: 200 }))
    await flushPromises()
    expect(fetchFn).toHaveBeenCalledTimes(1)
    expect(t(w, 'github-test').attributes('disabled')).toBeUndefined()
    await t(w, 'github-test').trigger('click')
    expect(fetchFn).toHaveBeenCalledTimes(2)
    expect(fetchFn.mock.calls[1][0]).toBe('/api/release/github')
    expect(t(w, 'github-test').attributes('disabled')).toBeDefined()
    pending[1](new Response(JSON.stringify(healthy), { status: 200 }))
    await flushPromises()
    expect(t(w, 'github-test').attributes('disabled')).toBeUndefined()
  })

  it('shows a load error and keeps the button', async () => {
    fetchFn = vi.fn(() => Promise.resolve(new Response('boom', { status: 500 })))
    vi.stubGlobal('fetch', fetchFn)
    const w = await mounted()
    expect(t(w, 'github-load-error').text()).toBe('boom')
    expect(t(w, 'github-test').attributes('disabled')).toBeUndefined()
  })
})
