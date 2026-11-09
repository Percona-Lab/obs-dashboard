<script setup lang="ts">
import { computed, reactive, ref, onUnmounted } from 'vue'
import type { OverviewProject } from '../types/overview'
import type { CveScan } from '../types/api'
import { oldestOpenLabel, oldestOpenColor, groupByCategory, formatFixHours } from '../lib/overview'
import { latestScanTime } from '../lib/cve'
import CveFindingsTable from './CveFindingsTable.vue'
import { useAuth, handleUnauthorized } from '../composables/useAuth'
import { errorText } from '../composables/http'

const props = defineProps<{
  projects: OverviewProject[]
  accentOf: (project: string) => string
}>()

// Only images that actually carry CVEs are shown, and only projects that have
// at least one such image get a row — clean images/projects are noise here.
const projectsWithCves = computed(() =>
  props.projects
    .map(p => ({ ...p, images: p.images.filter(i => i.critical + i.high > 0) }))
    .filter(p => p.images.length > 0),
)

// Devel / Releases / PRs sections, preserving snapshot order within each.
const groups = computed(() => groupByCategory(projectsWithCves.value, p => p.project))

// Keyed by project path (not array index) so expansion survives snapshot
// replacement from SSE updates. Multiple rows may be open at once.
const expanded = reactive<Record<string, boolean>>({})

function toggle(project: string) {
  expanded[project] = !expanded[project]
}

function regionId(project: string): string {
  return `cve-imgs-${project.replace(/[^a-z0-9]/gi, '-')}`
}

interface Aggregate {
  critical: number
  high: number
  oldest: number
  avgFix: number | null
}

function computeAggregate(p: OverviewProject): Aggregate {
  const critical = p.images.reduce((s, i) => s + i.critical, 0)
  const high = p.images.reduce((s, i) => s + i.high, 0)
  const oldest = p.images.reduce((m, i) => Math.max(m, i.oldest_open_days), 0)
  // Images with at least one closed episode, whether or not they are
  // vulnerable right now (a fixed image is exactly what fix time measures).
  const fixes = p.images.filter(i => i.avg_fix_hours > 0).map(i => i.avg_fix_hours)
  const avgFix = fixes.length > 0 ? fixes.reduce((a, b) => a + b, 0) / fixes.length : null
  return { critical, high, oldest, avgFix }
}

// Cached per-project aggregates keyed by path so template repeats don't
// recompute the same reduce()s on every access within a render.
const aggregates = computed<Record<string, Aggregate>>(() => {
  const map: Record<string, Aggregate> = {}
  for (const p of projectsWithCves.value) map[p.project] = computeAggregate(p)
  return map
})

function aggregate(project: string): Aggregate {
  return aggregates.value[project] ?? { critical: 0, high: 0, oldest: 0, avgFix: null }
}


function badgeClass(n: number, kind: 'crit' | 'high'): string {
  if (n > 0) {
    return kind === 'crit' ? 'text-crit bg-crit-tint' : 'text-high bg-high-tint'
  }
  return 'text-text-muted bg-bg-muted'
}

// --- Third-level expansion: per-image CVE report ---------------------------
// Reports are fetched lazily on first expand and cached for 5 minutes. An
// entry older than the TTL is treated as absent (dropped → shimmer + refetch);
// SSE snapshot refetches never touch this cache — expiry alone governs
// freshness (user decision).
const FINDINGS_TTL_MS = 5 * 60_000

interface ReportEntry {
  scans: CveScan[]
  fetchedAt: number
}

// All keyed by img.project + '/' + img.name so state survives snapshot
// replacement, independent of project-row expansion.
const reportOpen = reactive<Record<string, boolean>>({})
const reportCache = reactive(new Map<string, ReportEntry>())
const reportLoading = reactive<Record<string, boolean>>({})
const reportError = reactive<Record<string, boolean>>({})

function imgKey(img: { project: string; name: string; repo: string }): string {
  return img.project + '/' + img.name + '/' + img.repo
}

function reportRegionId(key: string): string {
  return `cve-report-${key.replace(/[^a-z0-9]/gi, '-')}`
}

function toggleReport(img: { project: string; name: string; repo: string }) {
  const key = imgKey(img)
  reportOpen[key] = !reportOpen[key]
  if (!reportOpen[key]) return
  const entry = reportCache.get(key)
  if (entry && Date.now() - entry.fetchedAt < FINDINGS_TTL_MS) return
  if (reportLoading[key]) return // a fetch is already in flight for this key
  reportCache.delete(key) // stale entry must shimmer, not flash old data
  void fetchReport(img)
}

// Per-key request sequence: an overlapping fetch for the same image (retry
// double-click, close/reopen mid-flight) supersedes the older one, whose
// settlement must not clobber the newer result. Plain object — never rendered.
const reportSeq: Record<string, number> = {}

