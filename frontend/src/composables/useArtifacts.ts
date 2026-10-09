import { computed, toValue } from 'vue'
import type { MaybeRef } from 'vue'
import type { Package, Target, CveScan, Context } from '../types/api'
import { matchesVersionKey } from '../lib/versions'
import { isTarballTarget } from '../lib/tarballs'
import { useInstances } from './useInstances'
import { registryRef } from '../lib/instances'

export interface RepoInfo {
  obs: string
  name: string
  type: 'rpm' | 'deb'
}

export interface ArtifactBinary {
  filename: string
  size?: number
  mtime?: number
  built_at?: string
}

export interface PackageRow {
  project: string
  name: string
  version: string
  tags: string[]
  state: string
  published: boolean
  repo: RepoInfo
  arch: string
  binaries?: ArtifactBinary[]
  builtAt?: string
  mtime?: number
  isRebuilding?: boolean
  instance?: string
}

export interface ContainerImage {
  id: string
  project: string
  imageName: string
  baseOs: string
  registry: string
  tags: string[]
  pullCmd: string
  rollupState: string
  published: boolean
  mtime?: number
  builtAt?: string
  isRebuilding?: boolean
  cveScans: CveScan[]
  instance?: string
}

export interface Tarball {
  id: string          // `${project}/${name}/${repo}`
  project: string
  name: string
  version: string     // pkg.version, e.g. "18.6-3"
  repo: string        // ssl1.1 | ssl3 | ssl3.5
  arches: string[]    // distinct arches for this (pkg, repo)
  rollupState: string
  published: boolean
  builtAt: string     // latest target started_at for this repo ("" if none)
  instance?: string
}

// Red Hat UBI base-image repos of any major version (ubi8, ubi9, ubi10, …),
// so a new UBI release needs no code change. Mirrors the backend's ubiRepoRe.
const UBI_REPO = /^ubi(\d+)$/

function baseOsFromRepo(repo?: string): string {
  const ubi = repo?.match(UBI_REPO)
  if (ubi) return `UBI ${ubi[1]}`
  switch (repo) {
    case 'noble': return 'Ubuntu 24.04 Noble'
    case 'bookworm': return 'Debian 12 Bookworm'
    default: return ''
  }
}

const UBI_LABEL = /^UBI (\d+)$/

// Display order for base-OS labels: UBI labels first by numeric version
// (UBI 8, UBI 9, UBI 10), then every other label in plain string order.
// Mirrors the backend's baseOSLess so release and live views agree.
export function compareBaseOs(a: string, b: string): number {
  const ua = a.match(UBI_LABEL)
  const ub = b.match(UBI_LABEL)
  if (ua && ub) return Number(ua[1]) - Number(ub[1])
  if (ua) return -1
  if (ub) return 1
  return a < b ? -1 : a > b ? 1 : 0
}

export function deriveBaseOs(project: string, repo?: string): string {
  const fromRepo = baseOsFromRepo(repo)
  if (fromRepo) return fromRepo
  const parts = project.split(':')
  const containerIdx = parts.lastIndexOf('containers')
  if (containerIdx >= 0 && containerIdx < parts.length - 1) {
    const suffix = parts[containerIdx + 1]
    return baseOsFromRepo(suffix) || suffix
  }
  return project
}

export function distroGroup(repo: RepoInfo): string {
  const name = repo.name.toLowerCase()
  if (/rhel|centos|rocky|oracle|ubi/.test(name)) return 'RHEL'
  if (/opensuse|suse/.test(name)) return 'openSUSE'
  if (/ubuntu/.test(name)) return 'Ubuntu'
  if (/debian/.test(name)) return 'Debian'
  return 'Other'
}

/** Does any non-container package of the corpus match the key in this
 *  context? Used by the Artifacts panel to leave an empty Packages tab
 *  for keys that only hold images (e.g. the common base). */
export function keyHasPackages(packages: Package[], ctx: Context, key: string): boolean {
  if (!key) return false
  return packages.some(p =>
    p.is_container !== true && matchesVersionKey(p.project, ctx.prefix, key, ctx.allowedSubprojects),
  )
}

