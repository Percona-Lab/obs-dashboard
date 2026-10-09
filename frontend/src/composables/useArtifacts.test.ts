import { describe, it, expect } from 'vitest'
import { keyHasPackages } from './useArtifacts'
import { PPG_STAGING_CONTEXT } from '../lib/contexts'
import type { Package } from '../types/api'

function pkg(project: string, extra: Partial<Package> = {}): Package {
  return {
    project,
    name: project.replace(/:/g, '-'),
    rollup_state: 'succeeded',
    ok_targets: 1,
    total_targets: 1,
    targets: [],
    updated_at: '2026-10-09T00:00:00Z',
    ...extra,
  } as Package
}

const CORPUS = [
  pkg('ppg:staging:18'),
  pkg('ppg:staging:18:containers', { is_container: true }),
  pkg('ppg:staging:common:containers', { is_container: true }),
  pkg('ppg:staging:common:tools'),
  pkg('ppg:staging:common:tools:containers', { is_container: true }),
  pkg('ppg:staging:common:extras:containers', { is_container: true }),
]

describe('keyHasPackages', () => {
  it('is true for a version with real packages', () => {
    expect(keyHasPackages(CORPUS, PPG_STAGING_CONTEXT, '18')).toBe(true)
  })

  it('is false for the common base, which only holds images', () => {
    expect(keyHasPackages(CORPUS, PPG_STAGING_CONTEXT, 'common')).toBe(false)
  })

  it('is true for common:tools and false for common:extras', () => {
    expect(keyHasPackages(CORPUS, PPG_STAGING_CONTEXT, 'common:tools')).toBe(true)
    expect(keyHasPackages(CORPUS, PPG_STAGING_CONTEXT, 'common:extras')).toBe(false)
  })

  it('is false when there are no packages at all', () => {
    expect(keyHasPackages([], PPG_STAGING_CONTEXT, '18')).toBe(false)
  })
})