async function fetchReport(img: { project: string; name: string; repo: string }) {
  const key = imgKey(img)
  const seq = (reportSeq[key] = (reportSeq[key] ?? 0) + 1)
  reportLoading[key] = true
  reportError[key] = false
  try {
    const res = await fetch(
      `/api/cve/scans?project=${encodeURIComponent(img.project)}&package=${encodeURIComponent(img.name)}`,
    )
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const scans: CveScan[] = await res.json()
    if (seq !== reportSeq[key]) return // superseded by a newer request
    reportCache.set(key, { scans: scans.filter(s => s.repo === img.repo), fetchedAt: Date.now() })
  } catch {
    if (seq !== reportSeq[key]) return
    reportError[key] = true
  } finally {
    if (seq === reportSeq[key]) reportLoading[key] = false
  }
}

function reportScans(key: string): CveScan[] | null {
  return reportCache.get(key)?.scans ?? null
}

// --- Rescan of every image that currently has CVEs (signed-in users) -------
// Results arrive through the scan events on the global stream, which already
// trigger an overview refetch, so the table updates as scans finish.
const { signedIn } = useAuth()
const RESCAN_COOLDOWN_MS = 10_000
const rescanBusy = ref(false)
const rescanMsg = ref<string | null>(null)
const rescanFailed = ref(false)
let rescanTimer: ReturnType<typeof setTimeout> | null = null
onUnmounted(() => { if (rescanTimer) clearTimeout(rescanTimer) })

async function rescan() {
  if (rescanBusy.value) return
  rescanBusy.value = true
  rescanFailed.value = false
  rescanMsg.value = null
  let cooldown = false
  try {
    const res = await fetch('/api/cve/rescan', { method: 'POST' })
    if (res.status === 401) {
      handleUnauthorized()
      return
    }
    if (!res.ok) throw new Error(await errorText(res))
    const { queued } = await res.json() as { queued: number }
    rescanMsg.value = queued > 0
      ? `Queued ${queued} image${queued === 1 ? '' : 's'}`
      : 'Nothing queued: rescans already pending'
    cooldown = true
  } catch (e) {
    rescanFailed.value = true
    rescanMsg.value = `Rescan failed: ${e instanceof Error ? e.message : 'network error'}`
  } finally {
    if (cooldown) {
      rescanTimer = setTimeout(() => { rescanBusy.value = false; rescanMsg.value = null }, RESCAN_COOLDOWN_MS)
    } else {
      rescanBusy.value = false
    }
  }
}
</script>

