// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { hostUpdateCommands, refreshUpgrades, useUpgrades, resetUpgradesForTests } from './useUpgrades'
import { resetAuthForTests } from './useAuth'

beforeEach(() => { resetAuthForTests(); resetUpgradesForTests() })
afterEach(() => vi.unstubAllGlobals())

describe('hostUpdateCommands', () => {
  it('builds tool-agnostic manual steps for the release', () => {
    const c = hostUpdateCommands('Percona-Lab/obs-dashboard', 'v0.3.0')
    expect(c).toContain('https://github.com/Percona-Lab/obs-dashboard/releases/download/v0.3.0/obs-dashboard-host-v0.3.0.tar.gz')
    expect(c).toContain('sha256sum -c --ignore-missing SHA256SUMS')
    expect(c).toContain('OBS_DASHBOARD_HOST_FILES_VERSION=v0.3.0')
    expect(c).toContain('systemctl restart obs-dashboard')
    expect(c.startsWith('cd "$(mktemp -d)"')).toBe(true)
  })
})

describe('refreshUpgrades', () => {
  it('treats an HTML 502 from the proxy as unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('<html>Bad Gateway</html>', { status: 502 }))))
    expect(await refreshUpgrades()).toBe('unreachable')
  })
  it('treats a network failure as unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('failed'))))
    expect(await refreshUpgrades()).toBe('unreachable')
  })
  it('rejects a body that is not an upgrades view', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('{"admins":[]}', { status: 200 }))))
    expect(await refreshUpgrades()).toBe('error')
    expect(useUpgrades().view.value).toBeNull()
  })
})
