// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ReleaseUnitList from './ReleaseUnitList.vue'
import type { UnitRow } from '../../types/release'

const row = (o: Partial<UnitRow>): UnitRow => ({
  unit: 'ppg:staging:18', members: ['ppg:staging:18'], release_project: '', release_dir: '',
  last_release: '18.6-5', built: 3, total: 3, tickable: true, ...o,
})

const units: UnitRow[] = [
  row({}),
  row({ unit: 'ppg:staging:18:extras', tickable: false, built: 1, total: 3,
    blocked: { reason: 'not_built', text: 'Not fully built', packages: ['pgvector'] } }),
  row({ unit: 'ppg:staging:17', tickable: false,
    blocked: { reason: 'in_flight', text: 'Release 17.10-2 is in progress', release_id: '17.10-2' } }),
  row({ unit: 'ppg:staging:common:tools', members: [] }),
]

const mk = (p: Partial<{ units: UnitRow[]; busy: boolean; error: string }> = {}) =>
  mount(ReleaseUnitList, { props: { units, busy: false, error: '', ...p } })

describe('ReleaseUnitList', () => {
  it('disables blocked units and shows the reason', () => {
    const w = mk()
    const blocked = w.get('[data-unit="ppg:staging:18:extras"]')
    expect(blocked.get('input[type=checkbox]').attributes('disabled')).toBeDefined()
    expect(blocked.text()).toContain('Not fully built')
    expect(blocked.text()).toContain('pgvector')
  })

  it('opens the in-flight release from the alert', async () => {
    const w = mk()
    await w.get('[data-unit="ppg:staging:17"] button[data-testid="open-release"]').trigger('click')
    expect(w.emitted('open')![0]).toEqual(['17.10-2'])
  })

  it('emits cut with ticked units only', async () => {
    const w = mk()
    const cut = w.get('[data-testid="cut-release"]')
    expect(cut.attributes('disabled')).toBeDefined()
    await w.get('[data-unit="ppg:staging:common:tools"] input').setValue(true)
    expect(cut.attributes('disabled')).toBeUndefined()
    await cut.trigger('click')
    expect(w.emitted('cut')![0]).toEqual([['ppg:staging:common:tools']])
  })

  it('shows a spinner in the cut button while the cut is being sent', () => {
    expect(mk({ busy: true }).get('[data-testid="cut-release"]').find('[data-testid="spinner"]').exists()).toBe(true)
    expect(mk().get('[data-testid="cut-release"]').find('[data-testid="spinner"]').exists()).toBe(false)
  })

  it('disables cut while busy', async () => {
    const w = mk({ busy: true })
    await w.get('[data-unit="ppg:staging:18"] input').setValue(true)
    expect(w.get('[data-testid="cut-release"]').attributes('disabled')).toBeDefined()
  })

  it('groups by major and common', () => {
    const heads = mk().findAll('[data-testid="unit-group"]').map((h) => h.text())
    expect(heads).toEqual(['PostgreSQL 18', 'PostgreSQL 17', 'Common'])
  })

  it('shows an error box instead of an empty list', () => {
    const w = mk({ units: [], error: 'boom' })
    expect(w.get('[data-testid="units-error"]').text()).toContain('boom')
    expect(w.find('[data-testid="cut-release"]').exists()).toBe(false)
  })

  it('does not re-tick a unit that was blocked in between and becomes tickable again', async () => {
    const w = mk()
    const tools = 'ppg:staging:common:tools'
    await w.get(`[data-unit="${tools}"] input`).setValue(true)
    const blockedUnits = units.map((u) => u.unit === tools ? { ...u, tickable: false, blocked: { reason: 'unchanged' as const, text: 'Nothing new' } } : u)
    await w.setProps({ units: blockedUnits })
    await w.get('[data-unit="ppg:staging:18"] input').setValue(true)
    await w.get('[data-testid="cut-release"]').trigger('click')
    expect(w.emitted('cut')![0]).toEqual([['ppg:staging:18']])
    await w.setProps({ units })
    expect((w.get(`[data-unit="${tools}"] input`).element as HTMLInputElement).checked).toBe(false)
    await w.get('[data-testid="cut-release"]').trigger('click')
    expect(w.emitted('cut')![1]).toEqual([['ppg:staging:18']])
  })

  it('the not-built alert opens the Builds tab', async () => {
    const w = mk()
    const btn = w.get('[data-unit="ppg:staging:18:extras"] button[data-testid="open-builds"]')
    expect(btn.text()).toBe('Open in Builds')
    await btn.trigger('click')
    expect(w.emitted('open-builds')).toHaveLength(1)
    expect(w.find('[data-unit="ppg:staging:17"] [data-testid="open-builds"]').exists()).toBe(false)
  })
})
