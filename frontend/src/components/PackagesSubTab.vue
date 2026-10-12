<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import type { ArtifactBinary, PackageRow, RepoInfo } from '../composables/useArtifacts'
import { distroGroup } from '../composables/useArtifacts'
import { formatArtifactTime } from '../lib/cve'
import { useInstances } from '../composables/useInstances'
import { obsProject, downloadBase } from '../lib/instances'
import { repoSetupSnippet } from '../lib/repoSetup'
import InstanceBadge from './InstanceBadge.vue'

const props = defineProps<{
  packageRows: PackageRow[]
  repos: RepoInfo[]
  selectedRepo: RepoInfo | null
  version: string
  artArch: string
  copiedKey: string | null
  loading?: boolean
}>()

const emit = defineEmits<{
  'update:art-repo': [obs: string]
  'update:art-arch': [arch: string]
  'copy': [key: string, text: string]
}>()

const rhelRepos = computed(() =>
  props.repos.filter(r => distroGroup(r) === 'RHEL')
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
)
const opensuseRepos = computed(() =>
  props.repos.filter(r => distroGroup(r) === 'openSUSE')
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
)
const ubuntuRepos = computed(() =>
  props.repos.filter(r => distroGroup(r) === 'Ubuntu')
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
)
const debianRepos = computed(() =>
  props.repos.filter(r => distroGroup(r) === 'Debian')
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
)
const otherRepos = computed(() =>
  props.repos.filter(r => distroGroup(r) === 'Other')
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
)
const showPackageList = computed(() => props.selectedRepo !== null || props.packageRows.length > 0 || !!props.loading)

// ----- snippet -----

const { instanceFor } = useInstances()

// sudo toggle for the setup snippet: on by default (PG-2745), remembered
// per browser. Storage access is best-effort — private windows and blocked
// site data must not break the tab.
const SUDO_STORAGE_KEY = 'obs-dashboard-repo-setup-sudo'
function readStoredSudo(): boolean {
  try {
    return localStorage.getItem(SUDO_STORAGE_KEY) !== 'off'
  } catch {
    return true
  }
}
const useSudo = ref(readStoredSudo())
function setSudo(on: boolean) {
  useSudo.value = on
  try {
    localStorage.setItem(SUDO_STORAGE_KEY, on ? 'on' : 'off')
  } catch {
    // ignore: preference simply will not persist
  }
}

const snippet = computed(() => {
  const repo = props.selectedRepo
  if (!repo) return ''
  const project = props.packageRows[0]?.project ?? `ppg:${props.version}`
  const inst = instanceFor(props.packageRows[0]?.instance)
  if (!inst) return ''
  return repoSetupSnippet(
    {
      obsProjectName: obsProject(inst, project), // repo alias / file names, as today
      baseUrl: downloadBase(inst, project, repo.obs),
      repoObs: repo.obs,
      repoType: repo.type,
    },
    { sudo: useSudo.value },
  )
})

// ----- binary expansion -----

type BinaryState = ArtifactBinary[] | 'loading' | 'error'
const binaryCache = reactive<Record<string, BinaryState>>({})
const expanded = reactive<Record<string, boolean>>({})

function rowKey(row: PackageRow): string {
  return `${row.project}/${row.repo.obs}/${row.arch}/${row.name}`
}

async function toggleRow(row: PackageRow) {
  const key = rowKey(row)
  expanded[key] = !expanded[key]
  if (!expanded[key] || binaryCache[key] !== undefined) return

  if (row.binaries) {
    binaryCache[key] = row.binaries
    return
  }

  binaryCache[key] = 'loading'
  try {
    const params = new URLSearchParams({
      project: row.project,
      repo: row.repo.obs,
      arch: row.arch,
      package: row.name,
    })
    const res = await fetch(`/api/binaries?${params}`)
    if (!res.ok) throw new Error(res.statusText)
    const data = await res.json() as { binaries: string[] }
    binaryCache[key] = data.binaries.map(filename => ({ filename }))
  } catch {
    binaryCache[key] = 'error'
  }
}

