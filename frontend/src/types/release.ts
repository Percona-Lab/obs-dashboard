// JSON shapes emitted by /api/release/* (snake_case, mirrors backend/internal/release).
// The backend never lists a release at step 'done': finished releases leave the snapshot.
export type ReleaseStep = 'cutting' | 'changelog' | 'sync' | 'qa' | 'releasing'
export type RunState = 'queued' | 'running' | 'success' | 'failure'

export interface BlockedReason {
  reason: 'in_flight' | 'not_built' | 'unchanged'
  text: string
  packages?: string[] // omitted when empty
  release_id?: string // omitted when empty
}

export interface UnitRow {
  unit: string
  members: string[]
  release_project: string
  release_dir: string
  last_release: string
  built: number
  total: number
  tickable: boolean
  blocked?: BlockedReason
}

export interface InstanceCard {
  instance: string
  state: RunState
  url: string
  started_at: string | null
  finished_at: string | null
  cdn: string
  failed_units: string[]
  errors: string[]
}

export interface Lane {
  name: string
  unit: string
  jenkins_url: string
  url: string
  state: RunState
}

export interface UnitState {
  unit: string
  release_project: string
  release_dir: string
  release_id: string
  tag: string
  changelog: string
  generated: string | null
}

export interface ReleaseState {
  id: string
  branch: string
  pr: number
  pr_url: string
  head_sha: string
  step: ReleaseStep
  dashboard: boolean
  units: UnitState[]
  cut: { state: RunState; url: string } | null
  sync_state: string
  sync: InstanceCard[]
  removals: { project: string; item: string }[]
  qa: { state: 'waiting' | 'running' | 'finished'; lanes: Lane[] } | null
  final: {
    tags: string[]
    instances: InstanceCard[]
    github_releases: string
    cleanup: string
    state: RunState
    url: string
  } | null
  started_by: string
  started_at: string | null
  actions: { at: string; email: string; action: string }[]
}

export interface Snapshot {
  releases: ReleaseState[]
  stale: boolean
  error: string
  refreshed_at: string
}

export interface HistoryRow {
  tag: string
  release_id: string
  unit: string
  released_at: string
  release_url: string
  pr_url: string
  notes: string
  released_by: string
  failed_lanes: string[]
}

export type ReleaseAction =
  | 'approve-changelog' | 'retry-sync' | 'approve-qa' | 'recut' | 'release' | 'retry-release' | 'abandon'
