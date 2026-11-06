<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { applyUpgrade, checkNow, refreshUpgrades, useUpgrades, type ReleaseView } from '../composables/useUpgrades'
import HostUpdateSteps from './HostUpdateSteps.vue'

const POLL_MS = 5000
type Phase = 'idle' | 'applying' | 'restarting' | 'upgraded' | 'failed'

const { view, loadError } = useUpgrades()
const confirming = ref<string | null>(null)
const target = ref<string | null>(null)
const phase = ref<Phase>('idle')
const failure = ref('')
const actionError = ref<string | null>(null)
const checking = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

const pending = computed(() => (view.value?.releases ?? []).filter((r) => r.status === 'available' || r.status === 'applying'))
const history = computed(() => (view.value?.releases ?? []).filter((r) => r.status === 'applied' || r.status === 'failed'))
const busy = computed(() => phase.value === 'applying' || phase.value === 'restarting' || !!view.value?.in_flight)

function fmt(ts: string | null): string {
  return ts ? `${ts.slice(0, 16).replace('T', ' ')} UTC` : ''
}

function stopPolling(): void {
  if (timer !== undefined) clearTimeout(timer)
  timer = undefined
}

function schedule(): void {
  stopPolling()
  timer = setTimeout(() => { void poll() }, POLL_MS)
}

async function poll(): Promise<void> {
  const res = await refreshUpgrades()
  if (target.value === null) return
  if (res === 'unreachable') { phase.value = 'restarting'; schedule(); return }
  if (res !== 'ok' || !view.value) { schedule(); return }
  if (view.value.current === target.value) { phase.value = 'upgraded'; stopPolling(); return }
  const row = view.value.releases.find((r) => r.version === target.value)
  if (row?.status === 'failed') {
    phase.value = 'failed'
    failure.value = row.error
    stopPolling()
    return
  }
  phase.value = 'applying'
  schedule()
}

function begin(version: string): void {
  target.value = version
  failure.value = ''
  phase.value = 'applying'
  schedule()
}

onMounted(async () => {
  await refreshUpgrades()
  if (view.value?.in_flight) begin(view.value.in_flight)
})
onUnmounted(stopPolling)

async function onCheck(): Promise<void> {
  checking.value = true
  actionError.value = await checkNow()
  checking.value = false
}

async function onConfirm(r: ReleaseView): Promise<void> {
  confirming.value = null
  actionError.value = await applyUpgrade(r.version)
  if (actionError.value === null) begin(r.version)
}

function reload(): void {
  window.location.reload()
}
</script>

