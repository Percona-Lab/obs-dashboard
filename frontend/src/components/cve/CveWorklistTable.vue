<script setup lang="ts">
import { computed, reactive } from 'vue'
import type { CveRow } from '../../types/cveReport'
import { KIND_LABEL, imagesSummary, sortRows } from '../../lib/cveReport'
import { useCveSeen } from '../../composables/useCveSeen'

const props = defineProps<{
  rows: CveRow[]
  firstColumn: string
  fixColumn: string
  focusRowKey: string | null
}>()

const { isNew } = useCveSeen()

const sorted = computed(() => sortRows(props.rows, isNew))

// Keyed by row key (not index) so expansion survives report refetches.
const expanded = reactive<Record<string, boolean>>({})

function isOpen(key: string): boolean {
  return expanded[key] ?? props.focusRowKey === key
}

function toggle(key: string) {
  expanded[key] = !isOpen(key)
}

function regionId(key: string): string {
  return `cve-row-${key.replace(/[^a-z0-9]/gi, '-')}`
}

function rowFresh(row: CveRow): boolean {
  return row.entries.some(e => isNew(e.seen_key))
}

function sevClass(sev: string): string {
  return sev === 'CRITICAL' ? 'text-crit bg-crit-tint' : 'text-high bg-high-tint'
}

const COLS = 'sm:grid-cols-[minmax(0,1.6fr)_minmax(0,2fr)_70px_90px_minmax(0,1.4fr)]'
</script>

<template>
  <div>
    <div
      class="hidden sm:grid gap-3 items-center p-[9px_20px] bg-bg-card-2 border-b border-border text-[10.5px] font-bold uppercase tracking-[0.05em] text-text-muted"
      :class="COLS"
    >
      <span>{{ firstColumn }}</span>
      <span>{{ fixColumn }}</span>
      <span class="text-center">CVEs</span>
      <span class="text-center">Worst</span>
      <span>Images affected</span>
    </div>

    <template v-for="row in sorted" :key="row.key">
      <button
        type="button"
        :data-testid="`cve-row-${row.key}`"
        class="w-full text-left p-[11px_20px] border-b border-border grid grid-cols-1 gap-1 sm:gap-3 items-center border-l-[3px]"
        :class="[COLS, isOpen(row.key) ? 'bg-bg-card-2' : '', rowFresh(row) ? 'border-l-warn' : 'border-l-transparent']"
        :aria-expanded="isOpen(row.key)"
        :aria-controls="regionId(row.key)"
        @click="toggle(row.key)"
      >
        <span class="flex items-center gap-2 min-w-0">
          <span
            class="text-text-muted text-[11px] transition-transform inline-block"
            :class="isOpen(row.key) ? 'rotate-90' : ''"
          >▸</span>
          <span class="font-mono text-[13.5px] font-semibold truncate">{{ row.name }}</span>
          <span class="text-[11px] text-text-muted whitespace-nowrap">{{ KIND_LABEL[row.kind] }}</span>
          <span v-if="row.vendor" class="text-[11px] text-text-muted whitespace-nowrap">{{ row.vendor }}</span>
          <span
            v-if="rowFresh(row)"
            data-testid="cve-new-tag"
            class="inline-block px-[6px] py-[1px] rounded-full bg-warn-tint text-warn text-[10.5px] font-bold leading-[1.4]"
          >NEW</span>
        </span>
        <span class="flex flex-col font-mono text-[12.5px] text-text-secondary min-w-0">
          <span v-for="f in row.fixes" :key="f.pkg" class="break-words">{{ f.pkg }} {{ f.installed }} → {{ f.fixed }}</span>
        </span>
        <span class="sm:text-center font-mono text-[13px] font-semibold">{{ row.cves }}<span class="sm:hidden text-[11px] text-text-muted font-normal"> CVEs</span></span>
        <span
          class="justify-self-start sm:justify-self-center text-[12px] font-bold px-2 py-0.5 rounded-md"
          :class="sevClass(row.worst)"
        >{{ row.worst }}</span>
        <span class="text-[12px] text-text-muted">{{ imagesSummary(row.images) }}</span>
      </button>

      <div
        v-show="isOpen(row.key)"
        :id="regionId(row.key)"
        class="bg-bg-card-2 border-b border-border pl-10 pr-5 py-3 flex flex-col gap-3"
      >
        <div class="flex flex-col gap-1">
          <div
            v-for="e in row.entries"
            :key="e.seen_key"
            data-testid="cve-entry"
            class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 text-[12px]"
          >
            <span class="font-mono font-bold text-text-primary">{{ e.id }}</span>
            <span class="text-[10px] font-bold uppercase px-[6px] py-[1px] rounded-md" :class="sevClass(e.severity)">{{ e.severity }}</span>
            <span class="font-mono text-text-secondary">{{ e.pkg }}</span>
            <span v-if="e.path" class="font-mono text-text-muted">{{ e.path }}</span>
            <span class="text-text-secondary">{{ e.title }}</span>
            <span
              v-if="isNew(e.seen_key)"
              data-testid="cve-new-tag"
              class="inline-block px-[6px] py-[1px] rounded-full bg-warn-tint text-warn text-[10.5px] font-bold leading-[1.4]"
            >NEW</span>
          </div>
        </div>
        <div class="flex flex-col gap-1 border-t border-border pt-2">
          <div
            v-for="i in row.images"
            :key="`${i.project}|${i.package}|${i.repo}`"
            class="font-mono text-[12px] text-text-secondary"
          >{{ i.project }} · {{ i.package }} · {{ i.repo }} · {{ i.arches.join(', ') }}</div>
        </div>
      </div>
    </template>
  </div>
</template>
