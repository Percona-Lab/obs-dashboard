import { describe, it, expect } from 'vitest'
import { ref } from 'vue'
import { useEvents } from './useEvents'
import { PPG_STAGING_CONTEXT } from '../lib/contexts'
import type { Context, Event } from '../types/api'

function ev(project: string): Event {
  return { project, package: 'p', tags: [] } as unknown as Event
}

const CORPUS = [
  ev('ppg:staging'),
  ev('ppg:staging:18'),
  ev('ppg:staging:18:extras'),
  ev('ppg:staging:common:tools'),
  ev('ppg:staging:common:tools:containers'),
  ev('ppg:common:deps'),
  ev('common:deps:build'),
]

function projectsFor(key: string): string[] {
  const { data, filterEvents } = useEvents('/api/products/ppg/staging', ref(key))
  data.value = CORPUS
  return filterEvents([], key, PPG_STAGING_CONTEXT).map(e => e.project).sort()
}

describe('useEvents version scoping', () => {
  it('hides common projects under a numeric key but keeps the root and shared trees', () => {
    expect(projectsFor('18')).toEqual([
      'common:deps:build', 'ppg:common:deps', 'ppg:staging', 'ppg:staging:18',
    ])
  })

  it('shows the common:tools subtree under common:tools', () => {
    expect(projectsFor('common:tools')).toEqual([
      'common:deps:build', 'ppg:common:deps', 'ppg:staging', 'ppg:staging:common:tools', 'ppg:staging:common:tools:containers',
    ])
  })

  it('shows everything under the empty key', () => {
    expect(projectsFor('')).toHaveLength(CORPUS.length)
  })
})

describe('useEvents catch-all (board PR) contexts', () => {
  it('shows every PR event under a numeric key', () => {
    const prCtx = { label: 'PR #127', apiBase: '/api/pr/pr-127', prefix: 'PR:pr-127' } as Context
    const { data, filterEvents } = useEvents('/api/pr/pr-127', ref('18'))
    data.value = [
      ev('PR:pr-127:ppg:staging:17'),
      ev('PR:pr-127:ppg:devel:18'),
      ev('PR:pr-127:common:deps'),
    ]
    expect(filterEvents([], '18', prCtx).map(e => e.project).sort()).toEqual([
      'PR:pr-127:common:deps', 'PR:pr-127:ppg:devel:18', 'PR:pr-127:ppg:staging:17',
    ])
  })
})
