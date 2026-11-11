// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { dropDraft, getDraft, hasDrafts, resetDraftsForTests, sameText, setDraft } from './useChangelogDrafts'

beforeEach(() => resetDraftsForTests())
afterEach(() => vi.restoreAllMocks())

describe('useChangelogDrafts', () => {
  it('keeps drafts by release and unit', () => {
    setDraft('r1', 'a', { text: 'x', synced: 'o' })
    setDraft('r2', 'a', { text: 'y', synced: 'o' })
    expect(getDraft('r1', 'a')).toEqual({ text: 'x', synced: 'o' })
    expect(getDraft('r1', 'b')).toBeUndefined()
    dropDraft('r1', 'a')
    expect(hasDrafts('r1')).toBe(false)
    expect(hasDrafts('r2')).toBe(true)
  })

  it('compares text ignoring trailing newlines and CRLF', () => {
    expect(sameText('a\r\nb\r\n', 'a\nb')).toBe(true)
    expect(sameText('a\nb\n\n', 'a\nb\n')).toBe(true)
    expect(sameText('a\nb', 'a\n\nb')).toBe(false)
    expect(sameText(' a', 'a')).toBe(false)
  })

  it('guards the page against unloading only while a draft exists', () => {
    const add = vi.spyOn(window, 'addEventListener')
    const remove = vi.spyOn(window, 'removeEventListener')
    setDraft('r1', 'a', { text: 'x', synced: 'o' })
    setDraft('r1', 'b', { text: 'x', synced: 'o' })
    const added = add.mock.calls.filter((c) => c[0] === 'beforeunload')
    expect(added).toHaveLength(1)
    const ev = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)

    dropDraft('r1', 'a')
    expect(remove.mock.calls.filter((c) => c[0] === 'beforeunload')).toHaveLength(0)
    dropDraft('r1', 'b')
    const removed = remove.mock.calls.filter((c) => c[0] === 'beforeunload')
    expect(removed).toHaveLength(1)
    expect(removed[0][1]).toBe(added[0][1])
    const later = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(later)
    expect(later.defaultPrevented).toBe(false)
  })
})
