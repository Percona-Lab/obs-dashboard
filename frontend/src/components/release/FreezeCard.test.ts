// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import FreezeCard from './FreezeCard.vue'
import type { UnitFreeze, UnitRow } from '../../types/release'

const uf = (o: Partial<UnitFreeze>): UnitFreeze => ({
  unit: 'ppg:staging:18', state: 'unfrozen', instances: [], frozen_by: '', frozen_at: null, pending: null, failed: null, ...o,
})
const row = (o: Partial<UnitRow>): UnitRow => ({
  unit: 'ppg:staging:18', members: [], release_project: '', release_dir: '', last_release: '', built: 3, total: 3, tickable: true, ...o,
})

const freeze: UnitFreeze[] = [
  uf({ unit: 'ppg:staging:18', state: 'frozen', frozen_by: 'maria@percona.com', frozen_at: '2026-11-20T10:00:00Z',
    instances: [{ instance: 'labs', state: 'frozen' }, { instance: 'obs', state: 'frozen' }] }),
  uf({ unit: 'ppg:staging:17:extras', state: 'partial', frozen_by: 'maria@percona.com',
    instances: [{ instance: 'labs', state: 'frozen' }, { instance: 'obs', state: 'unfrozen' }] }),
  uf({ unit: 'ppg:staging:16', state: 'unfrozen', pending: { action: 'freeze', url: 'https://github.com/r/7', by: 'rui@percona.com' } }),
  uf({ unit: 'ppg:staging:17', state: 'unfrozen' }),
  uf({ unit: 'ppg:staging:15', state: '' }),
]
const units = [row({ unit: 'ppg:staging:18' }), row({ unit: 'ppg:staging:17' })]

const mk = (p: Partial<{ freeze: UnitFreeze[] | null; units: UnitRow[]; error: string; busy: boolean; pickerOpen: boolean }> = {}) =>
  mount(FreezeCard, { props: { freeze, units, error: '', busy: false, pickerOpen: false, ...p } })