<template>
  <div class="bg-bg-card border border-border rounded-[14px] overflow-hidden">
    <div class="p-[15px_20px] border-b border-border flex items-baseline gap-[10px]">
      <h2 class="m-0 text-[14px] font-bold">CVE exposure by project</h2>
      <span class="text-[12px] text-text-muted">click a project to see per-image breakdown</span>
      <template v-if="signedIn && groups.length > 0">
        <span
          v-if="rescanMsg"
          data-testid="cve-rescan-status"
          role="status"
          class="ml-auto text-[12px]"
          :class="rescanFailed ? 'text-fail' : 'text-text-muted'"
        >{{ rescanMsg }}</span>
        <button
          type="button"
          data-testid="cve-rescan"
          class="px-[10px] py-[4px] rounded-[8px] border border-border bg-bg-card text-text-secondary text-[12px] font-semibold cursor-pointer disabled:opacity-50 disabled:cursor-default self-center"
          :class="rescanMsg ? '' : 'ml-auto'"
          :disabled="rescanBusy"
          title="Scan again every image that currently has CVEs"
          @click="rescan"
        >Rescan affected</button>
      </template>
    </div>

    <div class="grid grid-cols-[1fr_90px_90px_130px_130px] gap-3 items-center p-[9px_20px] bg-bg-card-2 border-b border-border text-[10.5px] font-bold uppercase tracking-[0.05em] text-text-muted">
      <span>Project</span>
      <span class="text-center">Critical</span>
      <span class="text-center">High</span>
      <span class="text-right">Oldest open</span>
      <span class="text-right">Avg fix time</span>
    </div>

    <template v-for="group in groups" :key="group.category">
      <!-- Category separator row: plain card surface + secondary label so it
           reads distinctly from the tinted (bg-card-2) column-header band. -->
      <div class="flex items-center gap-2 p-[9px_20px] border-b border-border">
        <span class="text-[10.5px] font-bold uppercase tracking-[0.06em] text-text-secondary">{{ group.category }}</span>
        <span class="flex-1 border-t border-border" />
      </div>

      <template v-for="p in group.items" :key="p.project">
      <button
        type="button"
        class="w-full text-left p-[11px_20px] border-b border-border grid grid-cols-[1fr_90px_90px_130px_130px] gap-3 items-center"
        :class="expanded[p.project] ? 'bg-bg-card-2' : ''"
        :aria-expanded="!!expanded[p.project]"
        :aria-controls="regionId(p.project)"
        @click="toggle(p.project)"
      >
        <span class="flex items-center gap-2 min-w-0">
          <span
            class="text-text-muted text-[11px] transition-transform inline-block"
            :class="expanded[p.project] ? 'rotate-90' : ''"
          >▸</span>
          <span class="w-[9px] h-[9px] rounded-[3px] shrink-0" :style="{ background: accentOf(p.project) }" />
          <span class="font-mono text-[13.5px] font-semibold truncate">{{ p.project }}</span>
          <span class="text-[11px] text-text-muted whitespace-nowrap">{{ p.images.length }} images</span>
        </span>
        <span
          class="min-w-[30px] text-center text-[13px] font-bold px-2 py-0.5 rounded-md justify-self-center font-mono"
          :class="badgeClass(aggregate(p.project).critical, 'crit')"
        >{{ aggregate(p.project).critical }}</span>
        <span
          class="min-w-[30px] text-center text-[13px] font-bold px-2 py-0.5 rounded-md justify-self-center font-mono"
          :class="badgeClass(aggregate(p.project).high, 'high')"
        >{{ aggregate(p.project).high }}</span>
        <span
          class="font-mono font-semibold justify-self-end"
          :style="{ color: oldestOpenColor(aggregate(p.project).oldest, aggregate(p.project).critical + aggregate(p.project).high > 0) }"
        >{{ oldestOpenLabel(aggregate(p.project).oldest, aggregate(p.project).critical + aggregate(p.project).high > 0) }}</span>
        <span class="font-mono text-text-secondary justify-self-end">
          {{ formatFixHours(aggregate(p.project).avgFix ?? 0) }}
        </span>
      </button>

      <div
        v-show="expanded[p.project]"
        :id="regionId(p.project)"
        class="bg-bg-card-2 border-b border-border"
      >
        <template v-for="(img, idx) in p.images" :key="imgKey(img)">
          <button
            type="button"
            class="w-full text-left pl-10 p-[9px_20px] grid grid-cols-[1fr_90px_90px_130px_130px] gap-3 items-center"
            :class="idx > 0 ? 'border-t border-border' : ''"
            :aria-expanded="!!reportOpen[imgKey(img)]"
            :aria-controls="reportRegionId(imgKey(img))"
            @click="toggleReport(img)"
          >
            <span class="flex items-center gap-2 min-w-0">
              <span
                class="text-text-muted text-[10px] transition-transform inline-block"
                :class="reportOpen[imgKey(img)] ? 'rotate-90' : ''"
              >▸</span>
              <span class="font-mono text-[12px] text-text-secondary truncate">
                {{ img.name }}
                <span v-if="img.base_os" class="text-text-muted text-[10.5px]"> · {{ img.base_os }}</span>
              </span>
            </span>
            <span
              class="min-w-[30px] text-center text-[12.5px] font-bold px-2 py-0.5 rounded-md justify-self-center font-mono"
              :class="badgeClass(img.critical, 'crit')"
            >{{ img.critical }}</span>
            <span
              class="min-w-[30px] text-center text-[12.5px] font-bold px-2 py-0.5 rounded-md justify-self-center font-mono"
              :class="badgeClass(img.high, 'high')"
            >{{ img.high }}</span>
            <span
              class="font-mono text-[12.5px] font-semibold justify-self-end"
              :style="{ color: oldestOpenColor(img.oldest_open_days, img.critical + img.high > 0) }"
            >{{ oldestOpenLabel(img.oldest_open_days, img.critical + img.high > 0) }}</span>
            <span class="font-mono text-[12.5px] text-text-muted justify-self-end">
              {{ formatFixHours(img.avg_fix_hours) }}
            </span>
          </button>

          <div
            v-show="reportOpen[imgKey(img)]"
            :id="reportRegionId(imgKey(img))"
            class="border-t border-border pl-14 pr-5 py-3"
          >
            <div v-if="reportLoading[imgKey(img)]" class="animate-pulse flex flex-col gap-2" aria-hidden="true">
              <div class="h-3 w-40 rounded bg-bg-muted"></div>
              <div class="h-3 w-full rounded bg-bg-muted"></div>
              <div class="h-3 w-3/4 rounded bg-bg-muted"></div>
            </div>
            <div v-else-if="reportError[imgKey(img)]" class="text-[12px] text-text-muted">
              failed to load report —
              <button type="button" class="underline text-text-secondary" @click="fetchReport(img)">retry</button>
            </div>
            <template v-else-if="reportScans(imgKey(img))?.length">
              <div class="text-[11px] text-text-muted mb-2">Scanned {{ latestScanTime(reportScans(imgKey(img))!) }}</div>
              <CveFindingsTable :scans="reportScans(imgKey(img))!" />
            </template>
            <div v-else-if="reportScans(imgKey(img))" class="text-[12px] text-text-muted">no scan data for this image</div>
          </div>
        </template>
      </div>
      </template>
    </template>
  </div>
</template>
