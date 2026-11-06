<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Package, Target } from '../types/api'
import { displayVersion, TAG_LABEL } from '../composables/useEventDisplay'
import { useRebuild } from '../composables/useRebuild'
import { useAuth } from '../composables/useAuth'
import { isTarballRepo } from '../lib/tarballs'
import { useInstances } from '../composables/useInstances'
import { packageUrl, liveLogUrl } from '../lib/instances'
import InstanceBadge from './InstanceBadge.vue'

const { instanceFor, packageInstances } = useInstances()

const props = defineProps<{ pkg: Package; spotlightStates?: string[]; activeInstances?: string[] }>()

// A tarball package: lives in a :tarballs subproject and builds against an
// ssl* repo (structural detection, matching the Artifacts Tarballs sub-tab).
const isTarball = computed(() =>
  props.pkg.project.endsWith(':tarballs') &&
  (props.pkg.targets ?? []).some(t => isTarballRepo(t.repo)),
)

const { trigger: triggerRebuild, isLoading: isRebuildLoading, errorFor: rebuildErrorFor } = useRebuild()
const { signedIn } = useAuth()

const SKIP_STATES = new Set(['disabled', 'excluded', 'locked'])
const REBUILD_STATES = new Set(['failed', 'broken', 'unresolvable', 'blocked'])

const STATE_COLOR: Record<string, string> = {
  succeeded: 'var(--ok)',
  published: 'var(--ok)',
  failed: 'var(--fail)',
  unresolvable: 'var(--broken)',
  broken: 'var(--broken)',
  blocked: 'var(--blocked)',
  building: 'var(--info)',
  finished: 'var(--warn)',
  scheduled: 'var(--info)',
}

const STATE_BG: Record<string, string> = {
  succeeded: 'var(--ok-tint)',
  published: 'var(--ok-tint)',
  failed: 'var(--fail-tint)',
  unresolvable: 'var(--broken-tint)',
  broken: 'var(--broken-tint)',
  blocked: 'var(--blocked-tint)',
  building: 'var(--info-tint)',
  finished: 'var(--warn-tint)',
  scheduled: 'var(--info-tint)',
}

const STATE_LABEL: Record<string, string> = {
  succeeded: 'Succeeded', published: 'Published', failed: 'Failed', unresolvable: 'Unresolvable',
  broken: 'Broken', blocked: 'Blocked', building: 'Building',
  finished: 'Finishing', scheduled: 'Scheduled',
}


const TARGET_SEVERITY: Record<string, number> = {
  broken: 6, unresolvable: 5, failed: 4, blocked: 3, building: 2, finished: 1, scheduled: 1, succeeded: 0,
}

const INITIAL_VISIBLE = 3

const showAll = ref(false)
const expandedTargets = ref(new Set<string>())

function targetKey(t: Target): string {
  return `${t.repo}/${t.arch}`
}

