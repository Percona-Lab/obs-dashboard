<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import type { UnitFreeze, UnitRow } from '../../types/release'
import { groupUnits } from '../../lib/unitGroups'
import ReleaseSpinner from './ReleaseSpinner.vue'

const props = defineProps<{ freeze: UnitFreeze[]; units: UnitRow[]; busy: boolean }>()
const emit = defineEmits<{ freeze: [units: string[]]; cancel: [] }>()

const ticked = ref<string[]>([])
const cancelBtn = ref<HTMLButtonElement | null>(null)
onMounted(() => cancelBtn.value?.focus())

const groups = computed(() => groupUnits(props.units))
const stateOf = (unit: string) => props.freeze.find((f) => f.unit === unit)

// Why a unit cannot be ticked; '' when it can.
function disabledReason(unit: string): string {
  const f = stateOf(unit)
  if (f?.pending) return f.pending.action === 'freeze' ? 'a freeze is running' : 'an unfreeze is running'
  if (!f || f.state === '') return 'state unknown'
  if (f.state === 'frozen') return 'already frozen'
  return ''
}

const selected = computed(() => ticked.value.filter((u) => !disabledReason(u)))

function toggle(unit: string, on: boolean) {
  ticked.value = on ? [...ticked.value, unit] : ticked.value.filter((u) => u !== unit)
}
const pillClass = (u: UnitRow) => (u.built < u.total ? 'bg-fail-tint text-fail' : 'bg-ok-tint text-ok')
</script>

<template>
  <div
    data-testid="freeze-picker-backdrop"
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    @click.self="emit('cancel')"
    @keydown.esc="emit('cancel')"
  >
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="freeze-picker-title"
      data-testid="freeze-picker"
      class="flex max-h-[85vh] w-[min(560px,94vw)] flex-col rounded-[14px] border border-border bg-bg-card p-5 text-[13px] text-text-secondary"
    >
      <h3 id="freeze-picker-title" class="m-0 mb-1 text-[15px] font-bold text-text-primary">Freeze units</h3>
      <p class="m-0 mb-3 text-text-muted">Unfreezing later lets the postponed rebuilds start.</p>
      <div class="flex-1 overflow-y-auto">
        <section v-for="g in groups" :key="g.key" class="flex flex-col gap-[6px]">
          <h4 data-testid="unit-group" class="m-0 mt-2 text-[11px] font-bold uppercase tracking-[0.06em] text-text-muted">{{ g.title }}</h4>
          <div
            v-for="u in g.rows"
            :key="u.unit"
            :data-unit="u.unit"
            class="grid grid-cols-[26px_1fr_auto] items-center gap-[10px] rounded-[10px] border bg-bg-card px-3 py-[10px]"
            :class="[selected.includes(u.unit) ? 'border-brand-purple' : 'border-border', disabledReason(u.unit) ? 'opacity-70' : '']"
          >
            <input
              type="checkbox"
              class="h-4 w-4"
              :disabled="!!disabledReason(u.unit)"
              :checked="selected.includes(u.unit)"
              :aria-label="`Select ${u.unit}`"
              @change="toggle(u.unit, ($event.target as HTMLInputElement).checked)"
            />
            <div class="min-w-0">
              <div class="font-mono text-[13px] text-text-primary">{{ u.unit }}</div>
              <div class="mt-[2px] text-[12px] text-text-muted">
                <span v-if="u.members.length">{{ u.members.join(' · ') }}</span>
                <span v-if="disabledReason(u.unit)"><template v-if="u.members.length"> &nbsp;·&nbsp; </template>{{ disabledReason(u.unit) }}</span>
              </div>
            </div>
            <span class="whitespace-nowrap rounded-full px-2 py-[2px] text-[11px]" :class="pillClass(u)">{{ u.built }} / {{ u.total }} built</span>
          </div>
        </section>
      </div>
      <div class="mt-3 flex items-center justify-between gap-3 border-t border-border pt-3">
        <span>{{ selected.length }} unit{{ selected.length === 1 ? '' : 's' }} selected</span>
        <div class="flex gap-2">
          <button ref="cancelBtn" type="button" data-testid="freeze-cancel" class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary" @click="emit('cancel')">Cancel</button>
          <button
            type="button"
            data-testid="freeze-submit"
            class="cursor-pointer rounded-md border border-brand-purple bg-brand-purple px-3 py-[6px] font-semibold text-white disabled:cursor-default disabled:opacity-50"
            :disabled="!selected.length || busy"
            :aria-busy="busy ? 'true' : undefined"
            @click="emit('freeze', [...selected])"
          ><ReleaseSpinner v-if="busy" light class="mr-[6px]" />Freeze {{ selected.length }} unit{{ selected.length === 1 ? '' : 's' }}</button>
        </div>
      </div>
    </div>
  </div>
</template>
