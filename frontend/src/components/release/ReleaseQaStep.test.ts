// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import ReleaseQaStep from './ReleaseQaStep.vue'
import type { Lane, ReleaseState } from '../../types/release'

const act = vi.fn(() => Promise.resolve(true))
vi.mock('../../composables/useRelease', () => ({ useRelease: () => ({ act, busy: ref(false) }) }))

const lane = (o: Partial<Lane>): Lane => ({ name: 'l', unit: 'u', jenkins_url: '', url: 'https://github.com/j/1', state: 'success', ...o })
type QA = NonNullable<ReleaseState['qa']>
type QAIn = Pick<QA, 'state' | 'lanes'> & Partial<QA>
const rel = (qaIn: QAIn, o: Partial<ReleaseState> = {}): ReleaseState => { const qa: QA = { url: '', error: '', ...qaIn }; return {
  id: 'r1', branch: 'b', pr: 5, pr_url: '', head_sha: 'abc', step: 'qa', dashboard: true,
  units: [{ unit: 'u', release_project: '', release_dir: '', release_id: '18.6-6', tag: '', changelog: '', generated: null }],
  cut: null, sync_state: 'success', sync_url: '', sync_error: '', sync: [], removals: [], qa, final: null, started_by: '', started_at: null, actions: [], cut_units: [], ...o,
} }
const mountQ = (qa: QAIn, o: Partial<ReleaseState> = {}) => mount(ReleaseQaStep, { props: { release: rel(qa, o) }, attachTo: document.body })

beforeEach(() => { vi.clearAllMocks(); document.body.innerHTML = '' })

