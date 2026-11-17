export type BuildState = 'succeeded' | 'failed' | 'unresolvable' | 'broken' | 'blocked' | 'scheduled' | 'building' | 'finished' | 'published'
export type EventType = 'triggered' | 'started' | 'succeeded' | 'failed' | 'unresolvable' | 'broken' | 'blocked' | 'published' | 'created' | 'deleted' | 'build_started' | 'build_finished' | 'version_change' | 'updated' | 'cve_scan_started' | 'cve_scan_finished' | 'cve_scan_failed'

export interface Context {
  label: string
  apiBase: string  // e.g. "/api/products/ppg/staging" or "/api/pr/pr-92"
  prefix: string   // e.g. "ppg:staging" or "PR:pr-92"
  /** Direct subprojects of prefix:ver absorbed into the plain version entry
   *  (e.g. "containers"); all others become <ver>:<sub> version-extension
   *  entries. Undefined = catch-all (PR/Releases contexts). */
  allowedSubprojects?: string[]
}

export interface Trigger {
  what: string
  kind: string
  at: string // ISO 8601
}

export interface Target {
  repo: string
  arch: string
  state: BuildState
  started_at?: string
  details?: string
  blocked_by?: string
  build_reason?: string
  build_reason_packages?: string[]
  published?: boolean
  instance?: string
}

export interface InstanceHealth {
  ok: boolean
  last_success?: string
  last_error?: string
  last_error_at?: string
  consecutive_failures: number
  mq_connected: boolean
}

export interface ObsInstance {
  name: string
  slug: string
  root: string        // prefix for logical names; '' = names are already instance names
  web_url: string
  download_url: string
  registry: string
  health: InstanceHealth
}

export interface CveFinding {
  id: string
  pkg: string
  installed: string
  fixed: string
  severity: 'HIGH' | 'CRITICAL'
  title: string
  scanners?: string[]
}

export interface CveScan {
  repo: string
  arch: string
  image_ref: string
  scanned_at: string
  critical_count: number
  high_count: number
  cve_since?: string    // ISO timestamp — present when repo+arch currently has CVEs
  clean_since?: string  // ISO timestamp — present when repo+arch is currently clean
  findings?: CveFinding[]
}

export interface Package {
  project: string
  name: string
  tags?: string[]
  is_release?: boolean
  rollup_state: BuildState
  ok_targets: number
  total_targets: number
  is_container?: boolean
  settled?: boolean // nothing awaits publication: all targets published or in never-publishing repos
  version?: string
  trigger?: Trigger // optional
  targets: Target[]
  updated_at: string // ISO 8601
  state_changed_at?: string // ISO 8601; absent when NULL
  container_tags?: string[]
  cve_scans?: CveScan[]
}

export interface PRGroup {
  pr: string
  rollup_state: BuildState
  packages: Package[]
}

export interface Event {
  id: string
  type: EventType
  tags?: string[]
  project: string
  package: string
  repo?: string // optional
  arch?: string // optional
  what: string
  why: string
  version?: string
  url: string
  at: string // ISO 8601
  instance?: string
}