function toggleTarget(t: Target) {
  if (!hasDetail(t)) return
  const key = targetKey(t)
  const next = new Set(expandedTargets.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expandedTargets.value = next
}

function isExpanded(t: Target): boolean {
  return expandedTargets.value.has(targetKey(t))
}

function hasDetail(t: Target): boolean {
  if (t.build_reason) return true
  if (t.state === 'blocked' && t.blocked_by) return true
  if ((t.state === 'finished' || t.state === 'unresolvable' || t.state === 'broken') && t.details) return true
  return false
}

function finishedOutcome(t: Target): 'ok' | 'fail' | 'unknown' {
  if (t.state !== 'finished') return 'unknown'
  if (t.details === 'succeeded' || t.details === 'unchanged') return 'ok'
  if (t.details === 'failed') return 'fail'
  return 'unknown'
}

function targetDotColor(t: Target): string {
  if (t.state === 'finished') {
    const o = finishedOutcome(t)
    if (o === 'ok') return 'var(--ok)'
    if (o === 'fail') return 'var(--fail)'
  }
  return STATE_COLOR[t.state] ?? 'var(--blocked)'
}


function targetBg(t: Target): string {
  if (t.state === 'finished') {
    const o = finishedOutcome(t)
    if (o === 'ok') return 'var(--ok-tint)'
    if (o === 'fail') return 'var(--fail-tint)'
  }
  return STATE_BG[t.state] ?? 'var(--blocked-tint)'
}

function buildReasonText(t: Target): string {
  if (!t.build_reason) return ''
  if (t.build_reason_packages?.length) return `${t.build_reason}: ${t.build_reason_packages.join(', ')}`
  return t.build_reason
}

function stateDetailLabel(t: Target): string {
  if (t.state === 'blocked') return 'WAITING FOR'
  if (t.state === 'finished') return 'BUILD OUTCOME'
  if (t.state === 'unresolvable') return 'UNRESOLVABLE'
  if (t.state === 'broken') return 'BROKEN'
  return ''
}

function stateDetailValue(t: Target): string {
  if (t.state === 'blocked') return t.blocked_by ?? ''
  if (t.state === 'finished') return t.details ?? ''
  if (t.state === 'unresolvable') return t.details ?? ''
  if (t.state === 'broken') return t.details ?? ''
  return ''
}

function stateDetailColor(t: Target): string {
  if (t.state === 'blocked') return 'var(--warn)'
  if (t.state === 'finished') {
    const o = finishedOutcome(t)
    if (o === 'ok') return 'var(--ok)'
    if (o === 'fail') return 'var(--fail)'
  }
  if (t.state === 'unresolvable') return 'var(--broken)'
  if (t.state === 'broken') return 'var(--broken)'
  return 'var(--text-muted)'
}

const failingTargets = computed(() =>
  props.pkg.targets
    .filter(t => !SKIP_STATES.has(t.state) && t.state !== 'succeeded')
    .sort((a, b) => (TARGET_SEVERITY[b.state] ?? 0) - (TARGET_SEVERITY[a.state] ?? 0))
)
const visibleFailing = computed(() =>
  showAll.value ? failingTargets.value : failingTargets.value.slice(0, INITIAL_VISIBLE)
)
const hiddenCount = computed(() => Math.max(0, failingTargets.value.length - INITIAL_VISIBLE))

const rollupColor = computed(() => STATE_COLOR[props.pkg.rollup_state] ?? 'var(--text-muted)')
const rollupBg = computed(() => STATE_BG[props.pkg.rollup_state] ?? 'var(--blocked-tint)')
const versionLabel = computed(() => displayVersion(props.pkg.version, props.pkg.is_container ?? false))

const obsLinks = computed(() => packageInstances(props.pkg).map(inst => ({
  name: inst.name, url: packageUrl(inst, props.pkg.project, props.pkg.name),
})))

function elapsedTime(iso: string | undefined): string | null {
  if (!iso) return null
  const ms = Date.now() - new Date(iso).getTime()
  if (!Number.isFinite(ms) || ms < 0) return null
  const m = Math.floor(ms / 60000)
  if (m < 1) return '<1m'
  if (m < 60) return `${m}m`
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`
}

const stateAge = computed((): string | null => {
  const startedTimes = props.pkg.targets
    .filter(t => t.state === props.pkg.rollup_state && t.started_at)
    .map(t => new Date(t.started_at!).getTime())
    .filter(n => Number.isFinite(n))
  if (startedTimes.length === 0) return null
  const oldest = new Date(Math.min(...startedTimes)).toISOString()
  const elapsed = elapsedTime(oldest)
  return elapsed ? `for ${elapsed}` : null
})

function logUrl(t: Target): string {
  return liveLogUrl(instanceFor(t.instance), props.pkg.project, props.pkg.name, t.repo, t.arch)
}

function targetDimmed(t: Target): boolean {
  const sel = props.activeInstances ?? []
  return sel.length > 0 && !!t.instance && !sel.includes(t.instance)
}

function targetAge(t: Target): string | null {
  return elapsedTime(t.started_at)
}

const isSpotlit = computed(() => !!props.spotlightStates?.length && props.spotlightStates.includes(props.pkg.rollup_state))
const isDimmed = computed(() => !!props.spotlightStates?.length && !props.spotlightStates.includes(props.pkg.rollup_state))
</script>

<template>
  <div
    class="flex flex-col gap-[11px] rounded-[12px] p-[15px]"
    :style="{
      background: 'var(--bg-card)',
      border: '1px solid var(--border)',
      borderLeft: `4px solid ${rollupColor}`,
      opacity: isDimmed ? 0.2 : 1,
      boxShadow: isSpotlit ? `0 0 0 2px ${rollupColor}, 0 6px 20px rgba(0,0,0,0.12)` : 'none',
      transition: 'opacity 0.2s, box-shadow 0.2s',
    }"
  >
    <!-- Row 1: state pill + duration + OBS link -->
    <div class="flex items-center gap-[9px]">
      <span
        class="text-[10.5px] font-bold uppercase tracking-[0.04em] py-[3px] px-[9px] rounded-[6px]"
        :style="{ color: rollupColor, background: rollupBg }"
      >{{ STATE_LABEL[pkg.rollup_state] ?? pkg.rollup_state }}</span>
      <span v-if="stateAge" class="ml-auto text-[10.5px] text-text-muted font-mono whitespace-nowrap flex-shrink-0">{{ stateAge }}</span>
      <template v-for="(link, i) in obsLinks" :key="link.name">
        <a :href="link.url || undefined" target="_blank" rel="noopener"
           :style="{ marginLeft: i === 0 && !stateAge ? 'auto' : '0' }"
           class="text-[11.5px] font-bold text-brand-purple no-underline whitespace-nowrap flex-shrink-0"
        >{{ obsLinks.length > 1 ? `${link.name} ↗` : 'OBS ↗' }}</a>
      </template>
    </div>

    <!-- Row 2: package name -->
    <code class="font-mono text-[13.5px] font-semibold text-text-primary [overflow-wrap:anywhere] leading-[1.35]">{{ pkg.name }}</code>

    <!-- Row 3: scope tags + version badge -->
    <div class="flex items-center gap-[7px]">
      <span
        v-for="tag in (pkg.tags ?? [])" :key="tag"
        class="text-[9.5px] font-bold uppercase tracking-[0.05em] py-[2px] px-[7px] rounded-[5px] bg-blocked-tint text-blocked"
      >{{ TAG_LABEL[tag] ?? tag }}</span>
      <span
        v-if="isTarball"
        class="text-[9.5px] font-bold uppercase tracking-[0.05em] py-[2px] px-[7px] rounded-[5px]"
        :style="{ background: 'var(--brand-purple-tint)', color: 'var(--brand-purple)' }"
      >Tarball</span>
      <span
        v-if="versionLabel"
        class="font-mono text-[10px] font-bold py-[2px] px-[7px] rounded-[5px] border border-border whitespace-nowrap flex-shrink-0"
        :style="{
          background: (pkg.is_container || isTarball) ? 'var(--brand-purple-tint)' : 'var(--bg-muted, var(--blocked-tint))',
          color: (pkg.is_container || isTarball) ? 'var(--brand-purple)' : 'var(--text-secondary)',
        }"
      >{{ versionLabel }}</span>
    </div>

    <!-- Row 4: project path -->
    <div class="flex">
      <code class="font-mono text-[10.5px] text-text-muted overflow-hidden text-ellipsis whitespace-nowrap">{{ pkg.project }}</code>
    </div>

    <!-- Row 5: failing targets -->
    <div v-if="failingTargets.length > 0" class="flex flex-col gap-[6px]">
      <span class="text-[10.5px] font-bold text-text-muted uppercase tracking-[0.05em]">
        {{ failingTargets.length }} active target{{ failingTargets.length !== 1 ? 's' : '' }}
      </span>
      <div class="flex flex-col gap-[5px]">
        <div
          v-for="t in visibleFailing"
          :key="targetKey(t)"
          class="rounded-[7px] overflow-hidden"
          :style="{ background: targetBg(t), opacity: targetDimmed(t) ? 0.45 : 1 }"
        >
          <!-- Target header row -->
          <div
            class="flex items-center gap-[9px] py-[5px] px-[9px] select-none"
            :style="{ cursor: hasDetail(t) ? 'pointer' : 'default', userSelect: 'none' }"
            @click="toggleTarget(t)"
          >
            <span class="w-2 h-2 rounded-[2px] flex-shrink-0" :style="{ background: targetDotColor(t) }"></span>
            <code class="font-mono text-[11.5px] text-text-primary flex-shrink-0">{{ t.repo }}/{{ t.arch }}</code>
            <InstanceBadge :slug="t.instance" />
            <span class="text-[11px] font-semibold ml-auto flex-shrink-0" :style="{ color: targetDotColor(t) }">
              {{ STATE_LABEL[t.state] ?? t.state }}<template v-if="targetAge(t)"> · {{ targetAge(t) }}</template>
            </span>
            <a
              :href="logUrl(t) || undefined"
              target="_blank"
              rel="noopener"
              class="text-[10.5px] font-bold text-brand-purple flex-shrink-0 no-underline"
              @click.stop
            >log ↗</a>
            <button
              v-if="signedIn && REBUILD_STATES.has(t.state)"
              :disabled="isRebuildLoading(t.repo, t.arch)"
              class="[background:none] border-none px-[2px] text-[13px] flex-shrink-0 leading-none"
              :style="{ cursor: isRebuildLoading(t.repo, t.arch) ? 'default' : 'pointer', color: 'var(--text-muted)' }"
              title="Retrigger build"
              aria-label="Retrigger build"
              @click.stop="triggerRebuild(pkg.project, pkg.name, t.repo, t.arch)"
            >
              <span :class="{ 'rebuild-spinning': isRebuildLoading(t.repo, t.arch) }">↺</span>
            </button>
            <span
              v-if="hasDetail(t)"
              class="text-[10px] text-text-muted flex-shrink-0 w-3 text-center"
            >{{ isExpanded(t) ? '▾' : '▸' }}</span>
          </div>

          <!-- Target body (collapsible) -->
          <div
            v-show="isExpanded(t)"
            class="flex flex-col gap-[5px]"
            :style="{ padding: '0 9px 8px calc(9px + 8px + 9px)' }"
          >
            <hr class="border-none border-t border-border m-0 mb-[3px]" />
            <div v-if="buildReasonText(t)" class="flex flex-col gap-[1px]">
              <span class="text-[9px] uppercase tracking-[0.07em] text-text-muted font-bold">Build reason</span>
              <span class="font-mono text-[10.5px] text-text-secondary leading-[1.4]">{{ buildReasonText(t) }}</span>
            </div>
            <div v-if="stateDetailValue(t)" class="flex flex-col gap-[1px]">
              <span class="text-[9px] uppercase tracking-[0.07em] text-text-muted font-bold">{{ stateDetailLabel(t) }}</span>
              <span class="font-mono text-[10.5px] font-semibold leading-[1.4]" :style="{ color: stateDetailColor(t) }">{{ stateDetailValue(t) }}</span>
            </div>
          </div>

          <!-- Rebuild error -->
          <div
            v-if="rebuildErrorFor(t.repo, t.arch)"
            class="py-[3px] px-[9px] pb-[6px] text-[10.5px] text-fail font-mono"
          >{{ rebuildErrorFor(t.repo, t.arch) }}</div>
        </div>

        <button
          v-if="!showAll && hiddenCount > 0"
          @click="showAll = true"
          class="text-[11px] text-brand-purple font-semibold py-[4px] px-[9px] border-none bg-transparent cursor-pointer text-left [font-family:inherit]"
        >+ {{ hiddenCount }} more</button>
        <button
          v-if="showAll && hiddenCount > 0"
          @click="showAll = false"
          class="text-[11px] text-text-muted font-semibold py-[4px] px-[9px] border-none bg-transparent cursor-pointer text-left [font-family:inherit]"
        >Show less</button>
      </div>
    </div>

    <!-- Row 5: ok targets count -->
    <div class="text-[11px] text-text-muted">{{ pkg.ok_targets }}/{{ pkg.total_targets }} targets ok</div>
  </div>
</template>

<style scoped>
@keyframes rebuild-spin {
  to { transform: rotate(360deg); }
}
.rebuild-spinning {
  display: inline-block;
  animation: rebuild-spin 0.7s linear infinite;
}
button:focus-visible {
  outline: 2px solid var(--brand-purple);
  outline-offset: 2px;
  border-radius: 2px;
}
</style>
