<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import type { Tarball } from '../composables/useArtifacts'
import { tarballRepoOrder } from '../lib/tarballs'
import { formatArtifactTime } from '../lib/cve'
import { useInstances } from '../composables/useInstances'
import { downloadBase } from '../lib/instances'
import InstanceBadge from './InstanceBadge.vue'

const props = defineProps<{
  tarballs: Tarball[]
  loading?: boolean
}>()

const { instanceFor } = useInstances()

// Published tarball filenames, resolved from the OBS binary list rather than
// reconstructed from the package version, which does not reliably match the
// published name (PG-2860). Keyed per build (builtAt) so a rebuild re-resolves.
const filenames = reactive<Record<string, string | null>>({})

function fileKey(t: Tarball, arch: string): string {
  return `${t.id}/${arch}/${t.builtAt}`
}

async function resolveFilename(t: Tarball, arch: string, key: string) {
  filenames[key] = null
  try {
    const params = new URLSearchParams({ project: t.project, repo: t.repo, arch, package: t.name })
    const res = await fetch(`/api/binaries?${params}`)
    if (!res.ok) return
    const data = await res.json() as { binaries: string[] }
    filenames[key] = data.binaries.find(f => f.endsWith('.tar.gz')) ?? null
  } catch {
    // unresolved: the link falls back to the repo directory
  }
}

watch(() => props.tarballs, tarballs => {
  for (const t of tarballs) {
    for (const arch of t.arches) {
      const key = fileKey(t, arch)
      if (!(key in filenames)) void resolveFilename(t, arch, key)
    }
  }
}, { immediate: true })

// Direct file link once the filename is known; until then (or if it cannot be
// resolved) the repo directory listing, which always exists.
function downloadHref(t: Tarball, arch: string): string | undefined {
  const base = downloadBase(instanceFor(t.instance), t.project, t.repo)
  if (!base) return undefined
  return base + (filenames[fileKey(t, arch)] ?? '')
}

// Group tarballs by ssl repo, sorted ssl1.1 < ssl3 < ssl3.5.
const groups = computed(() => {
  const map = new Map<string, Tarball[]>()
  for (const t of props.tarballs) {
    const list = map.get(t.repo) ?? []
    list.push(t)
    map.set(t.repo, list)
  }
  return Array.from(map.entries())
    .sort((a, b) => tarballRepoOrder(a[0], b[0]))
    .map(([repo, tarballs]) => ({ repo, tarballs }))
})

const STATE_LABELS: Record<string, string> = {
  building: 'Rebuilding',
  finished: 'Rebuilding',
  scheduled: 'Waiting to rebuild',
}

function rebuildBadge(t: Tarball): { label: string; cls: string } | null {
  const label = STATE_LABELS[t.rollupState]
  if (!label) return null
  return {
    label,
    cls: t.rollupState === 'scheduled' ? 'waiting' : 'building',
  }
}
</script>

