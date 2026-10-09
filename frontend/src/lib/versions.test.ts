import { describe, it, expect } from 'vitest'
import { splitVersionKey } from './versions'

describe('splitVersionKey', () => {
  it('splits an extension key and leaves a plain key whole', () => {
    expect(splitVersionKey('17:extras')).toEqual(['17', 'extras'])
    expect(splitVersionKey('17')).toEqual(['17', undefined])
  })
})
