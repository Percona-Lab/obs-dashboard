import type { CveImageRef, CveKind, CveRow, CveStaging } from '../types/cveReport'

export const KIND_LABEL: Record<CveKind, string> = {
  go_runtime: 'Go runtime',
  bundled_library: 'bundled library',
  obs_package: 'OBS package',
  base_os: 'base OS',
  unclassified: 'not classified',
}

export function allRows(s: CveStaging): CveRow[] {
  return [...s.ours, ...s.base_os, ...s.unclassified]
}

export function allSeenKeys(s: CveStaging): string[] {
  const keys = new Set<string>()
  for (const row of allRows(s)) for (const e of row.entries) keys.add(e.seen_key)
  return [...keys]
}

export function rowIsNew(row: CveRow, isNew: (k: string) => boolean): boolean {
  return row.entries.some(e => isNew(e.seen_key))
}

export function countNew(s: CveStaging, isNew: (k: string) => boolean): number {
  return allSeenKeys(s).filter(isNew).length
}

// Rows with new entries first; Array.prototype.sort is stable, so the server
// order is kept within each group.
export function sortRows(rows: CveRow[], isNew: (k: string) => boolean): CveRow[] {
  const flagged = rows.map(row => ({ row, fresh: rowIsNew(row, isNew) }))
  return flagged.sort((a, b) => Number(b.fresh) - Number(a.fresh)).map(f => f.row)
}

// "6 images · ubi8, ubi9"
export function imagesSummary(images: CveImageRef[]): string {
  const repos = [...new Set(images.map(i => i.repo))].sort()
  const n = images.length
  return `${n} ${n === 1 ? 'image' : 'images'} · ${repos.join(', ')}`
}
