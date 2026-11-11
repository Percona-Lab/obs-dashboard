// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import ReleaseWizard from './ReleaseWizard.vue'
import { resetDraftsForTests } from '../../composables/useChangelogDrafts'
import type { ReleaseState } from '../../types/release'

const act = vi.fn(() => Promise.resolve(true))
vi.mock('../../composables/useRelease', () => ({
  useRelease: () => ({ saveChangelog: vi.fn(), resetChangelog: vi.fn(), act, busy: ref(false) }),
}))

const rel = (o: Partial<ReleaseState> = {}): ReleaseState => ({
  id: 'r1', branch: 'b', pr: 5, pr_url: 'https://github.com/o/r/pull/5', head_sha: 'abc', step: 'sync', dashboard: true,
  units: [{ unit: 'u', release_project: '', release_dir: '', release_id: '18.6-6', tag: '', changelog: 'text', generated: null }],
  cut: null, sync_state: '', sync_url: '', sync: [], removals: [], qa: null, final: null, started_by: 'me', started_at: null, actions: [], ...o,
})
const mountW = (o: Partial<ReleaseState> = {}) => mount(ReleaseWizard, { props: { release: rel(o) }, attachTo: document.body })

beforeEach(() => { vi.clearAllMocks(); resetDraftsForTests(); document.body.innerHTML = '' })

