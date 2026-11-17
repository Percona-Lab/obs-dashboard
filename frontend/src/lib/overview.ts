// Shared helpers for the Overview panel components (StatCard consumers,
// RebuildBarChart, CveExposureTable). Kept in one place so the age-color
// escalation rule and project accent assignment can't drift between them.

import { isPRProject } from './project'

export const PROJECT_ACCENTS = ['#6E3FF3', '#2A78D4', '#1F9D55', '#E08A00', '#B0203A']

// Accent for a project by its position in the NAME-sorted project list (not
// snapshot ordering, which is rebuild-sorted and would shuffle colours between
// windows/refetches).
export function projectAccent(sortedProjects: string[], project: string): string {
  const idx = sortedProjects.indexOf(project)
  return PROJECT_ACCENTS[Math.max(0, idx) % PROJECT_ACCENTS.length]
}

export function ageColor(days: number): string {
  if (days >= 45) return 'var(--crit)'
  if (days >= 21) return 'var(--high)'
  return 'var(--text-secondary)'
}

export function ageLabel(days: number): string {
  return days === 0 ? '—' : `${days}d`
}

// "Oldest open" cell rendering. The backend floors the age to whole days, so a
// CVE opened under 24h ago arrives as 0 — but that is NOT the same as "nothing
// open". Because UpsertCveScan always stamps cve_since when a scan records
// vulns, `days === 0` while there ARE open CVEs unambiguously means "less than
// a day tracked", shown as "<1d". Only genuinely-clean rows (no open CVEs) show "—".
export function oldestOpenLabel(days: number, hasOpen: boolean): string {
  if (!hasOpen) return '—'
  return days < 1 ? '<1d' : `${days}d`
}

// Muted when nothing is open; otherwise the age-escalation color (a "<1d" age
// falls in the neutral secondary band via ageColor(0)).
export function oldestOpenColor(days: number, hasOpen: boolean): string {
  return hasOpen ? ageColor(days) : 'var(--text-muted)'
}

// Category grouping for the overview tables: logical projects are grouped
// into Devel / Staging / Releases / PRs sections, in that order.
export type ProjectCategory = 'Devel' | 'Staging' | 'Releases' | 'PRs'

export const CATEGORY_ORDER: ProjectCategory[] = ['Devel', 'Staging', 'Releases', 'PRs']

export function categoryOf(project: string): ProjectCategory {
  if (isPRProject(project)) return 'PRs'
  if (project.includes(':releases:')) return 'Releases'
  if (project.includes(':staging:')) return 'Staging'
  return 'Devel'
}

// Splits items into non-empty category sections, preserving item order.
export function groupByCategory<T>(
  items: T[],
  projectOf: (item: T) => string,
): { category: ProjectCategory; items: T[] }[] {
  return CATEGORY_ORDER
    .map(category => ({
      category,
      items: items.filter(i => categoryOf(projectOf(i)) === category),
    }))
    .filter(g => g.items.length > 0)
}

// Formats a CVE fix duration given in hours: "<1h", "6h", "2d", "2d 4h";
// "—" when there is no data (0 or negative).
export function formatFixHours(hours: number): string {
  if (!(hours > 0)) return '—'
  if (hours < 1) return '<1h'
  const total = Math.round(hours)
  if (total < 24) return `${total}h`
  const d = Math.floor(total / 24)
  const h = total % 24
  return h === 0 ? `${d}d` : `${d}d ${h}h`
}
