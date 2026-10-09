import { describe, it, expect } from 'vitest'
import {
  splitVersionKey,
  deriveVersionKeys,
  matchesVersionKey,
  versionKeyProject,
  selectorVersions,
  keyForVersion,
} from './versions'

const ABSORBED = ['containers', 'tarballs']

// Mirrors production ppg:staging on 2026-10-09 plus the shared trees that
// usePackages receives in the same corpus.
const STAGING = [
  'ppg:staging:14', 'ppg:staging:14:containers', 'ppg:staging:14:tarballs',
  'ppg:staging:15', 'ppg:staging:15:containers', 'ppg:staging:15:tarballs',
  'ppg:staging:16', 'ppg:staging:16:containers', 'ppg:staging:16:extras',
  'ppg:staging:16:extras:containers', 'ppg:staging:16:tarballs', 'ppg:staging:16:tde',
  'ppg:staging:17', 'ppg:staging:17:containers', 'ppg:staging:17:extras',
  'ppg:staging:17:extras:containers', 'ppg:staging:17:tarballs',
  'ppg:staging:18', 'ppg:staging:18:containers', 'ppg:staging:18:extras',
  'ppg:staging:18:extras:containers', 'ppg:staging:18:tarballs',
  'ppg:staging:19',
  'ppg:staging:common:containers', 'ppg:staging:common:tools',
  'ppg:staging:common:tools:containers', 'ppg:staging:common:extras:containers',
  'ppg:common:deps', 'ppg:common:deps:tarballs',
  'common:deps:build', 'common:containers:ubi9',
]

describe('splitVersionKey', () => {
  it('splits an extension key and leaves a plain key whole', () => {
    expect(splitVersionKey('17:extras')).toEqual(['17', 'extras'])
    expect(splitVersionKey('17')).toEqual(['17', undefined])
  })
})

describe('deriveVersionKeys', () => {
  it('derives numeric versions first, then common, each with plain key before extensions', () => {
    expect(deriveVersionKeys(STAGING, 'ppg:staging', ABSORBED)).toEqual([
      '19', '18', '18:extras', '17', '17:extras', '16', '16:extras', '16:tde', '15', '14',
      'common', 'common:extras', 'common:tools',
    ])
  })

  it('ignores projects outside the prefix', () => {
    expect(deriveVersionKeys(['ppg:common:deps', 'common:deps:build', 'ppg:staging'], 'ppg:staging', ABSORBED)).toEqual([])
  })

  it('folds absorbed subprojects into the plain key for common too', () => {
    expect(deriveVersionKeys(['ppg:staging:common:containers'], 'ppg:staging', ABSORBED)).toEqual(['common'])
  })

  it('yields an extension when a non-numeric version has a non-absorbed subproject', () => {
    expect(deriveVersionKeys(['ppg:staging:common:tools'], 'ppg:staging', ABSORBED)).toEqual(['common:tools'])
  })

  it('keeps catch-all contexts on plain keys only', () => {
    expect(deriveVersionKeys(['ppg:releases:18:extras', 'ppg:releases:17'], 'ppg:releases', undefined)).toEqual(['18', '17'])
  })

  it('surfaces legacy version-less rows as plain chips (accepted transient)', () => {
    expect(deriveVersionKeys(['ppg:staging:containers', 'ppg:staging:extras:containers'], 'ppg:staging', ABSORBED))
      .toEqual(['containers', 'extras'])
  })
})

describe('matchesVersionKey with common keys', () => {
  it('matches the common base and its absorbed containers', () => {
    expect(matchesVersionKey('ppg:staging:common', 'ppg:staging', 'common', ABSORBED)).toBe(true)
    expect(matchesVersionKey('ppg:staging:common:containers', 'ppg:staging', 'common', ABSORBED)).toBe(true)
    expect(matchesVersionKey('ppg:staging:common:tools', 'ppg:staging', 'common', ABSORBED)).toBe(false)
  })

  it('matches the whole subtree of a common extension', () => {
    expect(matchesVersionKey('ppg:staging:common:tools:containers', 'ppg:staging', 'common:tools', ABSORBED)).toBe(true)
    expect(matchesVersionKey('ppg:staging:common:extras:containers', 'ppg:staging', 'common:extras', ABSORBED)).toBe(true)
    expect(matchesVersionKey('ppg:staging:18', 'ppg:staging', 'common:tools', ABSORBED)).toBe(false)
  })
})

describe('versionKeyProject', () => {
  it('maps keys to their base project and the empty key to the prefix', () => {
    expect(versionKeyProject('ppg:staging', '18')).toBe('ppg:staging:18')
    expect(versionKeyProject('ppg:staging', '16:tde')).toBe('ppg:staging:16:tde')
    expect(versionKeyProject('ppg:staging', 'common')).toBe('ppg:staging:common')
    expect(versionKeyProject('ppg:staging', 'common:tools')).toBe('ppg:staging:common:tools')
    expect(versionKeyProject('ppg:staging', '')).toBe('ppg:staging')
  })
})

describe('selector rows', () => {
  const KEYS = ['19', '18', '18:extras', '16', '16:extras', '16:tde', 'common', 'common:extras', 'common:tools']

  it('lists distinct versions in key order', () => {
    expect(selectorVersions(KEYS)).toEqual(['19', '18', '16', 'common'])
  })

  it('picks the plain key for a version when it exists, else the first extension', () => {
    expect(keyForVersion(KEYS, '18')).toBe('18')
    expect(keyForVersion(['18:extras', '18:tde'], '18')).toBe('18:extras')
    expect(keyForVersion(KEYS, '99')).toBeUndefined()
  })
})