describe('FreezeCard', () => {
  it('lists the frozen, partial and pending units with the count', () => {
    const w = mk()
    expect(w.findAll('[data-unit]').map((r) => r.attributes('data-unit'))).toEqual(['ppg:staging:18', 'ppg:staging:17:extras', 'ppg:staging:16'])
    expect(w.get('[data-testid="freeze-count"]').text()).toBe('2 frozen')
    expect(w.text()).toContain('Build freeze')
    expect(w.text()).toContain('A frozen unit ignores dependency updates. Fixes merged to main still build.')
  })

  it('describes frozen and partial rows', () => {
    const w = mk()
    const a = w.get('[data-unit="ppg:staging:18"]').text()
    expect(a).toContain('frozen by maria@percona.com')
    expect(a).toContain('all instances')
    const b = w.get('[data-unit="ppg:staging:17:extras"]')
    expect(b.text()).toContain('labs only — not frozen on obs')
    expect(b.find('[data-testid="freeze-everywhere"]').exists()).toBe(true)
    expect(w.get('[data-unit="ppg:staging:18"]').find('[data-testid="freeze-everywhere"]').exists()).toBe(false)
  })

  it('shows a pending row with its spinner and disabled buttons', () => {
    const r = mk().get('[data-unit="ppg:staging:16"]')
    expect(r.text()).toContain('Freezing…')
    expect(r.text()).toContain('started by rui@percona.com')
    expect(r.find('[data-testid="spinner"]').exists()).toBe(true)
    expect(r.get('a').attributes('href')).toBe('https://github.com/r/7')
    expect(r.get('[data-testid="unfreeze"]').attributes('disabled')).toBeDefined()
  })

  it('does not link an unsafe run url', () => {
    const w = mk({ freeze: [uf({ unit: 'ppg:staging:16', pending: { action: 'unfreeze', url: 'javascript:x', by: '' } })] })
    expect(w.text()).toContain('Unfreezing…')
    expect(w.text()).not.toContain('started by')
    expect(w.find('a').exists()).toBe(false)
  })

  it('confirms before unfreezing', async () => {
    const w = mk()
    await w.get('[data-unit="ppg:staging:18"] [data-testid="unfreeze"]').trigger('click')
    expect(w.get('[data-testid="release-confirm"]').text()).toContain('Unfreeze ppg:staging:18?')
    expect(w.find('[data-testid="confirm-warning"]').exists()).toBe(false)
    await w.get('[data-testid="confirm-ok"]').trigger('click')
    expect(w.emitted('unfreeze')![0]).toEqual([['ppg:staging:18']])
    expect(w.find('[data-testid="release-confirm"]').exists()).toBe(false)
  })

  it('warns when a release of the unit is in flight', async () => {
    const w = mk({ units: [row({ unit: 'ppg:staging:18', tickable: false, blocked: { reason: 'in_flight', text: 'x' } })] })
    await w.get('[data-unit="ppg:staging:18"] [data-testid="unfreeze"]').trigger('click')
    expect(w.get('[data-testid="confirm-warning"]').text()).toContain('A release of this unit is in progress.')
  })

  it('cancelling the confirm emits nothing', async () => {
    const w = mk()
    await w.get('[data-unit="ppg:staging:18"] [data-testid="unfreeze"]').trigger('click')
    await w.get('[data-testid="confirm-cancel"]').trigger('click')
    expect(w.emitted('unfreeze')).toBeUndefined()
    expect(w.find('[data-testid="release-confirm"]').exists()).toBe(false)
  })

  it('freezes a partial unit everywhere', async () => {
    const w = mk()
    await w.get('[data-unit="ppg:staging:17:extras"] [data-testid="freeze-everywhere"]').trigger('click')
    expect(w.emitted('freeze')![0]).toEqual([['ppg:staging:17:extras']])
  })

  it('shows a failed run', () => {
    const w = mk({ freeze: [uf({ state: 'frozen', failed: { action: 'freeze', instances: ['obs'], url: 'https://github.com/r/8' } })] })
    expect(w.text()).toContain('The last freeze failed on obs')
    expect(w.find('a[href="https://github.com/r/8"]').exists()).toBe(true)
  })

  it('says so when nothing is frozen', () => {
    expect(mk({ freeze: [uf({ unit: 'ppg:staging:17' })] }).text()).toContain('No frozen units.')
    expect(mk({ freeze: [uf({ unit: 'ppg:staging:17' })] }).get('[data-testid="freeze-count"]').text()).toBe('0 frozen')
  })

  it('shows the load error, or a loading indicator', () => {
    expect(mk({ freeze: null, error: 'boom' }).text()).toContain('Could not load the freeze state: boom')
    const w = mk({ freeze: null })
    expect(w.find('[data-testid="release-loading"]').exists()).toBe(true)
  })

  it('names instances that could not be read', () => {
    const w = mk({ freeze: [uf({ state: 'frozen', instances: [{ instance: 'labs', state: 'unknown' }, { instance: 'obs', state: 'frozen' }] })] })
    expect(w.text()).toContain('could not read labs')
  })

  it('asks to open the picker', async () => {
    const w = mk()
    expect(w.find('[data-testid="freeze-picker"]').exists()).toBe(false)
    await w.get('[data-testid="freeze-open"]').trigger('click')
    expect(w.emitted('update:pickerOpen')![0]).toEqual([true])
  })

  it('renders the picker only while pickerOpen is true and passes its freeze on', async () => {
    const w = mk({ pickerOpen: true })
    expect(w.find('[data-testid="freeze-picker"]').exists()).toBe(true)
    await w.get('[data-unit="ppg:staging:17"] input').setValue(true)
    await w.get('[data-testid="freeze-submit"]').trigger('click')
    expect(w.emitted('freeze')![0]).toEqual([['ppg:staging:17']])
    expect(w.emitted('update:pickerOpen')).toBeUndefined()
  })

  it('asks to close the picker on cancel', async () => {
    const w = mk({ pickerOpen: true })
    await w.get('[data-testid="freeze-cancel"]').trigger('click')
    expect(w.emitted('update:pickerOpen')![0]).toEqual([false])
  })

  it('does not touch the picker when busy changes', async () => {
    const w = mk({ pickerOpen: true })
    await w.setProps({ busy: true })
    await w.setProps({ busy: false })
    expect(w.emitted('update:pickerOpen')).toBeUndefined()
    expect(w.find('[data-testid="freeze-picker"]').exists()).toBe(true)
  })

  it('shows the load error above the last known rows', () => {
    const w = mk({ error: 'boom' })
    expect(w.get('[data-testid="freeze-error"]').text()).toContain('Could not load the freeze state: boom')
    expect(w.findAll('[data-unit]').length).toBe(3)
    expect(w.find('[data-testid="release-loading"]').exists()).toBe(false)
  })

  it('lists a unit whose run failed everywhere, without action buttons', () => {
    const w = mk({ freeze: [uf({ unit: 'ppg:staging:16', state: 'unfrozen', failed: { action: 'freeze', instances: ['labs', 'obs'], url: 'https://github.com/r/9' } })] })
    const r = w.get('[data-unit="ppg:staging:16"]')
    expect(r.text()).toContain('The last freeze failed on labs, obs')
    expect(r.find('a[href="https://github.com/r/9"]').exists()).toBe(true)
    expect(r.find('[data-testid="unfreeze"]').exists()).toBe(false)
    expect(r.find('[data-testid="freeze-everywhere"]').exists()).toBe(false)
    expect(w.get('[data-testid="freeze-count"]').text()).toBe('0 frozen')
    expect(w.text()).not.toContain('No frozen units.')
    const e = mk({ freeze: [uf({ state: '', failed: { action: 'unfreeze', instances: [], url: '' } })] })
    expect(e.text()).toContain('The last unfreeze failed')
    expect(e.text()).not.toContain('failed on')
  })

  it('lists a unit that could not be read at all, without action buttons', () => {
    const w = mk({ freeze: [uf({ unit: 'ppg:staging:15', state: '', instances: [{ instance: 'labs', state: 'unknown' }, { instance: 'obs', state: 'unknown' }] })] })
    const r = w.get('[data-unit="ppg:staging:15"]')
    expect(r.text()).toContain('could not read labs, obs')
    expect(r.find('[data-testid="unfreeze"]').exists()).toBe(false)
    expect(w.text()).not.toContain('No frozen units.')
  })

  it('does not list unknown instances as not frozen, and words a partial with no frozen instance', () => {
    const a = mk({ freeze: [uf({ state: 'partial', instances: [
      { instance: 'labs', state: 'frozen' }, { instance: 'obs', state: 'unfrozen' }, { instance: 'x', state: 'unknown' }] })] })
    expect(a.text()).toContain('labs only — not frozen on obs')
    expect(a.text()).not.toContain('not frozen on obs, x')
    expect(a.text()).toContain('could not read x')
    const b = mk({ freeze: [uf({ state: 'partial', instances: [{ instance: 'labs', state: 'partial' }, { instance: 'obs', state: 'unfrozen' }] })] })
    expect(b.text()).toContain('partly frozen on labs')
    expect(b.text()).not.toContain(' only')
  })
})
