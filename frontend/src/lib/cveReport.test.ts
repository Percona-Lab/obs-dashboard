import { describe, expect, it } from 'vitest'
import type { CveEntry, CveImageRef, CveRow, CveStaging } from '../types/cveReport'
import { allRows, allSeenKeys, countNew, imagesSummary, rowIsNew, sortRows } from './cveReport'

const entry = (seen_key: string): CveEntry =>
  ({ id: 'CVE-X', pkg: 'p', severity: 'HIGH', title: 't', seen_key })
const row = (key: string, keys: string[]): CveRow =>
  ({ key, name: key, kind: 'obs_package', worst: 'HIGH', cves: keys.length, fixes: [], entries: keys.map(entry), images: [] })
const staging = (ours: CveRow[], base_os: CveRow[] = [], unclassified: CveRow[] = []): CveStaging =>
  ({ ours, base_os, unclassified, cves: 0, images: 0 })
const img = (repo: string): CveImageRef => ({ project: 'p', package: 'k', repo, arches: [] })

describe('cveReport helpers', () => {
  it('allRows concatenates ours, base_os, unclassified', () => {
    const a = row('ours|a', []), b = row('base_os|b', []), c = row('unclassified|c', [])
    expect(allRows(staging([a], [b], [c]))).toEqual([a, b, c])
  })

  it('allSeenKeys is distinct', () => {
    const s = staging([row('ours|a', ['k1', 'k2'])], [row('base_os|b', ['k2'])])
    expect(allSeenKeys(s)).toEqual(['k1', 'k2'])
  })

  it('countNew counts a shared key once', () => {
    const s = staging([row('ours|a', ['k1'])], [row('base_os|b', ['k1', 'k2'])])
    expect(countNew(s, () => true)).toBe(2)
    expect(countNew(s, k => k === 'k1')).toBe(1)
  })

  it('rowIsNew is false when all entries are seen', () => {
    const r = row('ours|a', ['k1', 'k2'])
    expect(rowIsNew(r, () => false)).toBe(false)
    expect(rowIsNew(r, k => k === 'k2')).toBe(true)
  })

  it('sortRows puts new rows first and keeps the rest in order', () => {
    const a = row('a', ['ka']), b = row('b', ['kb']), c = row('c', ['kc']), d = row('d', ['kd'])
    const isNew = (k: string) => k === 'kc' || k === 'kd'
    expect(sortRows([a, b, c, d], isNew).map(r => r.key)).toEqual(['c', 'd', 'a', 'b'])
    expect(sortRows([a, b], () => false).map(r => r.key)).toEqual(['a', 'b'])
  })

  it('imagesSummary', () => {
    expect(imagesSummary([img('ubi9'), img('ubi9'), img('ubi8')])).toBe('3 images · ubi8, ubi9')
    expect(imagesSummary([img('ubi9')])).toBe('1 image · ubi9')
  })
})