describe('ReleaseQaStep', () => {
  it('spins on lanes that have not finished and while QA has not produced lanes yet', () => {
    const w = mountQ({ state: 'running', lanes: [lane({ name: 'a', state: 'running' }), lane({ name: 'b', state: 'queued' }), lane({ name: 'c', state: 'success' })] })
    expect(w.findAll('[data-testid="lane-running"] [data-testid="spinner"]')).toHaveLength(2)
    expect(w.find('[data-testid="qa-starting"]').exists()).toBe(false)

    const starting = mountQ({ state: 'running', lanes: [] })
    expect(starting.get('[data-testid="qa-starting"]').text()).toContain('Waiting for QA to start')
    expect(starting.find('[data-testid="qa-starting"] [data-testid="spinner"]').exists()).toBe(true)

    const done = mountQ({ state: 'finished', lanes: [lane({ name: 'c', state: 'success' })] })
    expect(done.find('[data-testid="spinner"]').exists()).toBe(false)
  })

  it('links failed lanes to Jenkins and falls back to the GitHub job', () => {
    const w = mountQ({ state: 'finished', lanes: [
      lane({ name: 'a', state: 'failure', jenkins_url: 'https://jenkins/job/a' }),
      lane({ name: 'b', state: 'failure', jenkins_url: '', url: 'https://github.com/j/b' }),
    ] })
    const links = w.findAll('[data-testid="lane-failed"] a')
    expect(links[0].text()).toBe('Jenkins job ↗')
    expect(links[0].attributes('href')).toBe('https://jenkins/job/a')
    expect(links[0].attributes('target')).toBe('_blank')
    expect(links[1].text()).toBe('GitHub job ↗')
    expect(links[1].attributes('href')).toBe('https://github.com/j/b')
  })

  it('groups lanes by unit with failed first and passed collapsed', async () => {
    const w = mountQ({ state: 'finished', lanes: [
      lane({ name: 'ok1', unit: 'u' }), lane({ name: 'bad', unit: 'u', state: 'failure' }),
      lane({ name: 'ok2', unit: 'u' }), lane({ name: 'x', unit: '' }),
    ] })
    const titles = w.findAll('[data-testid="qa-group-title"]').map((t) => t.text())
    expect(titles).toEqual(['18.6-6', 'Other'])
    expect(w.get('[data-testid="qa-counts"]').text()).toContain('3 passed')
    expect(w.get('[data-testid="qa-counts"]').text()).toContain('1 failed')
    expect(w.find('[data-testid="lane-passed"]').exists()).toBe(false)
    const t = w.get('[data-testid="toggle-passed"]')
    expect(t.text()).toBe('2 passed lanes · show')
    await t.trigger('click')
    expect(w.findAll('[data-testid="lane-passed"]')).toHaveLength(2)
  })

  it('release is disabled while QA is running', () => {
    const w = mountQ({ state: 'running', lanes: [lane({ state: 'running' })] })
    expect((w.get('[data-testid="release"]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('release with failed lanes needs "Release anyway" and sends confirm_failed', async () => {
    const w = mountQ({ state: 'finished', lanes: [lane({ name: 'bad-lane', state: 'failure' })] })
    await w.get('[data-testid="release"]').trigger('click')
    expect(act).not.toHaveBeenCalled()
    expect(w.get('[data-testid="release-confirm"]').text()).toContain('bad-lane')
    expect(w.get('[data-testid="confirm-ok"]').text()).toBe('Release anyway')
    await w.get('[data-testid="confirm-ok"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'release', 'abc', true)
  })

  it('release uses the head shown when the dialog opened', async () => {
    const w = mountQ({ state: 'finished', lanes: [lane({})] })
    await w.get('[data-testid="release"]').trigger('click')
    await w.setProps({ release: rel({ state: 'finished', lanes: [lane({})] }, { head_sha: 'moved' }) })
    await w.get('[data-testid="confirm-ok"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'release', 'abc', false)
  })

  it('release without failures sends confirm_failed false', async () => {
    const w = mountQ({ state: 'finished', lanes: [lane({})] })
    await w.get('[data-testid="release"]').trigger('click')
    expect(act).not.toHaveBeenCalled()
    await w.get('[data-testid="confirm-ok"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'release', 'abc', false)
  })

  it('approve QA is offered only while waiting', async () => {
    const w = mountQ({ state: 'waiting', lanes: [] }, { removals: [{ project: 'p:rel', item: 'package six' }] })
    expect(w.text()).toContain('Releasing will remove 1 item from p:rel')
    await w.get('[data-testid="approve-qa"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'approve-qa', 'abc')
    const r = mountQ({ state: 'running', lanes: [lane({ state: 'running' })] })
    expect(r.find('[data-testid="approve-qa"]').exists()).toBe(false)
  })

  it('confirming after QA went back to running does not release', async () => {
    const w = mountQ({ state: 'finished', lanes: [lane({})] })
    await w.get('[data-testid="release"]').trigger('click')
    await w.setProps({ release: rel({ state: 'running', lanes: [lane({ state: 'running' })] }) })
    await w.get('[data-testid="confirm-ok"]').trigger('click')
    expect(act).not.toHaveBeenCalled()
    expect(w.find('[data-testid="release-confirm"]').exists()).toBe(false)
  })

  it('an unfinished lane counts as failed for the confirmation', async () => {
    const w = mountQ({ state: 'finished', lanes: [lane({ name: 'odd-lane', state: 'running' })] })
    await w.get('[data-testid="release"]').trigger('click')
    expect(w.get('[data-testid="release-confirm"]').text()).toContain('odd-lane')
    expect(w.get('[data-testid="release-confirm"]').text()).toContain('did not finish')
    expect(w.get('[data-testid="confirm-ok"]').text()).toBe('Release anyway')
    await w.get('[data-testid="confirm-ok"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'release', 'abc', true)
  })

  it('says why QA could not start, links to the run and offers Retry QA', async () => {
    const w = mountQ({ state: 'failure', lanes: [], url: 'https://github.com/o/r/actions/runs/11', error: 'QA could not start.' })
    const box = w.get('[data-testid="qa-failed"]')
    expect(box.text()).toContain('QA could not start.')
    expect(box.get('a').attributes('href')).toBe('https://github.com/o/r/actions/runs/11')
    expect(box.get('a').attributes('rel')).toBe('noopener noreferrer')
    expect(w.get('[data-testid="release"]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="approve-qa"]').exists()).toBe(false)
    await w.get('[data-testid="retry-qa"]').trigger('click')
    expect(act).toHaveBeenCalledWith('r1', 'retry-qa', 'abc')
  })

  it('a fork release gets no Retry QA', () => {
    const w = mountQ({ state: 'failure', lanes: [], url: '', error: 'The download servers did not serve the pre-release in time.' }, { dashboard: false })
    expect(w.get('[data-testid="qa-failed"]').text()).toContain('The download servers did not serve the pre-release in time.')
    expect(w.find('[data-testid="qa-failed"] a').exists()).toBe(false)
    expect(w.find('[data-testid="retry-qa"]').exists()).toBe(false)
  })
})
