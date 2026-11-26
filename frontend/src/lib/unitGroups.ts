import type { UnitRow } from '../types/release'

export interface UnitGroup { key: string; title: string; rows: UnitRow[] }

// Unit roots look like ppg:staging:18[:extras]; the third segment is the major.
function major(unit: string): string {
  return unit.split(':')[2] ?? 'common'
}

// Groups units by PostgreSQL major, newest first, "Common" last.
export function groupUnits(units: UnitRow[]): UnitGroup[] {
  const m = new Map<string, UnitRow[]>()
  for (const u of units) {
    const k = major(u.unit)
    m.set(k, [...(m.get(k) ?? []), u])
  }
  const keys = [...m.keys()].sort((a, b) => {
    if (a === 'common') return 1
    if (b === 'common') return -1
    return (parseInt(b, 10) || 0) - (parseInt(a, 10) || 0)
  })
  return keys.map((k) => ({ key: k, title: /^\d+$/.test(k) ? `PostgreSQL ${k}` : 'Common', rows: m.get(k)! }))
}
