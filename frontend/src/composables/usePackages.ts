import { ref, computed, toValue } from 'vue'
import type { MaybeRef, ComputedRef } from 'vue'
import type { Context, Package } from '../types/api'
import { deriveVersionKeys, matchesVersionKey } from '../lib/versions'
import { isTarballRepo } from '../lib/tarballs'
import { isUnderPrefix } from '../lib/project'

// A tarball package: in a :tarballs subproject, built against an ssl* repo.
// Detection is structural (no backend tag), matching the board card + Artifacts.
function isTarballPkg(p: Package): boolean {
  return p.project.endsWith(':tarballs') && (p.targets ?? []).some(t => isTarballRepo(t.repo))
}

const SEVERITY: Record<string, number> = {
  broken: 5,
  failed: 4,
  unresolvable: 3,
  blocked: 2,
  building: 1,
  finished: 1,
  scheduled: 1,
  succeeded: 0,
  published: -1,
}

export function usePackages(
  apiBase: MaybeRef<string>,
  version: MaybeRef<string>,
  context: MaybeRef<Context>,
) {
  const data = ref<Package[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function refresh() {
    const base = toValue(apiBase)
    loading.value = true
    error.value = null
    try {
      // Always fetch all versions so availableVersions stays complete.
      // Client-side matchesVersion handles per-version filtering in `sorted`.
      const res = await fetch(`${base}/_/packages`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      data.value = await res.json()
    } catch (e) {
      error.value = String(e)
    } finally {
      loading.value = false
    }
  }

  // availableVersions: version keys (plain + extensions) derived from the
  // fetched corpus at the context's prefix depth.
  const availableVersions: ComputedRef<string[]> = computed(() => {
    const ctx = toValue(context)
    return deriveVersionKeys(
      data.value.map(p => p.project),
      ctx.prefix,
      ctx.allowedSubprojects,
    )
  })

  const sorted = computed(() => {
    const ver = toValue(version)
    const ctx = toValue(context)
    return [...data.value]
      .filter(pkg => {
        if (pkg.is_release) return false
        if (!ver) return true
        // Shared common trees live outside the context prefix and are
        // always shown; everything under the prefix scopes to the key.
        if (!isUnderPrefix(pkg.project, ctx.prefix)) return true
        return matchesVersionKey(pkg.project, ctx.prefix, ver, ctx.allowedSubprojects)
      })
      .sort((a, b) => (SEVERITY[b.rollup_state] ?? 0) - (SEVERITY[a.rollup_state] ?? 0))
  })

  function filterByTags(tags: string[], instances: string[] = []): Package[] {
    return sorted.value.filter(p =>
      (tags.length === 0 || tags.every(t => t === 'tarball' ? isTarballPkg(p) : (p.tags ?? []).includes(t))) &&
      (instances.length === 0 || (p.targets ?? []).some(t => !!t.instance && instances.includes(t.instance))))
  }

  return { data: sorted, rawData: data, availableVersions, loading, error, refresh, filterByTags }
}