<template>
  <div class="flex flex-col gap-6 p-4">
    <div
      v-if="loading"
      class="tarballs-loading flex flex-col items-center justify-center py-12 gap-3 text-text-muted"
      :class="{ compact: groups.length > 0 }"
    >
      <div class="spinner"></div>
      <span class="text-[13px] text-text-muted">Fetching tarballs…</span>
    </div>

    <template v-if="groups.length > 0">
      <div v-for="group in groups" :key="group.repo" class="flex flex-col gap-[14px]">
        <div class="flex items-center justify-between gap-4 px-[14px] py-[11px] bg-bg-card-2 border border-border border-l-4 border-l-brand-purple rounded-lg">
          <div class="flex flex-col gap-0.5 min-w-0">
            <h3 class="m-0 text-[15px] [font-weight:750] leading-[1.2] text-text-primary">{{ group.repo }}</h3>
            <span class="text-[11.5px] text-text-muted">OpenSSL {{ group.repo.replace(/^ssl/i, '') }} build</span>
          </div>
          <span class="shrink-0 px-2 py-[3px] rounded-[6px] bg-bg-card border border-border text-text-secondary text-[11px] font-bold whitespace-nowrap">
            {{ group.tarballs.length }} tarball{{ group.tarballs.length !== 1 ? 's' : '' }}
          </span>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-[repeat(auto-fill,minmax(340px,1fr))] gap-4">
          <div
            v-for="tarball in group.tarballs"
            :key="tarball.id"
            class="bg-bg-card rounded-[12px] overflow-hidden flex flex-col [transition:opacity_0.15s_ease,filter_0.15s_ease]"
            :class="{ 'opacity-[0.48] grayscale-[0.85]': loading }"
          >
            <!-- Card header -->
            <div class="flex items-center justify-between px-[18px] py-[14px] border-b border-border">
              <div class="flex items-center gap-[10px]">
                <div class="flex items-center justify-center w-9 h-9 rounded-lg bg-info-tint text-info shrink-0">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none"
                       stroke="currentColor" stroke-width="1.8"
                       stroke-linecap="round" stroke-linejoin="round">
                    <path d="M21 8v13H3V8"/>
                    <path d="M1 3h22v5H1z"/>
                    <path d="M10 12h4"/>
                  </svg>
                </div>
                <span class="text-[14px] font-bold">{{ tarball.name }}</span>
                <InstanceBadge :slug="tarball.instance" />
              </div>
              <template v-for="badge in [rebuildBadge(tarball)]" :key="'rebuild-badge'">
                <span v-if="badge" class="status-badge" :class="badge.cls">{{ badge.label }}</span>
              </template>
            </div>

            <!-- Tarball label + repo -->
            <div class="bg-bg-card-2 px-[18px] py-[10px] border-b border-border">
              <div class="section-label">ARTIFACT</div>
              <span class="inline-flex items-center gap-[6px] mt-1 text-[12px] text-text-secondary font-semibold">
                <span class="inline-flex items-center px-2 py-[2px] rounded-[6px] bg-brand-purple-tint text-brand-purple text-[11px] font-bold">Tarball</span>
                <code class="font-mono">{{ tarball.repo }}</code>
              </span>
            </div>

            <!-- Version -->
            <div v-if="tarball.version" class="px-[18px] py-[10px] border-b border-border">
              <div class="section-label">VERSION</div>
              <span class="block mt-1 text-[12px] text-text-secondary">{{ tarball.version }}</span>
            </div>

            <!-- Architectures -->
            <div class="px-[18px] py-3 border-b border-border">
              <div class="section-label">ARCHITECTURES</div>
              <div class="flex flex-wrap gap-[6px] mt-2" v-if="tarball.arches.length > 0">
                <code
                  v-for="arch in tarball.arches"
                  :key="arch"
                  class="inline-flex items-center font-mono text-[11px] px-2 py-[3px] rounded-[6px] bg-bg-muted text-text-secondary font-semibold"
                >{{ arch }}</code>
              </div>
              <span v-else class="block mt-[6px] text-[12px] text-text-muted">No architectures yet</span>
            </div>

            <!-- Download (per arch) -->
            <div v-if="tarball.arches.length > 0" class="px-[18px] py-3 border-b border-border">
              <div class="section-label">DOWNLOAD</div>
              <div class="flex flex-wrap gap-[6px] mt-2">
                <a
                  v-for="arch in tarball.arches"
                  :key="arch"
                  :href="downloadHref(tarball, arch)"
                  target="_blank"
                  rel="noopener"
                  class="inline-flex items-center gap-[5px] font-mono text-[11px] px-[9px] py-[4px] rounded-[6px] bg-brand-purple-tint text-brand-purple font-bold no-underline"
                >↓ {{ arch }} · .tar.gz</a>
              </div>
            </div>

            <!-- Built -->
            <div v-if="tarball.builtAt" class="px-[18px] py-[10px] flex-1">
              <div class="section-label">BUILT</div>
              <span class="block mt-1 text-[12px] text-text-secondary">{{ formatArtifactTime(tarball.builtAt) }}</span>
            </div>
          </div>
        </div>
      </div>
    </template>

    <div v-else-if="!loading" class="text-center p-12 text-text-muted text-[14px]">
      No tarballs for this version.
    </div>
  </div>
</template>

<style scoped>
@keyframes spin {
  to { transform: rotate(360deg); }
}

/* Loading states */
.tarballs-loading.compact {
  flex-direction: row;
  justify-content: flex-start;
  padding: 12px 16px;
  gap: 10px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 12px;
}

.spinner {
  width: 28px;
  height: 28px;
  border: 3px solid var(--border);
  border-top-color: var(--brand-purple);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.tarballs-loading.compact .spinner {
  width: 18px;
  height: 18px;
  border-width: 2px;
}

/* Section label shared utility */
.section-label {
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-muted);
}

/* Status badges */
.status-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 3px 9px;
  border-radius: 10px;
  white-space: nowrap;
}

.status-badge.building {
  background: #fef9c3;
  color: #a16207;
}

.status-badge.waiting {
  background: var(--info-tint);
  color: var(--info);
}
</style>
