// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import ReleaseChangelogStep from './ReleaseChangelogStep.vue'
import type { ReleaseState } from '../../types/release'

const saveChangelog = vi.fn(() => Promise.resolve(true))
const resetChangelog = vi.fn(() => Promise.resolve(true))
const act = vi.fn(() => Promise.resolve(true))
vi.mock('../../composables/useRelease', () => ({
  useRelease: () => ({ saveChangelog, resetChangelog, act, busy: ref(false) }),
}))

const rel = (o: Partial<ReleaseState> = {}, changelog = 'orig', generated: string | null = 'gen'): ReleaseState => ({
  id: '20261111T143210', branch: 'b', pr: 1, pr_url: 'u', head_sha: 'abc', step: 'changelog', dashboard: true,
  units: [{ unit: 'ppg:staging:18', release_project: '', release_dir: '', release_id: '18.6-6', tag: '', changelog, generated }],
  cut: null, sync_state: '', sync: [], removals: [], qa: null, final: null, started_by: '', started_at: null, actions: [], ...o,
})

beforeEach(() => vi.clearAllMocks())

describe('ReleaseChangelogStep', () => {
  it('save sends head, unit and the edited section', async () => {
    const w = mount(ReleaseChangelogStep, { props: { release: rel() } })
    await w.get('textarea').setValue('<edited>')
    await w.get('[data-testid="save-changelog"]').trigger('click')
    expect(saveChangelog).toHaveBeenCalledWith('20261111T143210', 'abc', 'ppg:staging:18', '<edited>')
  })

  it('approve is disabled with unsaved edits', async () => {
    const w = mount(ReleaseChangelogStep, { props: { release: rel() } })
    const approve = w.get('[data-testid="approve-changelog"]')
    expect(approve.attributes('disabled')).toBeUndefined()
    await w.get('textarea').setValue('changed')
    expect(approve.attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="dirty-dot"]').exists()).toBe(true)
    await w.get('textarea').setValue('orig')
    expect(approve.attributes('disabled')).toBeUndefined()
    await approve.trigger('click')
    expect(act).toHaveBeenCalledWith('20261111T143210', 'approve-changelog', 'abc')
  })

  it('save is disabled without edits and reset when generated is null', async () => {
    const w = mount(ReleaseChangelogStep, { props: { release: rel({}, 'orig', null) } })
    expect(w.get('[data-testid="save-changelog"]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid="reset-changelog"]').attributes('disabled')).toBeDefined()
  })

  it('keeps unsaved edits and warns when head_sha changes', async () => {
    const w = mount(ReleaseChangelogStep, { props: { release: rel() } })
    await w.get('textarea').setValue('mine')
    await w.setProps({ release: rel({ head_sha: 'def' }, 'theirs') })
    await nextTick()
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('mine')
    expect(w.get('[data-testid="head-changed"]').text()).toContain('The release changed on GitHub')
  })

  it('takes the new text when there are no unsaved edits', async () => {
    const w = mount(ReleaseChangelogStep, { props: { release: rel() } })
    await w.setProps({ release: rel({ head_sha: 'def' }, 'theirs') })
    await nextTick()
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('theirs')
    expect(w.find('[data-testid="head-changed"]').exists()).toBe(false)
  })

  it('no warning after own save moves the head', async () => {
    const w = mount(ReleaseChangelogStep, { props: { release: rel() } })
    await w.get('textarea').setValue('saved')
    await w.setProps({ release: rel({ head_sha: 'def' }, 'saved') })
    expect(w.find('[data-testid="head-changed"]').exists()).toBe(false)
    expect(w.find('[data-testid="dirty-dot"]').exists()).toBe(false)
  })
})