<template>
  <section data-testid="service-zone" class="rounded-[14px] border border-border bg-bg-card p-5 flex flex-col gap-4">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex flex-col gap-1">
        <h2 class="m-0 text-[16px] font-bold text-text-primary">Service</h2>
        <p class="m-0 text-[12.5px] text-text-muted">
          Running <span data-testid="current-version" class="font-mono text-text-primary">{{ view?.current ?? '…' }}</span>
          <template v-if="view?.checked_at"> · last checked <span data-testid="checked-at">{{ fmt(view.checked_at) }}</span></template>
        </p>
      </div>
      <button
        data-testid="check-now"
        :disabled="checking"
        class="px-[14px] py-2 rounded-[10px] border border-border bg-bg-card text-text-secondary text-[13px] font-semibold cursor-pointer disabled:opacity-50"
        @click="onCheck"
      >Check now</button>
    </header>

    <div v-if="loadError" data-testid="upgrades-error" class="text-[12.5px] text-fail">{{ loadError }}</div>
    <div v-if="actionError" data-testid="action-error" class="text-[12.5px] text-fail">{{ actionError }}</div>
    <div v-if="view && !view.apply_enabled" data-testid="apply-disabled" class="text-[12.5px] text-text-muted">
      Upgrades cannot be applied from this deployment: it is a development build, or UPGRADE_APPLY_DIR is not set.
    </div>

    <div v-if="phase !== 'idle'" data-testid="progress" role="status" class="flex flex-wrap items-center gap-2 rounded-[10px] border border-border bg-bg-app px-3 py-2 text-[13px] text-text-primary">
      <template v-if="phase === 'applying'">Upgrading to {{ target }}…</template>
      <template v-else-if="phase === 'restarting'">Restarting…</template>
      <template v-else-if="phase === 'upgraded'">
        Upgraded to {{ target }}.
        <button data-testid="reload" class="px-[10px] py-[4px] rounded-[8px] border border-border-strong bg-bg-card text-brand-purple text-[12px] font-semibold cursor-pointer" @click="reload">Reload</button>
      </template>
      <template v-else>Upgrade to {{ target }} failed: <span data-testid="failure" class="text-fail">{{ failure }}</span></template>
    </div>

    <ul v-if="pending.length" class="m-0 p-0 list-none flex flex-col divide-y divide-border border border-border rounded-[10px]">
      <li v-for="r in pending" :key="r.version" data-testid="release-row" class="flex flex-col gap-2 px-3 py-3">
        <div class="flex flex-wrap items-center gap-3">
          <a :href="r.url" target="_blank" rel="noopener" class="font-mono text-[13px] font-semibold text-brand-purple">{{ r.version }}</a>
          <span class="text-[11.5px] text-text-muted">{{ r.published_at.slice(0, 10) }}</span>
          <span v-if="r.status === 'applying'" class="ml-auto text-[11.5px] font-semibold text-warn">applying</span>
          <template v-else-if="confirming === r.version">
            <button data-testid="confirm" class="ml-auto px-[10px] py-[4px] rounded-[8px] border border-border-strong bg-bg-card text-brand-purple text-[12px] font-semibold cursor-pointer" @click="onConfirm(r)">Confirm upgrade to {{ r.version }}</button>
            <button data-testid="cancel" class="px-[10px] py-[4px] rounded-[8px] border border-border bg-bg-card text-text-secondary text-[12px] cursor-pointer" @click="confirming = null">Cancel</button>
          </template>
          <button
            v-else
            data-testid="upgrade"
            class="ml-auto px-[10px] py-[4px] rounded-[8px] border border-border-strong bg-bg-card text-brand-purple text-[12px] font-semibold cursor-pointer disabled:opacity-40 disabled:cursor-default"
            :disabled="!view?.apply_enabled || busy || r.blocked"
            @click="confirming = r.version"
          >{{ r.blocked ? 'Host update required' : 'Upgrade' }}</button>
        </div>
        <div v-if="r.blocked && view" data-testid="blocked-note" class="rounded-[8px] border border-warn bg-warn-tint p-3">
          <HostUpdateSteps :release="r" :repo="view.repo" :host-files-version="view.host_files_version" />
        </div>
        <div data-testid="notes" class="whitespace-pre-wrap text-[12.5px] text-text-secondary">{{ r.notes }}</div>
      </li>
    </ul>
    <div v-else-if="view" data-testid="up-to-date" class="text-[12.5px] text-text-muted">No newer release.</div>

    <div v-if="history.length" class="flex flex-col gap-1">
      <h3 class="m-0 text-[13px] font-semibold text-text-primary">History</h3>
      <ul class="m-0 p-0 list-none flex flex-col gap-1">
        <li v-for="r in history" :key="'h:' + r.version" data-testid="history-row" class="flex flex-wrap gap-2 text-[12px]">
          <span class="font-mono text-text-primary">{{ r.version }}</span>
          <span :class="r.status === 'failed' ? 'text-fail' : 'text-text-muted'">{{ r.status }}</span>
          <span class="text-text-muted">{{ fmt(r.finished_at) }}{{ r.requested_by ? ` · by ${r.requested_by}` : '' }}</span>
          <span v-if="r.error" class="text-fail">{{ r.error }}</span>
        </li>
      </ul>
    </div>
  </section>
</template>
