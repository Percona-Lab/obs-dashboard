// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import ReleaseWizard from './ReleaseWizard.vue'
import type { ReleaseState } from '../../types/release'

const act = vi.fn(() => Promise.resolve(true))
vi.mock('../../composables/useRelease', () => ({
  useRelease: () => ({ saveChangelog: vi.fn(), resetChangelog: vi.fn(), act, busy: ref(false) }),
}))

const rel = (o: Partial<ReleaseState> = {}): ReleaseState => ({
  id: 'r1', branch: 'b', pr: 5, pr_url: 'https://github.com/o/r/pull/5', head_sha: 'abc', step: 'sync', dashboard: true,
  units: [{ unit: 'u', release_project: '', release_dir: '', release_id: '18.6-6', tag: '', changelog: 'text', generated: null }],
  cut: null, sync_state: '', sync: [], removals: [], qa: null, final: null, started_by: 'me', started_at: null, actions: [], ...o,
})
const mountW = (o: Partial<ReleaseState> = {}) => mount(ReleaseWizard, { props: { release: rel(o) }, attachTo: document.body })

beforeEach(() => { vi.clearAllMocks(); document.body.innerHTML = '' })

describe('ReleaseWizard', () => {
  it('shows the cut progress and a retry on a failed cut', async () => {
    const w = mountW({ step: 'cutting', pr: 0, pr_url: '', head_sha: '', cut: { state: 'failure', url: 'https://run' } })
    expect(w.text()).toContain('Release branch created')
    expect(w.text()).toContain('The cut failed')
    expect(w.get('[data-testid="cut-failed"] a').attributes('href')).toBe('https://run')
    await w.get('[data-testid="retry-cut"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'recut', '')
    expect(w.find('[data-testid="abandon"]').exists()).toBe(true)
  })

  it('hides retry for a hand-made release and abandon while the cut runs', () => {
    const w = mountW({ id: 'pr-7', step: 'cutting', cut: { state: 'failure', url: '' } })
    expect(w.find('[data-testid="retry-cut"]').exists()).toBe(false)
    const r = mountW({ step: 'cutting', cut: { state: 'running', url: '' } })
    expect(r.find('[data-testid="abandon"]').exists()).toBe(false)
  })

  it('highlights Changelog for cutting and changelog', () => {
    for (const step of ['cutting', 'changelog'] as const) {
      const w = mountW({ step })
      const s = w.findAll('[data-testid="step"]')
      expect(s[0].attributes('data-state')).toBe('done')
      expect(s[1].attributes('data-state')).toBe('on')
    }
  })

  it('hides abandon while releasing', () => {
    expect(mountW({ step: 'releasing' }).find('[data-testid="abandon"]').exists()).toBe(false)
    expect(mountW({ step: 'qa' }).find('[data-testid="abandon"]').exists()).toBe(true)
  })

  it('hides every action for a fork PR', () => {
    const w = mountW({ dashboard: false, step: 'changelog' })
    expect(w.findAll('button').map((b) => b.text()).filter((t) => t !== '18.6-6')).toEqual(['← Back'])
    expect(w.find('[data-testid="fork-note"]').exists()).toBe(true)
  })

  it('abandon needs confirmation', async () => {
    const w = mountW()
    await w.get('[data-testid="abandon"]').trigger('click')
    expect(act).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(w.get('[data-testid="confirm-cancel"]').element)
    await w.get('[data-testid="confirm-ok"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'abandon', 'abc')
  })

  it('escape cancels the dialog', async () => {
    const w = mountW()
    await w.get('[data-testid="abandon"]').trigger('click')
    await w.get('[data-testid="release-confirm"]').trigger('keydown', { key: 'Escape' })
    expect(w.find('[data-testid="release-confirm"]').exists()).toBe(false)
    expect(act).not.toHaveBeenCalled()
  })

  it('re-cut in sync/qa only for non pr- ids, with confirmation', async () => {
    expect(mountW({ id: 'pr-1' }).find('[data-testid="recut"]').exists()).toBe(false)
    expect(mountW({ step: 'changelog' }).find('[data-testid="recut"]').exists()).toBe(false)
    const w = mountW({ step: 'qa' })
    await w.get('[data-testid="recut"]').trigger('click')
    expect(w.get('[data-testid="release-confirm"]').text()).toContain('changelog edits will be regenerated')
    await w.get('[data-testid="confirm-ok"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'recut', 'abc')
  })

  it('shows the changelog read-only in a collapsed section after the changelog step', () => {
    const w = mountW({ step: 'sync' })
    expect(w.find('textarea').exists()).toBe(false)
    const d = w.get('[data-testid="changelog-readonly"]')
    expect((d.element as HTMLDetailsElement).open).toBe(false)
    expect(d.text()).toContain('text')
  })

  it('offers Retry final sync when the final sync failed', async () => {
    const inst = { instance: 'obs-a', state: 'failure' as const, url: '', started_at: null, finished_at: null, cdn: '', failed_units: [], errors: [] }
    const w = mountW({ step: 'releasing', final: { tags: ['t1'], instances: [inst], github_releases: 'skipped', cleanup: 'queued', state: 'failure', url: '' } })
    expect(w.text()).toContain('PR #5 merged')
    expect(w.text()).toContain('Publish the GitHub releases')
    await w.get('[data-testid="retry-final"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'retry-release', 'abc')
    expect(mountW({ step: 'releasing', final: { tags: [], instances: [], github_releases: '', cleanup: '', state: 'running', url: '' } }).find('[data-testid="retry-final"]').exists()).toBe(false)
  })

  it('shows waiting text and Retry sync in the sync step', async () => {
    expect(mountW({ sync_state: 'queued' }).text()).toContain('Waiting for the sync to start')
    const w = mountW({ sync_state: 'failure' })
    await w.get('[data-testid="retry-sync"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'retry-sync', 'abc')
    expect(mountW({ sync_state: 'success' }).text()).toContain('Nothing will be removed')
  })
})