export function useArtifacts(
  packages: MaybeRef<Package[]>,
  version: MaybeRef<string>,
  selectedRepo: MaybeRef<RepoInfo | null>,
  artArch: MaybeRef<string>,
  context: MaybeRef<Context>,
) {
  const { instanceFor } = useInstances()
  // matchesProject: does an OBS project belong to this context at the
  // selected version key? Plain keys own the version root + absorbed
  // subprojects; extension keys ("17:extras") own that subtree; contexts
  // without allowedSubprojects (PR, Releases) keep the historical catch-all.
  const matchesProject = (project: string, ver: string): boolean => {
    const ctx = toValue(context)
    return matchesVersionKey(project, ctx.prefix, ver, ctx.allowedSubprojects)
  }

  const packageRows = computed<PackageRow[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)
    const repo = toValue(selectedRepo)
    const arch = toValue(artArch)

    if (!repo) return []

    const rows: PackageRow[] = []
    for (const pkg of pkgs) {
      // Confirmed container images (is_container: true) are excluded — they belong
      // in the Container Images tab, not here.
      if (!matchesProject(pkg.project, ver) || pkg.is_container === true) continue

      const target = pkg.targets?.find(
        (t: Target) => t.repo === repo.obs && t.arch === arch,
      )
      if (!target) continue

      rows.push({
        project: pkg.project,
        name: pkg.name,
        version: pkg.version ?? '',
        tags: pkg.tags ?? [],
        state: target.state ?? '',
        published: target.published === true,
        repo,
        arch,
        instance: target.instance,
      })
    }
    return rows
  })

  const containerImages = computed<ContainerImage[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)

    return pkgs
      .filter(pkg =>
        pkg.is_container === true &&
        matchesProject(pkg.project, ver)
      )
      .flatMap(pkg => {
        const tags = pkg.container_tags ?? []
        const pullTag = tags[tags.length - 1] ?? ''
        // Distinct build repos = base images. Old-layout containers built against
        // a single "images" repo (base OS in the project); new-layout containers
        // build against ubi8/ubi9/… — one row per repo.
        const targets = pkg.targets ?? []
        const repos = [...new Set(targets.map((t: Target) => t.repo))]
        const effectiveRepos = repos.length > 0 ? repos : ['images']
        return effectiveRepos.map(repo => {
          const baseOs = deriveBaseOs(pkg.project, repo)
          const inst = instanceFor(targets.find(t => t.repo === repo)?.instance)
          const registry = registryRef(inst, pkg.project, repo, pkg.name)
          const pullCmd = pullTag
            ? `docker pull ${registry}:${pullTag}`
            : `docker pull ${registry}`
          const published = targets.some((t: Target) => t.repo === repo && t.published === true)
          return {
            id: pkg.project + '/' + pkg.name + '/' + repo,
            project: pkg.project,
            imageName: pkg.name,
            baseOs,
            registry,
            tags,
            pullCmd,
            rollupState: pkg.rollup_state ?? '',
            published,
            cveScans: (pkg.cve_scans ?? []).filter((s: CveScan) => s.repo === repo),
            instance: targets.find(t => t.repo === repo)?.instance,
          }
        })
      })
  })

  // Tarballs: PostgreSQL distribution tarballs built against ssl* repos inside
  // a :tarballs subproject. Detection is structural (isTarballTarget) — no
  // backend flag. One card per (package, ssl-repo); built time/state come from
  // the package targets directly, so no metadata/CVE enrichment is involved.
  const tarballs = computed<Tarball[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)

    return pkgs
      .filter(pkg =>
        matchesProject(pkg.project, ver) &&
        pkg.project.endsWith(':tarballs')
      )
      .flatMap(pkg => {
        const targets = pkg.targets ?? []
        const repos = [...new Set(
          targets
            .filter((t: Target) => isTarballTarget(pkg.project, t.repo))
            .map((t: Target) => t.repo),
        )]
        return repos.map(repo => {
          const repoTargets = targets.filter((t: Target) => t.repo === repo)
          const arches = [...new Set(repoTargets.map((t: Target) => t.arch))]
          const builtAt = repoTargets.reduce(
            (latest: string, t: Target) => {
              const at = t.started_at ?? ''
              return at > latest ? at : latest
            },
            '',
          )
          return {
            id: pkg.project + '/' + pkg.name + '/' + repo,
            project: pkg.project,
            name: pkg.name,
            version: pkg.version ?? '',
            repo,
            arches,
            rollupState: pkg.rollup_state ?? '',
            published: repoTargets.some((t: Target) => t.published === true),
            builtAt,
            instance: repoTargets.find((t: Target) => t.instance)?.instance,
          }
        })
      })
  })

  return { packageRows, containerImages, tarballs }
}
