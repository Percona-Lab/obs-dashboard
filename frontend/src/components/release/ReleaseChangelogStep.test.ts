// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
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

beforeEach(() => { vi.clearAllMocks(); document.body.innerHTML = '' })

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

  const two = (head: string, a: string, b: string): ReleaseState => {
    const r = rel({ head_sha: head })
    r.units = [
      { ...r.units[0], unit: 'a', release_id: 'A-1', changelog: a, generated: 'genA' },
      { ...r.units[0], unit: 'b', release_id: 'B-1', changelog: b, generated: 'genB' },
    ]
    return r
  }

  it('saving one unit does not flag another unit with unsaved edits', async () => {
    const w = mount(ReleaseChangelogStep, { props: { release: two('h1', 'a0', 'b0') } })
    await w.get('textarea').setValue('a1')
    await w.findAll('[data-testid="unit-tab"]')[1].trigger('click')
    await w.get('textarea').setValue('b-mine')
    await w.setProps({ release: two('h2', 'a1', 'b0') })
    expect(w.find('[data-testid="head-changed"]').exists()).toBe(false)
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('b-mine')
    // the head moved again with B still unchanged upstream: still no warning
    await w.setProps({ release: two('h3', 'a1', 'b0') })
    expect(w.find('[data-testid="head-changed"]').exists()).toBe(false)
  })

  it('reset asks before discarding unsaved edits', async () => {
    const w = mount(ReleaseChangelogStep, { props: { release: rel() }, attachTo: document.body })
    await w.get('textarea').setValue('mine')
    await w.get('[data-testid="reset-changelog"]').trigger('click')
    expect(resetChangelog).not.toHaveBeenCalled()
    expect(w.get('[data-testid="release-confirm"]').text()).toContain('Discard your edits to 18.6-6 and restore the generated text?')
    await w.get('[data-testid="confirm-ok"]').trigger('click')
    expect(resetChangelog).toHaveBeenCalledWith('20261111T143210', 'abc', 'ppg:staging:18')
    w.unmount()
  })

  it('reset without unsaved edits does not ask', async () => {
    const w = mount(ReleaseChangelogStep, { props: { release: rel() }, attachTo: document.body })
    await w.get('[data-testid="reset-changelog"]').trigger('click')
    expect(w.find('[data-testid="release-confirm"]').exists()).toBe(false)
    expect(resetChangelog).toHaveBeenCalledWith('20261111T143210', 'abc', 'ppg:staging:18')
    w.unmount()
  })

  it('a successful reset leaves no warning and no unsaved dot', async () => {
    let done!: (v: boolean) => void
    resetChangelog.mockImplementationOnce(() => new Promise<boolean>((r) => { done = r }))
    const w = mount(ReleaseChangelogStep, { props: { release: rel() }, attachTo: document.body })
    await w.get('textarea').setValue('mine')
    await w.get('[data-testid="reset-changelog"]').trigger('click')
    await w.get('[data-testid="confirm-ok"]').trigger('click')
    // the refreshed snapshot lands before the call resolves
    await w.setProps({ release: rel({ head_sha: 'def' }, 'gen', 'gen') })
    done(true)
    await flushPromises()
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('gen')
    expect(w.find('[data-testid="head-changed"]').exists()).toBe(false)
    expect(w.find('[data-testid="dirty-dot"]').exists()).toBe(false)
    w.unmount()
  })

  it('a failed reset leaves the text untouched', async () => {
    resetChangelog.mockImplementationOnce(() => Promise.resolve(false))
    const w = mount(ReleaseChangelogStep, { props: { release: rel() }, attachTo: document.body })
    await w.get('textarea').setValue('mine')
    await w.get('[data-testid="reset-changelog"]').trigger('click')
    await w.get('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('mine')
    w.unmount()
  })

  it('a failed save keeps the edited text', async () => {
    saveChangelog.mockImplementationOnce(() => Promise.resolve(false))
    const w = mount(ReleaseChangelogStep, { props: { release: rel() } })
    await w.get('textarea').setValue('mine')
    await w.get('[data-testid="save-changelog"]').trigger('click')
    await flushPromises()
    await w.setProps({ release: rel() })
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('mine')
    expect(w.find('[data-testid="dirty-dot"]').exists()).toBe(true)
  })

  it('switching unit tabs keeps unsaved edits', async () => {
    const w = mount(ReleaseChangelogStep, { props: { release: two('h1', 'a0', 'b0') } })
    await w.get('textarea').setValue('a-mine')
    const tabs = w.findAll('[data-testid="unit-tab"]')
    await tabs[1].trigger('click')
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('b0')
    await tabs[0].trigger('click')
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('a-mine')
  })
})
