<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { FreezeAction, UnitFreeze, UnitRow } from '../../types/release'
import { safeHref } from '../../lib/safeHref'
import ReleaseConfirm from './ReleaseConfirm.vue'
import ReleaseLoading from './ReleaseLoading.vue'
import ReleaseSpinner from './ReleaseSpinner.vue'
import FreezePicker from './FreezePicker.vue'

const props = defineProps<{ freeze: UnitFreeze[] | null; units: UnitRow[]; error: string; busy: boolean }>()
const emit = defineEmits<{ freeze: [units: string[]]; unfreeze: [units: string[]] }>()

const isFrozen = (f: UnitFreeze) => f.state === 'frozen' || f.state === 'partial'
const rows = computed(() => (props.freeze ?? []).filter((f) => isFrozen(f) || f.pending))
const frozenCount = computed(() => (props.freeze ?? []).filter(isFrozen).length)

const when = (iso: string | null) => {
  const d = iso ? new Date(iso) : null
  return d && !Number.isNaN(d.getTime()) ? d.toLocaleString() : ''
}
const names = (f: UnitFreeze, state: string) => f.instances.filter((i) => i.state === state).map((i) => i.instance)

function detail(f: UnitFreeze): string {
  const parts: string[] = []
  if (f.frozen_by) parts.push(['frozen by ' + f.frozen_by, when(f.frozen_at)].filter(Boolean).join(' · '))
  if (f.state === 'frozen') parts.push('all instances')
  if (f.state === 'partial') {
    const others = f.instances.filter((i) => i.state !== 'frozen').map((i) => i.instance)
    parts.push(`${names(f, 'frozen').join(', ')} only — not frozen on ${others.join(', ')}`)
  }
  const unknown = names(f, 'unknown')
  if (unknown.length) parts.push(`could not read ${unknown.join(', ')}`)
  return parts.join(' · ')
}

const actionLabel = (a: FreezeAction) => (a === 'freeze' ? 'Freezing…' : 'Unfreezing…')
const inFlight = (unit: string) => props.units.some((u) => u.unit === unit && u.blocked?.reason === 'in_flight')

const confirming = ref<string | null>(null)
function confirmUnfreeze() {
  const unit = confirming.value
  confirming.value = null
  if (unit) emit('unfreeze', [unit])
}

const picking = ref(false)
const sent = ref(false)
function onPick(units: string[]) {
  sent.value = true
  emit('freeze', units)
}
function closePicker() {
  picking.value = false
  sent.value = false
}
// The picker closes once the request it sent is done.
watch(() => props.busy, (now, was) => { if (was && !now && sent.value) closePicker() })
</script>

<template>
  <section data-testid="freeze-card" class="rounded-[14px] border border-border bg-bg-card p-5">
    <div class="mb-3 flex flex-wrap items-start gap-3">
      <div class="min-w-0 flex-1">
        <h3 class="m-0 flex items-center gap-2 text-[14px] font-bold text-text-primary">
          Build freeze
          <span v-if="freeze" data-testid="freeze-count" class="rounded-full bg-bg-muted px-2 py-[2px] text-[11px] font-normal text-text-secondary">{{ frozenCount }} frozen</span>
        </h3>
        <p class="m-0 mt-[2px] text-[12px] text-text-muted">A frozen unit ignores dependency updates. Fixes merged to main still build.</p>
      </div>
      <button
        v-if="freeze"
        type="button"
        data-testid="freeze-open"
        class="cursor-pointer rounded-[8px] border border-border-strong bg-bg-card px-3 py-[6px] text-[13px] font-semibold text-brand-purple"
        @click="picking = true"
      >Freeze units…</button>
    </div>

    <div v-if="!freeze && error" data-testid="freeze-error" class="rounded-[10px] border border-fail bg-fail-tint px-3 py-2 text-[13px] text-fail">
      Could not load the freeze state: {{ error }}
    </div>
    <ReleaseLoading v-else-if="!freeze">Loading the freeze state…</ReleaseLoading>
    <template v-else>
      <div v-if="!rows.length" class="text-[13px] text-text-muted">No frozen units.</div>
      <ul v-else class="m-0 flex list-none flex-col gap-[6px] p-0">
        <li v-for="f in rows" :key="f.unit" :data-unit="f.unit" class="flex flex-col gap-1 rounded-[10px] border border-border px-3 py-[10px]">
          <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
            <div class="min-w-0 flex-1">
              <div class="font-mono text-[13px] text-text-primary">{{ f.unit }}</div>
              <div v-if="detail(f)" class="mt-[2px] text-[12px] text-text-muted">{{ detail(f) }}</div>
            </div>
            <button
              v-if="f.state === 'partial'"
              type="button"
              data-testid="freeze-everywhere"
              class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[4px] text-[12px] font-semibold text-brand-purple disabled:cursor-default disabled:opacity-50"
              :disabled="!!f.pending || busy"
              @click="emit('freeze', [f.unit])"
            >Freeze everywhere</button>
            <button
              type="button"
              data-testid="unfreeze"
              class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[4px] text-[12px] font-semibold text-text-primary disabled:cursor-default disabled:opacity-50"
              :disabled="!!f.pending || busy"
              @click="confirming = f.unit"
            >Unfreeze</button>
          </div>
          <div v-if="f.pending" role="status" class="flex flex-wrap items-center gap-2 text-[12px] text-text-primary">
            <ReleaseSpinner />
            <span>{{ actionLabel(f.pending.action) }}</span>
            <span v-if="f.pending.by" class="text-text-muted">started by {{ f.pending.by }}</span>
            <a v-if="safeHref(f.pending.url)" :href="safeHref(f.pending.url)" target="_blank" rel="noopener noreferrer" class="text-brand-purple">workflow run</a>
          </div>
          <div v-if="f.failed" class="flex flex-wrap items-center gap-2 rounded-[4px] border-l-[3px] border-fail bg-fail-tint px-[10px] py-[6px] text-[12px] text-text-primary">
            <span>The last {{ f.failed.action }} failed on {{ f.failed.instances.join(', ') }}</span>
            <a v-if="safeHref(f.failed.url)" :href="safeHref(f.failed.url)" target="_blank" rel="noopener noreferrer" class="text-brand-purple">workflow run</a>
          </div>
        </li>
      </ul>
    </template>

    <ReleaseConfirm
      v-if="confirming"
      :title="`Unfreeze ${confirming}?`"
      body="The postponed rebuilds will start."
      confirm-label="Unfreeze"
      :warning="inFlight(confirming) ? 'A release of this unit is in progress.' : undefined"
      @confirm="confirmUnfreeze"
      @cancel="confirming = null"
    />
    <FreezePicker v-if="picking && freeze" :freeze="freeze" :units="units" :busy="busy" @freeze="onPick" @cancel="closePicker" />
  </section>
</template>