function downloadUrl(row: PackageRow, filename: string): string {
  const base = downloadBase(instanceFor(row.instance), row.project, row.repo.obs)
  return base ? `${base}${row.arch}/${filename}` : ''
}

// ----- labels -----


const STATE_LABELS: Record<string, string> = {
  succeeded: 'Built',
  building:  'Building',
  scheduled: 'Scheduled',
  blocked:   'Blocked',
  failed:    'Failed',
  disabled:  'Disabled',
  excluded:  'Excluded',
  broken:    'Broken',
  unresolvable: 'Unresolvable',
}

function stateLabel(state: string): string {
  return STATE_LABELS[state] ?? state
}

function stateClass(state: string): string {
  if (state === 'succeeded') return 'status-built'
  if (state === 'building' || state === 'scheduled') return 'status-building'
  if (state === 'failed' || state === 'broken' || state === 'unresolvable') return 'status-failed'
  return 'status-other'
}

// A row is expandable/downloadable when a built artifact is available:
// either the build succeeded, or a new build is in progress while the
// previous build's binaries are still downloadable (isRebuilding).
function canExpand(row: PackageRow): boolean {
  return row.state === 'succeeded' || !!row.isRebuilding
}

</script>

<template>
  <!-- packages-subtab: flex gap-4 p-4 h-full min-h-0 -->
  <div class="flex flex-col lg:flex-row gap-4 p-4 h-full min-h-0">
    <!-- Mobile repo dropdown (below lg) -->
    <select
      class="lg:hidden w-full font-mono text-[13px] text-text-secondary bg-bg-card border border-border rounded-lg px-3 py-2 cursor-pointer [appearance:auto]"
      :value="selectedRepo?.obs ?? ''"
      @change="emit('update:art-repo', ($event.target as HTMLSelectElement).value)"
    >
      <option value="" disabled>Select a build repo…</option>
      <optgroup v-if="rhelRepos.length > 0" label="RHEL">
        <option v-for="repo in rhelRepos" :key="repo.obs" :value="repo.obs">{{ repo.name }}</option>
      </optgroup>
      <optgroup v-if="opensuseRepos.length > 0" label="openSUSE">
        <option v-for="repo in opensuseRepos" :key="repo.obs" :value="repo.obs">{{ repo.name }}</option>
      </optgroup>
      <optgroup v-if="ubuntuRepos.length > 0" label="Ubuntu">
        <option v-for="repo in ubuntuRepos" :key="repo.obs" :value="repo.obs">{{ repo.name }}</option>
      </optgroup>
      <optgroup v-if="debianRepos.length > 0" label="Debian">
        <option v-for="repo in debianRepos" :key="repo.obs" :value="repo.obs">{{ repo.name }}</option>
      </optgroup>
      <optgroup v-if="otherRepos.length > 0" label="Other">
        <option v-for="repo in otherRepos" :key="repo.obs" :value="repo.obs">{{ repo.name }}</option>
      </optgroup>
    </select>
    <!-- Sidebar: w-[220px] shrink-0 -->
    <div class="hidden lg:flex w-[220px] shrink-0 self-start bg-bg-card rounded-xl overflow-hidden flex-col">
      <template v-if="rhelRepos.length > 0">
        <!-- group-label -->
        <div class="px-4 py-2 text-[11px] font-semibold uppercase tracking-[0.06em] text-text-muted border-t border-b border-border first:border-t-0">RHEL</div>
        <button
          v-for="repo in rhelRepos"
          :key="repo.obs"
          class="w-full py-[9px] px-4 text-left border-none text-[13.5px] cursor-pointer"
          :class="selectedRepo?.obs === repo.obs
            ? 'bg-brand-purple-tint text-brand-purple font-bold'
            : 'bg-transparent text-text-secondary font-medium'"
          @click="emit('update:art-repo', repo.obs)"
        >{{ repo.name }}</button>
      </template>

      <template v-if="opensuseRepos.length > 0">
        <div class="px-4 py-2 text-[11px] font-semibold uppercase tracking-[0.06em] text-text-muted border-t border-b border-border first:border-t-0">openSUSE</div>
        <button
          v-for="repo in opensuseRepos"
          :key="repo.obs"
          class="w-full py-[9px] px-4 text-left border-none text-[13.5px] cursor-pointer"
          :class="selectedRepo?.obs === repo.obs
            ? 'bg-brand-purple-tint text-brand-purple font-bold'
            : 'bg-transparent text-text-secondary font-medium'"
          @click="emit('update:art-repo', repo.obs)"
        >{{ repo.name }}</button>
      </template>

      <template v-if="ubuntuRepos.length > 0">
        <div class="px-4 py-2 text-[11px] font-semibold uppercase tracking-[0.06em] text-text-muted border-t border-b border-border first:border-t-0">Ubuntu</div>
        <button
          v-for="repo in ubuntuRepos"
          :key="repo.obs"
          class="w-full py-[9px] px-4 text-left border-none text-[13.5px] cursor-pointer"
          :class="selectedRepo?.obs === repo.obs
            ? 'bg-brand-purple-tint text-brand-purple font-bold'
            : 'bg-transparent text-text-secondary font-medium'"
          @click="emit('update:art-repo', repo.obs)"
        >{{ repo.name }}</button>
      </template>

      <template v-if="debianRepos.length > 0">
        <div class="px-4 py-2 text-[11px] font-semibold uppercase tracking-[0.06em] text-text-muted border-t border-b border-border first:border-t-0">Debian</div>
        <button
          v-for="repo in debianRepos"
          :key="repo.obs"
          class="w-full py-[9px] px-4 text-left border-none text-[13.5px] cursor-pointer"
          :class="selectedRepo?.obs === repo.obs
            ? 'bg-brand-purple-tint text-brand-purple font-bold'
            : 'bg-transparent text-text-secondary font-medium'"
          @click="emit('update:art-repo', repo.obs)"
        >{{ repo.name }}</button>
      </template>

      <template v-if="otherRepos.length > 0">
        <div class="px-4 py-2 text-[11px] font-semibold uppercase tracking-[0.06em] text-text-muted border-t border-b border-border first:border-t-0">Other</div>
        <button
          v-for="repo in otherRepos"
          :key="repo.obs"
          class="w-full py-[9px] px-4 text-left border-none text-[13.5px] cursor-pointer"
          :class="selectedRepo?.obs === repo.obs
            ? 'bg-brand-purple-tint text-brand-purple font-bold'
            : 'bg-transparent text-text-secondary font-medium'"
          @click="emit('update:art-repo', repo.obs)"
        >{{ repo.name }}</button>
      </template>

      <div v-if="repos.length === 0" class="px-4 py-3 text-[12px] text-text-muted">
        {{ packageRows.length > 0 ? 'No build repos' : 'Loading…' }}
      </div>
    </div>

    <!-- Main content -->
    <div class="flex-1 flex flex-col gap-3 min-w-0 overflow-y-auto">
      <!-- Combined repo header + snippet card -->
      <div class="bg-bg-card rounded-[14px] overflow-hidden shrink-0" v-if="selectedRepo">
        <div class="flex items-center justify-between px-[18px] py-[14px]">
          <div class="flex items-baseline gap-[10px]">
            <span class="text-[17px] font-bold">{{ selectedRepo.name }}</span>
            <code class="[font-family:var(--font-mono)] text-[12px] text-text-muted">{{ selectedRepo.obs }}</code>
          </div>
          <!-- arch-selector: segmented control -->
          <div class="flex gap-0.5 bg-bg-muted [padding:3px] rounded-[9px] border border-border">
            <button
              class="px-3 py-1 rounded-[7px] text-[12px] font-medium cursor-pointer border"
              :class="artArch === 'x86_64'
                ? 'bg-bg-card text-brand-purple border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.10)]'
                : 'bg-transparent text-text-muted border-transparent'"
              @click="emit('update:art-arch', 'x86_64')"
            >x86_64</button>
            <button
              class="px-3 py-1 rounded-[7px] text-[12px] font-medium cursor-pointer border"
              :class="artArch === 'aarch64'
                ? 'bg-bg-card text-brand-purple border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.10)]'
                : 'bg-transparent text-text-muted border-transparent'"
              @click="emit('update:art-arch', 'aarch64')"
            >aarch64</button>
          </div>
        </div>
        <div class="px-[18px] pb-[18px]">
          <div class="flex items-center justify-between gap-3 mb-2">
            <span class="text-[11px] font-semibold uppercase tracking-[0.06em] text-text-muted">Repository setup</span>
            <div class="flex items-center gap-2">
              <!-- sudo toggle: root shells and minimal containers have no sudo -->
              <div class="flex gap-[3px] bg-bg-muted p-[3px] rounded-[9px] border border-border" role="group" aria-label="Privilege prefix">
                <button
                  type="button"
                  class="px-3 py-[2px] rounded-[7px] border text-[12px] cursor-pointer [font-family:inherit]"
                  :class="useSudo
                    ? 'bg-bg-card text-text-primary font-bold border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)]'
                    : 'bg-transparent text-text-muted font-medium border-transparent'"
                  :aria-pressed="useSudo"
                  @click="setSudo(true)"
                >sudo</button>
                <button
                  type="button"
                  class="px-3 py-[2px] rounded-[7px] border text-[12px] cursor-pointer [font-family:inherit]"
                  :class="!useSudo
                    ? 'bg-bg-card text-text-primary font-bold border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)]'
                    : 'bg-transparent text-text-muted font-medium border-transparent'"
                  :aria-pressed="!useSudo"
                  @click="setSudo(false)"
                >no sudo</button>
              </div>
              <button
                class="text-[12px] py-1 px-[10px] rounded-md border border-border bg-bg-card text-text-secondary cursor-pointer"
                :class="{ 'text-[var(--success)] border-[var(--success)]': copiedKey === 'repo-config' }"
                @click="emit('copy', 'repo-config', snippet)"
              >
                {{ copiedKey === 'repo-config' ? '✓ Copied' : 'Copy' }}
              </button>
            </div>
          </div>
          <pre class="bg-bg-card-2 py-[14px] px-4 rounded-lg font-code text-[12px] whitespace-pre overflow-x-auto m-0"><code class="font-code">{{ snippet }}</code></pre>
        </div>
      </div>

      <!-- Package list card -->
      <div class="bg-bg-card rounded-xl overflow-hidden shrink-0" v-if="showPackageList">
        <div class="flex items-baseline gap-[10px] px-[18px] py-[14px] border-b border-border">
          <span class="text-[15px] font-bold">Packages</span>
          <span class="text-[12px] text-text-muted">
            {{ packageRows.length }} available
            <template v-if="selectedRepo"> · {{ selectedRepo.name }} / {{ artArch }}</template>
          </span>
        </div>
        <div v-if="loading" class="flex flex-col items-center justify-center py-12 gap-3">
          <div class="spinner"></div>
          <span class="text-[13px] text-text-muted">Fetching packages…</span>
        </div>
        <div v-else class="overflow-y-auto">
          <div
            v-for="row in packageRows"
            :key="rowKey(row)"
            class="border-b border-border last:border-b-0"
          >
            <!-- Package header row (click to expand) -->
            <button
              class="pkg-row flex items-center gap-[10px] py-[10px] px-[18px] w-full text-left border-none cursor-pointer"
              :class="expanded[rowKey(row)] ? 'bg-bg-muted' : 'bg-transparent'"
              @click="canExpand(row) ? toggleRow(row) : undefined"
              :disabled="!canExpand(row)"
              :title="canExpand(row) ? 'Click to show binaries' : 'Package is not in succeeded state'"
            >
              <span class="text-[9px] text-text-muted w-[10px] shrink-0">{{ canExpand(row) ? (expanded[rowKey(row)] ? '▼' : '▶') : '' }}</span>
              <code class="[font-family:var(--font-mono)] text-[13px] font-bold flex-1 min-w-0 text-left">{{ row.name }}</code>
              <InstanceBadge :slug="row.instance" />
              <code v-if="row.version" class="[font-family:var(--font-mono)] text-[11px] text-text-muted whitespace-nowrap shrink-0">{{ row.version }}</code>
              <span v-if="row.builtAt" class="text-[11px] text-text-muted whitespace-nowrap shrink-0">{{ formatArtifactTime(row.builtAt) }}</span>
              <span
                v-if="row.isRebuilding"
                class="status-badge status-stale-warning"
                title="A new build is in progress — these files may be replaced soon."
              >Rebuilding — files may be replaced</span>
              <span v-else-if="row.state !== 'succeeded'" class="status-badge" :class="stateClass(row.state)">
                {{ stateLabel(row.state) }}
              </span>
            </button>

            <!-- Binary list (expanded) -->
            <div v-if="expanded[rowKey(row)]" class="bg-bg-card-2 border-t border-border py-[6px]">
              <div v-if="binaryCache[rowKey(row)] === 'loading'" class="py-2 px-[18px] pl-[38px] text-[12px] text-text-muted">
                Loading…
              </div>
              <div v-else-if="binaryCache[rowKey(row)] === 'error'" class="py-2 px-[18px] pl-[38px] text-[12px] text-[var(--danger,#dc2626)]">
                Failed to load binaries.
              </div>
              <template v-else-if="Array.isArray(binaryCache[rowKey(row)])">
                <div
                  v-for="binary in (binaryCache[rowKey(row)] as ArtifactBinary[])"
                  :key="binary.filename"
                  class="binary-row flex items-center justify-between py-[6px] px-[18px] pl-[38px] gap-3 hover:bg-bg-muted"
                >
                  <div class="flex flex-col gap-0.5 min-w-0">
                    <code class="[font-family:var(--font-mono)] text-[12px] text-text-secondary flex-1 min-w-0 [word-break:break-all]">{{ binary.filename }}</code>
                    <span v-if="binary.built_at" class="text-[11px] text-text-muted">{{ formatArtifactTime(binary.built_at) }}</span>
                  </div>
                  <a
                    class="text-[12px] py-[3px] px-[10px] rounded-md bg-brand-purple text-white no-underline whitespace-nowrap font-medium shrink-0"
                    :href="downloadUrl(row, binary.filename) || undefined"
                    target="_blank"
                  >&#x2193; Download</a>
                </div>
                <div v-if="(binaryCache[rowKey(row)] as ArtifactBinary[]).length === 0" class="py-2 px-[18px] pl-[38px] text-[12px] text-text-muted">
                  No distributable binaries.
                </div>
              </template>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* pkg-row hover/disabled states — complex pseudo-class selectors kept scoped */
.pkg-row:hover:not(:disabled) {
  background: var(--bg-muted);
}

.pkg-row:disabled {
  cursor: default;
  opacity: 0.7;
}

/* Spinner keyframes — must stay scoped */
@keyframes spin {
  to { transform: rotate(360deg); }
}

.spinner {
  width: 28px;
  height: 28px;
  border: 3px solid var(--border);
  border-top-color: var(--brand-purple);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

/* Status badge base + color classes — kept scoped for clarity */
.status-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 10px;
  white-space: nowrap;
}

.status-built {
  background: var(--success-tint, #d1fae5);
  color: var(--success, #16a34a);
}

.status-building {
  background: #fef9c3;
  color: #a16207;
}

.status-stale-warning {
  background: var(--warn-tint, #fef9c3);
  color: var(--warn, #a16207);
  cursor: help;
}

.status-failed {
  background: #fee2e2;
  color: var(--danger, #dc2626);
}

.status-other {
  background: var(--bg-muted);
  color: var(--text-muted);
}


</style>
