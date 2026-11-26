// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import FreezePicker from './FreezePicker.vue'
import type { UnitFreeze, UnitRow } from '../../types/release'

const uf = (o: Partial<UnitFreeze>): UnitFreeze => ({
  unit: 'ppg:staging:18', state: 'unfrozen', instances: [], frozen_by: '', frozen_at: null, pending: null, failed: null, ...o,
})
const row = (o: Partial<UnitRow>): UnitRow => ({
  unit: 'ppg:staging:18', members: ['pg18'], release_project: '', release_dir: '', last_release: '', built: 2, total: 3, tickable: true, ...o,
})
const freeze: UnitFreeze[] = [
  uf({ unit: 'ppg:staging:18', state: 'frozen' }),
  uf({ unit: 'ppg:staging:17:extras', state: 'partial' }),
  uf({ unit: 'ppg:staging:16', pending: { action: 'freeze', url: '', by: '' } }),
  uf({ unit: 'ppg:staging:17' }),
  uf({ unit: 'ppg:staging:15', state: '' }),
]
const units = freeze.map((f) => row({ unit: f.unit }))
const mk = (p: Partial<{ busy: boolean }> = {}) => mount(FreezePicker, { props: { freeze, units, busy: false, ...p } })
const box = (w: ReturnType<typeof mk>, u: string) => w.get(`[data-unit="${u}"] input`)

describe('FreezePicker', () => {
  it('is an accessible dialog with a note and grouped units', () => {
    const w = mk()
    const d = w.get('[data-testid="freeze-picker"]')
    expect(d.attributes('role')).toBe('dialog')
    expect(d.attributes('aria-modal')).toBe('true')
    expect(w.text()).toContain('Unfreezing later lets the postponed rebuilds start.')
    expect(w.findAll('[data-testid="unit-group"]').map((h) => h.text())).toEqual(['PostgreSQL 18', 'PostgreSQL 17', 'PostgreSQL 16', 'PostgreSQL 15'])
    expect(w.get('[data-unit="ppg:staging:18"]').text()).toContain('2 / 3 built')
    expect(w.get('[data-unit="ppg:staging:18"]').text()).toContain('pg18')
    expect(box(w, 'ppg:staging:18').attributes('aria-label')).toBe('Select ppg:staging:18')
  })

  it('disables the units that cannot be frozen, with the reason', () => {
    const w = mk()
    for (const [u, why] of [['ppg:staging:18', 'already frozen'], ['ppg:staging:16', 'a freeze is running'], ['ppg:staging:15', 'state unknown']]) {
      expect(box(w, u).attributes('disabled')).toBeDefined()
      expect(w.get(`[data-unit="${u}"]`).text()).toContain(why)
    }
    expect(box(w, 'ppg:staging:17').attributes('disabled')).toBeUndefined()
    expect(box(w, 'ppg:staging:17:extras').attributes('disabled')).toBeUndefined()
  })

  it('says an unfreeze is running', () => {
    const w = mount(FreezePicker, { props: { freeze: [uf({ pending: { action: 'unfreeze', url: '', by: '' } })], units: [row({})], busy: false } })
    expect(w.text()).toContain('an unfreeze is running')
  })

  it('emits the ticked units', async () => {
    const w = mk()
    const btn = w.get('[data-testid="freeze-submit"]')
    expect(btn.attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('0 units selected')
    await box(w, 'ppg:staging:17').setValue(true)
    expect(btn.text()).toContain('Freeze 1 unit')
    expect(w.text()).toContain('1 unit selected')
    await btn.trigger('click')
    expect(w.emitted('freeze')![0]).toEqual([['ppg:staging:17']])
  })

  it('cancels from the button, Esc and the backdrop', async () => {
    const w = mk()
    await w.get('[data-testid="freeze-cancel"]').trigger('click')
    await w.get('[data-testid="freeze-picker-backdrop"]').trigger('keydown', { key: 'Escape' })
    await w.get('[data-testid="freeze-picker-backdrop"]').trigger('click')
    expect(w.emitted('cancel')).toHaveLength(3)
  })

  it('disables the button while busy', async () => {
    const w = mk({ busy: true })
    await box(w, 'ppg:staging:17').setValue(true)
    expect(w.get('[data-testid="freeze-submit"]').attributes('disabled')).toBeDefined()
  })
})
