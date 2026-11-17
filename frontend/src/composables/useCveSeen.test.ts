// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SEEN_STORAGE_KEY, resetCveSeenForTests, useCveSeen } from './useCveSeen'

beforeEach(() => {
  localStorage.clear()
  resetCveSeenForTests()
})
afterEach(() => vi.restoreAllMocks())

describe('useCveSeen', () => {
  it('is new until marked seen, and persists', () => {
    const { isNew, markAllSeen } = useCveSeen()
    expect(isNew('CVE-1|openssl|')).toBe(true)
    markAllSeen(['CVE-1|openssl|'])
    expect(isNew('CVE-1|openssl|')).toBe(false)
    expect(JSON.parse(localStorage.getItem(SEEN_STORAGE_KEY)!)).toContain('CVE-1|openssl|')
  })

  it('stays seen after a reload', () => {
    const { isNew, markAllSeen } = useCveSeen()
    markAllSeen(['CVE-1|openssl|'])
    resetCveSeenForTests()
    expect(isNew('CVE-1|openssl|')).toBe(false)
    expect(isNew('CVE-2|x|')).toBe(true)
  })

  it('only adds to the seen set', () => {
    const { isNew, markAllSeen } = useCveSeen()
    markAllSeen(['a'])
    markAllSeen(['b'])
    expect(isNew('a')).toBe(false)
    expect(isNew('b')).toBe(false)
  })

  it('disables markers on corrupt JSON without throwing', () => {
    localStorage.setItem(SEEN_STORAGE_KEY, '{not json')
    resetCveSeenForTests()
    const { available, isNew, markAllSeen } = useCveSeen()
    expect(available.value).toBe(false)
    expect(isNew('anything')).toBe(false)
    expect(() => markAllSeen(['a'])).not.toThrow()
  })

  it('treats valid JSON that is not a string array as corrupt', () => {
    localStorage.setItem(SEEN_STORAGE_KEY, '{"a":1}')
    resetCveSeenForTests()
    const { available, isNew } = useCveSeen()
    expect(available.value).toBe(false)
    expect(isNew('a')).toBe(false)
  })

  it('disables markers when getItem throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('denied') })
    resetCveSeenForTests()
    const { available, isNew } = useCveSeen()
    expect(available.value).toBe(false)
    expect(isNew('a')).toBe(false)
  })

  it('disables markers when setItem throws', () => {
    const { available, isNew, markAllSeen } = useCveSeen()
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota') })
    expect(() => markAllSeen(['a'])).not.toThrow()
    expect(available.value).toBe(false)
    expect(isNew('a')).toBe(false)
  })
})
