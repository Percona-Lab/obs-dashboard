<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { safeHref } from '../../lib/safeHref'
import ReleaseSpinner from './ReleaseSpinner.vue'
import type { InstanceCard } from '../../types/release'

const props = defineProps<{ cards: InstanceCard[] }>()

const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined
const active = computed(() => props.cards.some((c) => c.state === 'queued' || c.state === 'running'))
watch(active, (on) => {
  if (on && !timer) {
    now.value = Date.now()
    timer = setInterval(() => { now.value = Date.now() }, 1000)
  } else if (!on && timer) {
    clearInterval(timer)
    timer = undefined
  }
}, { immediate: true })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
const cdnNote = (c: InstanceCard) => c.cdn && c.cdn !== 'success' && c.cdn !== 'skipped' ? `Download servers: ${c.cdn}` : ''

function elapsed(c: InstanceCard): string {
  const start = c.started_at ? new Date(c.started_at).getTime() : NaN
  if (Number.isNaN(start)) return ''
  const end = c.finished_at ? new Date(c.finished_at).getTime() : now.value
  const s = Math.max(0, Math.round((end - start) / 1000))
  return s < 60 ? `${s} s` : `${Math.round(s / 60)} min`
}

const PILL: Record<string, string> = {
  queued: 'border-border text-text-muted',
  running: 'border-warn text-warn',
  success: 'border-ok text-ok',
  failure: 'border-fail text-fail',
}
// One line per unit on the card: where it landed on this instance.
const UNIT: Record<string, { icon: string; cls: string; text: string }> = {
  success: { icon: '✓', cls: 'text-ok', text: 'synced' },
  failure: { icon: '✗', cls: 'text-fail', text: 'failed' },
  skipped: { icon: '–', cls: 'text-text-muted', text: 'not on this instance' },
}
const LABEL: Record<string, string> = { queued: 'Queued', running: 'Running', success: 'Done', failure: 'Failed' }
</script>

<template>
  <div class="flex flex-col gap-2">
    <div v-for="c in cards" :key="c.instance" data-testid="run-card" class="rounded-[10px] border border-border p-3">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
        <ReleaseSpinner v-if="c.state === 'queued' || c.state === 'running'" />
        <b class="text-text-primary">{{ c.instance }}</b>
        <span data-testid="card-state" class="rounded-full border px-2 py-[1px] text-[11px] font-semibold" :class="c.nothing_to_sync ? PILL.queued : PILL[c.state]">{{ c.nothing_to_sync ? 'Nothing to sync' : (LABEL[c.state] ?? c.state) }}</span>
        <small v-if="elapsed(c)" data-testid="card-elapsed" class="text-text-muted">{{ elapsed(c) }}</small>
        <a v-if="safeHref(c.url)" :href="safeHref(c.url)" target="_blank" rel="noopener noreferrer" class="ml-auto text-[12px] text-brand-purple">View log on GitHub</a>
      </div>
      <div v-if="cdnNote(c)" data-testid="card-cdn" class="mt-1 text-[12px] text-warn">{{ cdnNote(c) }}</div>
      <ul v-if="c.units?.length" data-testid="card-units" class="m-0 mt-2 flex list-none flex-col gap-[2px] p-0 text-[12px]">
        <li v-for="u in c.units" :key="u.project" data-testid="card-unit" :data-outcome="u.outcome" class="flex flex-wrap items-center gap-x-2">
          <span class="w-3 text-center" :class="UNIT[u.outcome]?.cls">{{ UNIT[u.outcome]?.icon }}</span>
          <span class="font-mono text-text-secondary">{{ u.project }}</span>
          <span :class="UNIT[u.outcome]?.cls">{{ UNIT[u.outcome]?.text }}</span>
        </li>
      </ul>
      <div v-if="c.state === 'failure'" data-testid="card-failure" class="mt-2 text-[12px]">
        <ul v-if="c.errors.length" class="m-0 mt-1 list-disc pl-5 text-text-secondary">
          <li v-for="(e, i) in c.errors" :key="i">{{ e }}</li>
        </ul>
      </div>
    </div>
  </div>
</template>
