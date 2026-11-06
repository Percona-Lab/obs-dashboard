// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import SettingsPanel from './SettingsPanel.vue'
import { useAuth, resetAuthForTests } from '../composables/useAuth'
import { resetUpgradesForTests } from '../composables/useUpgrades'

const listBody = {
  admins: ['root@percona.com'],
  users: [
    { email: 'alice@percona.com', added_by: 'root@percona.com', added_at: '2026-11-06T12:00:00.000000000Z' },
    { email: 'bob@percona.com', added_by: 'alice@percona.com', added_at: '2026-11-07T09:30:00.000000000Z' },
  ],
}

type Handler = (url: string, init?: RequestInit) => Response
let handler: Handler
let fetchFn: ReturnType<typeof vi.fn>

beforeEach(() => {
  resetAuthForTests()
  resetUpgradesForTests()
  useAuth().user.value = 'alice@percona.com'
  handler = (url: string) => (url.startsWith('/api/upgrades')
    ? new Response(JSON.stringify({ current: 'v0.1.0', host_files_version: '', repo: 'Percona-Lab/obs-dashboard', apply_enabled: false, in_flight: '', checked_at: null, releases: [] }), { status: 200 })
    : new Response(JSON.stringify(listBody), { status: 200 }))
  fetchFn = vi.fn((url: string, init?: RequestInit) => Promise.resolve(handler(url, init)))
  vi.stubGlobal('fetch', fetchFn)
})
afterEach(() => vi.unstubAllGlobals())

async function mounted() {
  const w = mount(SettingsPanel)
  await flushPromises()
  return w
}

describe('SettingsPanel Access zone', () => {
  it('lists admins and users', async () => {
    const w = await mounted()
    const admins = w.findAll('[data-testid="admin-row"]')
    expect(admins).toHaveLength(1)
    expect(admins[0].text()).toContain('root@percona.com')
    expect(admins[0].text()).toContain('config')
    expect(admins[0].find('[data-testid="remove"]').exists()).toBe(false)
    const users = w.findAll('[data-testid="user-row"]')
    expect(users).toHaveLength(2)
    expect(users[1].text()).toContain('added by alice@percona.com · 2026-11-07')
  })

  it('disables Remove on your own row', async () => {
    const w = await mounted()
    const [own, other] = w.findAll('[data-testid="user-row"]')
    expect(own.find('[data-testid="remove"]').attributes('disabled')).toBeDefined()
    expect(other.find('[data-testid="remove"]').attributes('disabled')).toBeUndefined()
  })

  it('adds a user and clears the input', async () => {
    const w = await mounted()
    handler = (_url, init) => init?.method === 'POST'
      ? new Response('{}', { status: 201 })
      : new Response(JSON.stringify(listBody), { status: 200 })
    await w.find('[data-testid="add-email"]').setValue('  Carol@Percona.com ')
    await w.find('form').trigger('submit')
    await flushPromises()
    const post = fetchFn.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === 'POST')!
    expect(post[0]).toBe('/api/settings/users')
    expect(JSON.parse((post[1] as RequestInit).body as string)).toEqual({ email: 'carol@percona.com' })
    expect((w.find('[data-testid="add-email"]').element as HTMLInputElement).value).toBe('')
    expect(w.find('[data-testid="add-error"]').exists()).toBe(false)
  })

  it('shows the server error when adding fails', async () => {
    const w = await mounted()
    handler = (_url, init) => init?.method === 'POST'
      ? new Response('email must be a @percona.com address\n', { status: 400 })
      : new Response(JSON.stringify(listBody), { status: 200 })
    await w.find('[data-testid="add-email"]').setValue('eve@gmail.com')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.find('[data-testid="add-error"]').text()).toBe('email must be a @percona.com address')
  })

  it('removes after confirmation only', async () => {
    const w = await mounted()
    handler = (_url, init) => init?.method === 'DELETE'
      ? new Response(null, { status: 204 })
      : new Response(JSON.stringify(listBody), { status: 200 })
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    const bobRemove = w.findAll('[data-testid="user-row"]')[1].find('[data-testid="remove"]')
    await bobRemove.trigger('click')
    await flushPromises()
    expect(fetchFn.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === 'DELETE')).toBe(false)
    await bobRemove.trigger('click')
    await flushPromises()
    const del = fetchFn.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === 'DELETE')!
    expect(del[0]).toBe('/api/settings/users/bob%40percona.com')
    confirmSpy.mockRestore()
  })

  it('signs out on 401', async () => {
    handler = () => new Response('unauthorized', { status: 401 })
    await mounted()
    expect(useAuth().user.value).toBeNull()
  })
})