describe('ReleaseWizard', () => {
  it('shows the cut progress and a retry on a failed cut', async () => {
    const w = mountW({ step: 'cutting', pr: 0, pr_url: '', head_sha: '', cut: { state: 'failure', url: 'https://run' } })
    expect(w.text()).toContain('Release branch created')
    expect(w.text()).toContain('The cut failed')
    expect(w.get('[data-testid="cut-failed"] a').attributes('href')).toBe('https://run')
    await w.get('[data-testid="retry-cut"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'recut', '')
    // nothing was left on GitHub: there is nothing to abandon
    expect(w.find('[data-testid="abandon"]').exists()).toBe(false)
    // a failed cut that left a branch can be abandoned
    expect(mountW({ step: 'cutting', pr: 0, pr_url: '', head_sha: 'abc', cut: { state: 'failure', url: '' } }).find('[data-testid="abandon"]').exists()).toBe(true)
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

  it('abandon uses the head shown when the dialog opens', async () => {
    const w = mountW()
    await w.get('[data-testid="abandon"]').trigger('click')
    await w.setProps({ release: rel({ head_sha: 'moved' }) })
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

  it('a failed sync with no cards says it failed, not waiting', () => {
    const w = mountW({ sync_state: 'failure' })
    expect(w.text()).not.toContain('Waiting for the sync to start')
    expect(w.text()).toContain('The pre-release sync failed before any instance reported')
    expect(w.find('[data-testid="retry-sync"]').exists()).toBe(true)
    expect(mountW({ sync_state: 'running' }).text()).toContain('The sync is starting')
  })

  it('the final view says so when the final sync failed', () => {
    const w = mountW({ step: 'releasing', final: { tags: [], instances: [], github_releases: '', cleanup: '', state: 'failure', url: '' } })
    expect(w.text()).toContain('The final sync failed.')
  })

  it('a failed re-cut shows the failed run with Retry above a locked editor', async () => {
    const w = mountW({ step: 'changelog', cut: { state: 'failure', url: 'https://run/9' } })
    const box = w.get('[data-testid="cut-failed"]')
    expect(box.text()).toContain('The cut failed')
    expect(box.get('a').attributes('href')).toBe('https://run/9')
    expect(w.get('textarea').attributes('readonly')).toBeDefined()
    expect(w.find('[data-testid="approve-changelog"]').exists()).toBe(false)
    expect(w.find('[data-testid="save-changelog"]').exists()).toBe(false)
    const html = w.html()
    expect(html.indexOf('data-testid="cut-failed"')).toBeLessThan(html.indexOf('data-testid="changelog-step"'))
    await w.get('[data-testid="retry-cut"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'recut', 'abc')
    // a normal changelog step has no such box
    expect(mountW({ step: 'changelog', cut: { state: 'success', url: '' } }).find('[data-testid="cut-failed"]').exists()).toBe(false)
    // a hand-made release cannot be re-cut
    expect(mountW({ id: 'pr-7', step: 'changelog', cut: { state: 'failure', url: '' } }).find('[data-testid="retry-cut"]').exists()).toBe(false)
  })

  it('says the unsaved text is kept when the release leaves the changelog step', async () => {
    const w = mountW({ step: 'changelog' })
    expect(w.find('[data-testid="drafts-kept"]').exists()).toBe(false)
    await w.get('textarea').setValue('mine')
    await w.setProps({ release: rel({ step: 'cutting', cut: { state: 'running', url: '' } }) })
    expect(w.get('[data-testid="drafts-kept"]').text()).toBe('This release moved to another step. Your unsaved changelog text is kept and shown if the release returns to the changelog step.')
    await w.setProps({ release: rel({ step: 'changelog', head_sha: 'def' }) })
    expect(w.find('[data-testid="drafts-kept"]').exists()).toBe(false)
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('mine')
  })

  it('no such notice without unsaved text', async () => {
    const w = mountW({ step: 'changelog' })
    await w.setProps({ release: rel({ step: 'sync' }) })
    expect(w.find('[data-testid="drafts-kept"]').exists()).toBe(false)
  })

  it('lists who did what, oldest first', () => {
    expect(mountW().find('[data-testid="release-activity"]').exists()).toBe(false)
    const w = mountW({ actions: [
      { at: '2026-11-11T14:32:10Z', email: 'a@x.io', action: 'cut' },
      { at: '2026-11-11T14:40:00Z', email: 'b@x.io', action: 'save_changelog' },
      { at: '2026-11-11T14:41:00Z', email: 'b@x.io', action: 'approve_changelog' },
      { at: '2026-11-11T14:50:00Z', email: 'c@x.io', action: 'retry_sync' },
      { at: '2026-11-11T15:00:00Z', email: 'c@x.io', action: 'approve_qa' },
      { at: '2026-11-11T15:10:00Z', email: 'c@x.io', action: 'retry_qa' },
      { at: '2026-11-11T15:20:00Z', email: 'c@x.io', action: 'recut' },
      { at: '2026-11-11T15:30:00Z', email: 'd@x.io', action: 'release' },
      { at: '2026-11-11T15:40:00Z', email: 'd@x.io', action: 'retry_release' },
      { at: '2026-11-11T15:50:00Z', email: 'e@x.io', action: 'abandon' },
    ] })
    const items = w.findAll('[data-testid="release-activity"] li').map((li) => li.text())
    expect(items).toHaveLength(10)
    const words = ['a@x.io started the release', 'b@x.io edited the changelog', 'b@x.io approved the changelog', 'c@x.io retried the sync',
      'c@x.io approved the QA run', 'c@x.io retried QA', 'c@x.io updated to latest main', 'd@x.io released', 'd@x.io retried the final sync', 'e@x.io abandoned']
    words.forEach((t, i) => expect(items[i]).toContain(t))
    expect(items[0]).toContain(new Date('2026-11-11T14:32:10Z').toLocaleString())
  })

  it('a sync that failed before any instance links to the run', () => {
    const w = mountW({ sync_state: 'failure', sync_url: 'https://github.com/o/r/actions/runs/7' })
    const a = w.get('[data-testid="sync-failed"] a')
    expect(a.text()).toBe('View the run on GitHub')
    expect(a.attributes('href')).toBe('https://github.com/o/r/actions/runs/7')
    expect(a.attributes('rel')).toBe('noopener noreferrer')
    expect(mountW({ sync_state: 'failure', sync_url: 'javascript:alert(1)' }).find('[data-testid="sync-failed"] a').exists()).toBe(false)
  })
})
