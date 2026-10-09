import { describe, it, expect } from 'vitest'
import { ref } from 'vue'
import { usePackages } from './usePackages'
import { PPG_STAGING_CONTEXT } from '../lib/contexts'
import type { Context, Package } from '../types/api'

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
  pkg('ppg:staging:18:extras'),
  pkg('ppg:staging:common:tools'),
  pkg('ppg:staging:common:tools:containers', { is_container: true }),
  pkg('ppg:common:deps'),
  pkg('common:deps:build'),
]

function projectsFor(key: string): string[] {
  const version = ref(key)
  const { data, rawData } = usePackages('/api/products/ppg/staging', version, ref(PPG_STAGING_CONTEXT))
  rawData.value = CORPUS
  return data.value.map(p => p.project).sort()
}

describe('usePackages version scoping', () => {
  it('hides common projects under a numeric key but keeps shared trees', () => {
    expect(projectsFor('18')).toEqual([
      'common:deps:build', 'ppg:common:deps', 'ppg:staging:18', 'ppg:staging:18:containers',
    ])
  })

  it('shows only the common:tools subtree plus shared trees under common:tools', () => {
    expect(projectsFor('common:tools')).toEqual([
      'common:deps:build', 'ppg:common:deps', 'ppg:staging:common:tools', 'ppg:staging:common:tools:containers',
    ])
  })

  it('shows everything under the empty key', () => {
    expect(projectsFor('')).toHaveLength(CORPUS.length)
  })
})

describe('usePackages catch-all (board PR) contexts', () => {
  it('shows every PR package under a numeric key', () => {
    const prCtx = { label: 'PR #127', apiBase: '/api/pr/pr-127', prefix: 'PR:pr-127' } as Context
    const { data, rawData } = usePackages('/api/pr/pr-127', ref('18'), ref(prCtx))
    rawData.value = [
      pkg('PR:pr-127:ppg:staging:17'),
      pkg('PR:pr-127:ppg:devel:18'),
      pkg('PR:pr-127:common:deps'),
    ]
    expect(data.value.map(p => p.project).sort()).toEqual([
      'PR:pr-127:common:deps', 'PR:pr-127:ppg:devel:18', 'PR:pr-127:ppg:staging:17',
    ])
  })
})
