// JSON shapes emitted by /api/release/* (snake_case, mirrors backend/internal/release).
// The backend never lists a release at step 'done': finished releases leave the snapshot.
export type ReleaseStep = 'cutting' | 'changelog' | 'sync' | 'qa' | 'releasing'
export type RunState = 'queued' | 'running' | 'success' | 'failure'
// The cut can also be held by GitHub until a person approves the workflow run. It is not a failure.
export type CutState = RunState | 'approval'

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
  /** Each release project of the populate report with its outcome on this instance; empty until the job reported. `skipped`: the instance holds nothing of it. */
  units: { project: string; outcome: 'success' | 'failure' | 'skipped' }[]
  /** The job succeeded and every entry of its report was skipped. */
  nothing_to_sync: boolean
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
  cut: { state: CutState; url: string } | null
  sync_state: string
  sync_url: string // the run the sync state was read from; '' while none
  /** Why the sync failed when no single instance did: a unit populated on no instance. */
  sync_error: string
  sync: InstanceCard[]
  removals: { project: string; item: string }[]
  // skipped: a user chose to release this head of the PR without QA; Release is then available whatever the state.
  // url: the QA run ('' while none). error: why QA could not run, set with state 'failure'.
  qa: { state: 'waiting' | 'running' | 'finished' | 'failure'; lanes: Lane[]; url: string; error: string; skipped?: boolean; skipped_by?: string } | null
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
  /** Why the release PR could not be opened, while the release waits for it; '' otherwise. */
  pr_error: string
  /** Unit roots asked for, while `units` is still empty (the cut has not produced them yet). */
  cut_units: string[]
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

export interface HistoryPage {
  rows: HistoryRow[]
  more: boolean // a further page may exist, whatever the number of rows on this one
}

export type ReleaseAction =
  | 'approve-changelog' | 'retry-sync' | 'approve-qa' | 'retry-qa' | 'skip-qa' | 'recut' | 'release' | 'retry-release' | 'abandon'
