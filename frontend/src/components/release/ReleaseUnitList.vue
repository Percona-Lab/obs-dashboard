<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { UnitRow } from '../../types/release'

const props = defineProps<{ units: UnitRow[]; busy: boolean; error: string }>()
const emit = defineEmits<{ cut: [units: string[]]; open: [releaseId: string]; 'open-builds': [] }>()

const ticked = ref<string[]>([])

// Unit roots look like ppg:staging:18[:extras]; the third segment is the major.
function major(unit: string): string {
  return unit.split(':')[2] ?? 'common'
}

const groups = computed(() => {
  const m = new Map<string, UnitRow[]>()
  for (const u of props.units) {
    const k = major(u.unit)
    m.set(k, [...(m.get(k) ?? []), u])
  }
  const keys = [...m.keys()].sort((a, b) => {
    if (a === 'common') return 1
    if (b === 'common') return -1
    return (parseInt(b, 10) || 0) - (parseInt(a, 10) || 0)
  })
  return keys.map((k) => ({ key: k, title: /^\d+$/.test(k) ? `PostgreSQL ${k}` : 'Common', rows: m.get(k)! }))
})

// Drop ticks on units that are no longer tickable, so one that is blocked and
// later tickable again is not silently re-ticked.
const isTickable = (u: string) => props.units.some((r) => r.unit === u && r.tickable)
watch(() => props.units, () => { ticked.value = ticked.value.filter(isTickable) })
const selected = computed(() => ticked.value.filter(isTickable))

function toggle(unit: string, on: boolean) {
  ticked.value = on ? [...ticked.value, unit] : ticked.value.filter((u) => u !== unit)
}

const alertClass: Record<string, string> = {
  not_built: 'border-fail bg-fail-tint',
  in_flight: 'border-warn bg-warn-tint',
  unchanged: 'border-border-strong bg-bg-muted',
}
const pillClass = (u: UnitRow) => {
  if (u.blocked?.reason === 'in_flight') return 'bg-warn-tint text-warn'
  if (u.blocked?.reason === 'unchanged') return 'bg-bg-muted text-text-muted'
  return u.built < u.total ? 'bg-fail-tint text-fail' : 'bg-ok-tint text-ok'
}
</script>

<template>
  <div data-testid="unit-list" class="flex flex-col gap-2">
    <div v-if="error" data-testid="units-error" class="rounded-[10px] border border-fail bg-fail-tint px-3 py-2 text-[13px] text-fail">
      Could not load the release units: {{ error }}
    </div>
    <template v-else>
      <div v-if="!units.length" class="text-[13px] text-text-muted">No release units.</div>
      <section v-for="g in groups" :key="g.key" class="flex flex-col gap-[6px]">
        <h4 data-testid="unit-group" class="m-0 mt-2 text-[11px] font-bold uppercase tracking-[0.06em] text-text-muted">{{ g.title }}</h4>
        <div
          v-for="u in g.rows"
          :key="u.unit"
          :data-unit="u.unit"
          class="grid grid-cols-[26px_1fr_auto] items-center gap-[10px] rounded-[10px] border bg-bg-card px-3 py-[10px]"
          :class="[selected.includes(u.unit) ? 'border-brand-purple' : 'border-border', u.tickable ? '' : 'opacity-70']"
        >
          <input
            type="checkbox"
            class="h-4 w-4"
            :disabled="!u.tickable"
            :checked="selected.includes(u.unit)"
            :aria-label="`Select ${u.unit}`"
            @change="toggle(u.unit, ($event.target as HTMLInputElement).checked)"
          />
          <div class="min-w-0">
            <div class="font-mono text-[13px] text-text-primary">{{ u.unit }}</div>
            <div class="mt-[2px] text-[12px] text-text-muted">
              <span v-if="u.members.length">{{ u.members.join(' · ') }} &nbsp;·&nbsp; </span>last released {{ u.last_release || 'never' }}
            </div>
          </div>
          <span class="whitespace-nowrap rounded-full px-2 py-[2px] text-[11px]" :class="pillClass(u)">{{ u.built }} / {{ u.total }} built</span>
          <div
            v-if="u.blocked"
            class="col-span-2 col-start-2 flex flex-wrap items-center gap-2 rounded-[4px] border-l-[3px] px-[10px] py-[6px] text-[12px] text-text-primary"
            :class="alertClass[u.blocked.reason]"
          >
            <span>{{ u.blocked.text }}<template v-if="u.blocked.reason === 'not_built' && (u.blocked.packages ?? []).length">: <b class="font-mono">{{ (u.blocked.packages ?? []).join(', ') }}</b></template></span>
            <button
              v-if="u.blocked.reason === 'in_flight' && u.blocked.release_id"
              data-testid="open-release"
              type="button"
              class="cursor-pointer border-0 bg-transparent p-0 text-[12px] font-semibold text-brand-purple underline"
              @click="emit('open', u.blocked.release_id!)"
            >Continue that release</button>
            <button
              v-if="u.blocked.reason === 'not_built'"
              data-testid="open-builds"
              type="button"
              class="cursor-pointer border-0 bg-transparent p-0 text-[12px] font-semibold text-brand-purple underline"
              @click="emit('open-builds')"
            >Open in Builds</button>
          </div>
        </div>
      </section>
      <div v-if="units.length" class="mt-3 flex items-center justify-between gap-3 border-t border-border pt-3 text-[13px] text-text-muted">
        <span>{{ selected.length }} unit{{ selected.length === 1 ? '' : 's' }} selected<template v-if="selected.length > 1"> · they go out together in one release</template></span>
        <button
          data-testid="cut-release"
          type="button"
          class="cursor-pointer rounded-[8px] border border-border-strong bg-bg-card px-4 py-2 text-[13px] font-semibold text-brand-purple disabled:cursor-default disabled:opacity-50"
          :disabled="!selected.length || busy"
          @click="emit('cut', [...selected])"
        >Cut release</button>
      </div>
    </template>
  </div>
</template>
