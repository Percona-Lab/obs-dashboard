// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import VersionSelector from './VersionSelector.vue'

const KEYS = ['19', '18', '18:extras', '16', '16:extras', '16:tde', 'common', 'common:extras', 'common:tools']

function chips(wrapper: ReturnType<typeof mount>, row: 'version' | 'subproject'): string[] {
  return wrapper.findAll(`[data-row="${row}"] button`).map(b => b.text())
}

describe('VersionSelector', () => {
  it('renders distinct versions and no subproject row for a lone version', () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: '19' } })
    expect(chips(w, 'version')).toEqual(['19', '18', '16', 'common'])
    expect(w.find('[data-row="subproject"]').exists()).toBe(false)
  })

  it('renders base plus extensions for the selected version', () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: '16:tde' } })
    expect(chips(w, 'subproject')).toEqual(['base', 'extras', 'tde'])
  })

  it('renders base, extras, tools for common', () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: 'common' } })
    expect(chips(w, 'subproject')).toEqual(['base', 'extras', 'tools'])
  })

  it('appends an All chip when allowAll and hides subprojects while All is selected', () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: '', allowAll: true } })
    expect(chips(w, 'version')).toEqual(['19', '18', '16', 'common', 'All'])
    expect(w.find('[data-row="subproject"]').exists()).toBe(false)
  })

  it('emits the plain key when a version chip is clicked', async () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: '19' } })
    await w.findAll('[data-row="version"] button')[1].trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([['18']])
  })

  it('emits the first extension when the version has no plain key', async () => {
    const w = mount(VersionSelector, { props: { keys: ['19', '18:extras', '18:tde'], modelValue: '19' } })
    await w.findAll('[data-row="version"] button')[1].trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([['18:extras']])
  })

  it('emits the subproject key and the empty key for All', async () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: 'common', allowAll: true } })
    await w.findAll('[data-row="subproject"] button')[2].trigger('click')
    await w.findAll('[data-row="version"] button')[4].trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([['common:tools'], ['']])
  })

  it('marks the selected version and subproject chips as active', () => {
    const w = mount(VersionSelector, { props: { keys: KEYS, modelValue: '16:tde' } })
    const active = w.findAll('button.font-bold').map(b => b.text())
    expect(active).toEqual(['16', 'tde'])
  })
})
