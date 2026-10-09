import { ref, toValue } from 'vue'
import type { MaybeRef } from 'vue'
import type { Context, Event } from '../types/api'
import { matchesVersionKey, scopedByVersion } from '../lib/versions'
import { isTarballRepo } from '../lib/tarballs'
import { projectInContext } from '../lib/project'

// A tarball build event: in a :tarballs subproject, on an ssl* repo. Structural,
// matching the package-side tarball filter (no backend 'tarball' tag exists).
function isTarballEvent(e: Event): boolean {
  return e.project.endsWith(':tarballs') && isTarballRepo(e.repo ?? '')
}

export function useEvents(apiBase: MaybeRef<string>, version: MaybeRef<string>) {
  const data = ref<Event[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function refresh(opts: { window?: number; from?: string; to?: string } = {}) {
    const base = toValue(apiBase)
    const v = toValue(version)
    loading.value = true
    error.value = null
    try {
      let qs = ''
      if (opts.from && opts.to) {
        qs = `?from=${encodeURIComponent(opts.from)}&to=${encodeURIComponent(opts.to)}`
      } else {
        qs = `?window=${opts.window ?? 60}`
      }
      const res = await fetch(`${base}/${v || 'all'}/events${qs}`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      data.value = await res.json()
    } catch (e) {
      error.value = String(e)
    } finally {
      loading.value = false
    }
  }

  function matchesEventVersion(event: Event, key: string, ctx: Context): boolean {
    if (!key) return true
    // The prefix root (project-level events) and the shared common trees
    // are always shown, as are non-numeric projects in catch-all contexts;
    // everything else scopes to the key.
    if (!scopedByVersion(event.project, ctx.prefix, ctx.allowedSubprojects)) return true
    return matchesVersionKey(event.project, ctx.prefix, key, ctx.allowedSubprojects)
  }

  function filterEvents(tags: string[], version: string, ctx: Context, instances: string[] = []): Event[] {
    return data.value.filter(e => {
      if (!projectInContext(e.project, ctx.prefix)) return false
      if (tags.length > 0 && !tags.every(t => t === 'tarball' ? isTarballEvent(e) : (e.tags ?? []).includes(t))) return false
      if (instances.length > 0 && e.instance && !instances.includes(e.instance)) return false
      return matchesEventVersion(e, version, ctx)
    })
  }

  return { data, loading, error, refresh, filterEvents }
}
