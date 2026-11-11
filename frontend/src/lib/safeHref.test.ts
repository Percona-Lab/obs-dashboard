import { describe, it, expect } from 'vitest'
import { safeHref } from './safeHref'

describe('safeHref', () => {
  it('passes only http(s) urls', () => {
    expect(safeHref('https://a.b/c')).toBe('https://a.b/c')
    expect(safeHref('http://a.b')).toBe('http://a.b')
    expect(safeHref('javascript:alert(1)')).toBeUndefined()
    expect(safeHref('')).toBeUndefined()
    expect(safeHref('//a.b')).toBeUndefined()
  })
})
