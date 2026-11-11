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
  cut: null, sync_state: '', sync_url: '', sync_error: '', sync: [], removals: [], qa: null, final: null, started_by: 'me', started_at: null, actions: [], cut_units: [], ...o,
})
const mountW = (o: Partial<ReleaseState> = {}) => mount(ReleaseWizard, { props: { release: rel(o) }, attachTo: document.body })

beforeEach(() => { vi.clearAllMocks(); resetDraftsForTests(); document.body.innerHTML = '' })

describe('ReleaseWizard', () => {
  it('spins on the cut progress line that is in progress, and only on that one', () => {
    const line = (w: ReturnType<typeof mountW>, text: string) => w.findAll('[data-testid="cut-line"]').find((l) => l.text().includes(text))!
    const spin = (w: ReturnType<typeof mountW>, text: string) => line(w, text).find('[data-testid="spinner"]').exists()
    const base = { step: 'cutting' as const, pr: 0, pr_url: '', head_sha: '' }

    const queued = mountW({ ...base, cut: { state: 'queued', url: '' } })
    expect(spin(queued, 'Starting the cut on GitHub')).toBe(true)
    expect(spin(queued, 'Generating release files and changelogs')).toBe(false)
    expect(queued.findAll('[data-testid="spinner"]')).toHaveLength(1)

    const running = mountW({ ...base, cut: { state: 'running', url: '' } })
    expect(spin(running, 'Starting the cut on GitHub')).toBe(false)
    expect(spin(running, 'Generating release files and changelogs')).toBe(true)
    expect(spin(running, 'Opening the release PR')).toBe(false)

    const opening = mountW({ ...base, cut: { state: 'success', url: '' } })
    expect(spin(opening, 'Generating release files and changelogs')).toBe(false)
    expect(spin(opening, 'Opening the release PR')).toBe(true)

    const failed = mountW({ ...base, cut: { state: 'failure', url: '' } })
    expect(failed.find('[data-testid="cut-progress"] [data-testid="spinner"]').exists()).toBe(false)
  })

  it('spins while waiting for the sync to start, for approval and for the final sync', () => {
    expect(mountW({ step: 'sync', sync_state: 'queued', sync: [] }).find('[data-testid="sync-waiting"] [data-testid="spinner"]').exists()).toBe(true)
    expect(mountW({ step: 'sync', sync_state: 'running', sync: [] }).find('[data-testid="sync-waiting"] [data-testid="spinner"]').exists()).toBe(true)
    expect(mountW({ step: 'cutting', pr: 0, pr_url: '', cut: { state: 'approval', url: 'https://run' } }).find('[data-testid="cut-approval"] [data-testid="spinner"]').exists()).toBe(true)

    const noFinal = mountW({ step: 'releasing', final: null })
    expect(noFinal.get('[data-testid="final-waiting"]').text()).toContain('Waiting for the final sync to start')
    expect(noFinal.find('[data-testid="final-waiting"] [data-testid="spinner"]').exists()).toBe(true)

    const fin = mountW({ step: 'releasing', final: { tags: [], instances: [], github_releases: 'running', cleanup: '', state: 'running', url: '' } })
    expect(fin.find('[data-testid="final-github-releases"] [data-testid="spinner"]').exists()).toBe(true)
    expect(fin.get('[data-testid="final-cleanup"]').find('[data-testid="spinner"]').exists()).toBe(false)
    expect(fin.find('[data-testid="final-waiting"]').exists()).toBe(false)
  })

  it('shows the cut progress and a retry on a failed cut', async () => {
    const w = mountW({ step: 'cutting', pr: 0, pr_url: '', head_sha: '', cut: { state: 'failure', url: 'https://run' } })
    expect(w.text()).toContain('Generating release files and changelogs')
    expect(w.text()).toContain('The cut failed')
    expect(w.get('[data-testid="cut-failed"] a').attributes('href')).toBe('https://run')
    await w.get('[data-testid="retry-cut"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'recut', '')
    // nothing was left on GitHub: there is nothing to abandon
    expect(w.find('[data-testid="abandon"]').exists()).toBe(false)
    // a failed cut that left a branch can be abandoned
    expect(mountW({ step: 'cutting', pr: 0, pr_url: '', head_sha: 'abc', cut: { state: 'failure', url: '' } }).find('[data-testid="abandon"]').exists()).toBe(true)
  })

  it('hides retry for a hand-made release', () => {
    const w = mountW({ id: 'pr-7', step: 'cutting', cut: { state: 'failure', url: '' } })
    expect(w.find('[data-testid="retry-cut"]').exists()).toBe(false)
  })

  it('shows abandon disabled, with the reason, while the cut is queued or running', async () => {
    for (const state of ['queued', 'running'] as const) {
      const w = mountW({ step: 'cutting', cut: { state, url: '' } })
      const btn = w.get('[data-testid="abandon"]')
      expect((btn.element as HTMLButtonElement).disabled).toBe(true)
      expect(btn.attributes('title')).toBe('Wait for the cut to finish before abandoning.')
      await btn.trigger('click')
      expect(w.find('[data-testid="release-confirm"]').exists()).toBe(false)
    }
    const idle = mountW({ step: 'changelog' }).get('[data-testid="abandon"]')
    expect((idle.element as HTMLButtonElement).disabled).toBe(false)
    expect(idle.attributes('title')).toBeUndefined()
  })

  it('highlights Changelog for cutting and changelog', () => {
    for (const step of ['cutting', 'changelog'] as const) {
      const w = mountW({ step })
      const s = w.findAll('[data-testid="step"]')
      expect(s[0].attributes('data-state')).toBe('done')
      expect(s[1].attributes('data-state')).toBe('on')
    }
  })

  it('names the units being cut in the strip while there is no release id yet', () => {
    const w = mountW({ id: '20261111T230450', step: 'cutting', pr: 0, pr_url: '', units: [], cut: { state: 'running', url: '' }, cut_units: ['ppg:staging:16:tde'] })
    expect(w.text()).toContain('ppg:staging:16:tde')
    expect(w.text()).not.toContain('20261111T230450')
  })

  it('says why the sync failed when a unit was populated on no instance', () => {
    const w = mountW({ step: 'sync', sync_state: 'failure', sync_error: 'ppg:releases:16:tde was not populated on any instance.',
      sync: [{ instance: 'labs', state: 'success', url: '', started_at: null, finished_at: null, cdn: '', errors: [], units: [{ project: 'ppg:releases:16:tde', outcome: 'skipped' }], nothing_to_sync: true }] })
    expect(w.get('[data-testid="sync-error"]').text()).toContain('ppg:releases:16:tde was not populated on any instance.')
    expect(w.find('[data-testid="retry-sync"]').exists()).toBe(true)
    expect(mountW({ step: 'sync', sync_state: 'running', sync_error: '' }).find('[data-testid="sync-error"]').exists()).toBe(false)
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
    const inst = { instance: 'obs-a', state: 'failure' as const, url: '', started_at: null, finished_at: null, cdn: '', errors: [], units: [], nothing_to_sync: false }
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

  it('a held cut shows waiting for approval with a run link and no Retry', () => {
    const held = { state: 'approval' as const, url: 'https://github.com/o/r/actions/runs/9' }
    const w = mountW({ step: 'cutting', pr: 0, pr_url: '', head_sha: '', units: [], cut: held })
    const box = w.get('[data-testid="cut-approval"]')
    expect(box.text()).toContain('Waiting for approval on GitHub. A person with write access must approve the workflow run before the cut starts.')
    const a = box.get('a')
    expect(a.text()).toBe('Open the run on GitHub')
    expect(a.attributes('href')).toBe('https://github.com/o/r/actions/runs/9')
    expect(a.attributes('rel')).toBe('noopener noreferrer')
    expect(box.classes()).toContain('border-warn')
    expect(w.find('[data-testid="cut-failed"]').exists()).toBe(false)
    expect(w.find('[data-testid="retry-cut"]').exists()).toBe(false)
    expect(w.find('[data-testid="recut"]').exists()).toBe(false)
    expect(w.text()).not.toContain('The cut failed')
    // no PR: nothing to abandon
    expect(w.find('[data-testid="abandon"]').exists()).toBe(false)
    // a held re-cut of a release that has a PR can be abandoned
    expect(mountW({ step: 'cutting', cut: held }).find('[data-testid="abandon"]').exists()).toBe(true)
    expect(mountW({ step: 'cutting', cut: { state: 'approval', url: 'javascript:alert(1)' } }).find('[data-testid="cut-approval"] a').exists()).toBe(false)
  })
})
