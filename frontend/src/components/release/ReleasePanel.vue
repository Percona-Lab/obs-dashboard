<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRelease } from '../../composables/useRelease'
import ReleaseUnitList from './ReleaseUnitList.vue'
import ReleaseHistory from './ReleaseHistory.vue'
import ReleaseWizard from './ReleaseWizard.vue'
import ReleaseLoading from './ReleaseLoading.vue'
import { safeHref } from '../../lib/safeHref'
import type { ReleaseState, ReleaseStep } from '../../types/release'

const emit = defineEmits<{ 'open-builds': [] }>()
const { snapshot, units, unitsError, unitsLoaded, loadFailed, notice, busy, loadUnits, cut } = useRelease()

const selectedId = ref<string | null>(null)

const STEP_LABEL: Record<ReleaseStep, string> = {
  cutting: 'Cutting', changelog: 'Changelog', sync: 'Pre-release sync', qa: 'QA', releasing: 'Releasing',
}
const releases = computed(() => snapshot.value?.releases ?? [])
const selected = computed(() => releases.value.find((r) => r.id === selectedId.value) ?? null)

// The selected release finished or was abandoned: back to the landing view.
// Only once a snapshot has arrived, so a fresh cut is not dropped before it shows up.
// A release just cut stays selected while GitHub has not listed it yet (`pendingId`),
// for up to START_WAIT_MS.
const START_WAIT_MS = 90_000
const pendingId = ref<string | null>(null)
let pendingTimer: ReturnType<typeof setTimeout> | undefined
function clearPending() {
  pendingId.value = null
  if (pendingTimer !== undefined) clearTimeout(pendingTimer)
  pendingTimer = undefined
}
function leaveStarting() {
  clearPending()
  selectedId.value = null
}
onUnmounted(clearPending)
watch(releases, (rs) => {
  if (pendingId.value && rs.some((r) => r.id === pendingId.value)) clearPending()
  if (selectedId.value && selectedId.value !== pendingId.value && snapshot.value && !rs.some((r) => r.id === selectedId.value)) selectedId.value = null
})

// Refresh the unit list when the set of release ids changes.
const idKey = computed(() => releases.value.map((r) => r.id).sort().join(','))
watch(idKey, () => { void loadUnits() })
onMounted(() => { void loadUnits() })

async function onCut(ticked: string[]) {
  const id = await cut(ticked)
  await loadUnits()
  if (!id) return
  selectedId.value = id
  if (releases.value.some((r) => r.id === id)) return
  clearPending()
  pendingId.value = id
  pendingTimer = setTimeout(() => {
    if (pendingId.value !== id) return
    leaveStarting()
    notice.value = 'The cut did not start. Check the Actions tab of the repository.'
  }, START_WAIT_MS)
}

// blocked.release_id may be a unit release id rather than the release's own id.
function openRelease(id: string) {
  const r = releases.value.find((x) => x.id === id || x.units.some((u) => u.release_id === id))
  if (r) selectedId.value = r.id
}

function stepLabel(r: ReleaseState): string {
  if (r.step === 'cutting') return r.cut?.state === 'failure' ? 'Cut failed' : r.cut?.state === 'approval' ? 'Waiting for approval' : 'Cutting…'
  return STEP_LABEL[r.step]
}
const when = (iso: string | null) => {
  const d = iso ? new Date(iso) : null
  return d && !Number.isNaN(d.getTime()) ? d.toLocaleString() : ''
}
</script>

<template>
  <section data-testid="release-panel" class="flex flex-col gap-4 text-[13px] text-text-secondary">
    <div v-if="notice" data-testid="release-notice" class="flex items-start gap-2 rounded-[10px] border border-warn bg-warn-tint px-3 py-2 text-text-primary">
      <span class="flex-1">{{ notice }}</span>
      <button type="button" aria-label="Dismiss" class="cursor-pointer border-0 bg-transparent p-0 text-text-muted" @click="notice = ''">✕</button>
    </div>
    <div v-if="snapshot?.stale" data-testid="release-stale" class="rounded-[10px] border border-warn bg-warn-tint px-3 py-2 text-text-primary">
      GitHub data may be out of date: {{ snapshot.error }}
    </div>

    <!-- wizard: Task 12 -->
    <ReleaseWizard v-if="selected" :release="selected" @back="selectedId = null" />
    <!-- /wizard -->
    <section v-else-if="selectedId && selectedId === pendingId" data-testid="release-starting" class="rounded-[14px] border border-border bg-bg-card p-5">
      <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-[12px] font-semibold text-brand-purple" @click="leaveStarting">← Back</button>
      <p class="m-0 mt-3 text-text-primary">Starting the cut…</p>
    </section>

    <template v-else>
      <section data-testid="in-flight" class="rounded-[14px] border border-border bg-bg-card p-5">
        <h3 class="m-0 mb-3 text-[14px] font-bold text-text-primary">In flight</h3>
        <ReleaseLoading v-if="!snapshot && !loadFailed">Loading releases from GitHub…</ReleaseLoading>
        <div v-else-if="!snapshot" data-testid="release-load-error" class="rounded-[10px] border border-fail bg-fail-tint px-3 py-2 text-fail">Could not load the releases. Retrying…</div>
        <div v-else-if="!releases.length" data-testid="release-in-flight" class="text-text-muted">No releases in flight.</div>
        <ul v-else data-testid="release-in-flight" class="m-0 flex list-none flex-col gap-[6px] p-0">
          <li v-for="r in releases" :key="r.id" :data-release="r.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-[10px] border border-border px-3 py-[10px]">
            <button type="button" data-testid="open-in-flight" class="cursor-pointer border-0 bg-transparent p-0 font-mono text-[13px] font-semibold text-brand-purple" @click="selectedId = r.id">
              {{ r.units.length ? r.units.map((u) => u.release_id).join(', ') : r.id }}
            </button>
            <a v-if="r.pr && safeHref(r.pr_url)" :href="safeHref(r.pr_url)" target="_blank" rel="noopener noreferrer" class="text-[12px] text-brand-purple">PR #{{ r.pr }}</a>
            <span class="text-[12px] text-text-muted">started by {{ r.started_by || 'unknown' }}<template v-if="when(r.started_at)"> · {{ when(r.started_at) }}</template></span>
            <span class="ml-auto rounded-full bg-bg-muted px-2 py-[2px] text-[11px]" :class="r.step === 'cutting' && r.cut?.state === 'failure' ? 'text-fail' : 'text-text-secondary'">{{ stepLabel(r) }}</span>
          </li>
        </ul>
      </section>

      <section data-testid="new-release" class="rounded-[14px] border border-border bg-bg-card p-5">
        <h3 class="m-0 mb-3 text-[14px] font-bold text-text-primary">New release</h3>
        <ReleaseLoading v-if="!unitsLoaded">Loading release units…</ReleaseLoading>
        <ReleaseUnitList v-else :units="units" :busy="busy" :error="unitsError" @cut="onCut" @open="openRelease" @open-builds="emit('open-builds')" />
      </section>

      <section data-testid="history" class="rounded-[14px] border border-border bg-bg-card p-5">
        <h3 class="m-0 mb-3 text-[14px] font-bold text-text-primary">History</h3>
        <ReleaseHistory />
      </section>
    </template>
  </section>
</template>
