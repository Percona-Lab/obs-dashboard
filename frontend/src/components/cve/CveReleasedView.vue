<script setup lang="ts">
import { reactive } from 'vue'
import type { ReleasedImage } from '../../types/cveReport'

defineProps<{ released: ReleasedImage[] }>()
const emit = defineEmits<{ 'open-staging-row': [key: string] }>()

const expanded = reactive<Record<string, boolean>>({})

function imageKey(i: ReleasedImage): string {
  return `${i.project}|${i.package}|${i.repo}`
}

function toggle(key: string) {
  expanded[key] = !expanded[key]
}

function regionId(key: string): string {
  return `cve-released-${key.replace(/[^a-z0-9]/gi, '-')}`
}

// ppg:releases:18:containers -> 18
function pgMajor(project: string): string {
  return project.split(':')[2] ?? ''
}

// "ours|golang" -> "golang"
function stagingRowName(key: string): string {
  return key.slice(key.indexOf('|') + 1)
}

function sevClass(sev: string): string {
  return sev === 'CRITICAL' ? 'text-crit bg-crit-tint' : 'text-high bg-high-tint'
}

const COLS = 'sm:grid-cols-[minmax(0,2fr)_minmax(0,1.2fr)_70px_100px_minmax(0,1.2fr)]'
</script>

<template>
  <div class="bg-bg-card border border-border rounded-[14px] overflow-hidden">
    <div class="p-[15px_20px] border-b border-border flex items-baseline gap-[10px]">
      <h2 class="m-0 text-[14px] font-bold">Released images</h2>
      <span class="text-[12px] text-text-muted">{{ released.length }}</span>
    </div>
    <div v-if="released.length === 0" data-testid="cve-empty" class="p-[15px_20px] text-[12px] text-text-muted">No released images have CVEs.</div>
    <div v-else>
      <div
        class="hidden sm:grid gap-3 items-center p-[9px_20px] bg-bg-card-2 border-b border-border text-[10.5px] font-bold uppercase tracking-[0.05em] text-text-muted"
        :class="COLS"
      >
        <span>Released image</span>
        <span>Released version</span>
        <span class="text-center">CVEs</span>
        <span class="text-center">Fixed in staging</span>
        <span>Staging version</span>
      </div>

      <template v-for="img in released" :key="imageKey(img)">
        <button
          type="button"
          class="w-full text-left p-[11px_20px] border-b border-border grid grid-cols-1 gap-1 sm:gap-3 items-center border-l-[3px] border-l-transparent"
          :class="[COLS, expanded[imageKey(img)] ? 'bg-bg-card-2' : '']"
          :aria-expanded="!!expanded[imageKey(img)]"
          :aria-controls="regionId(imageKey(img))"
          @click="toggle(imageKey(img))"
        >
          <span class="flex items-center gap-2 min-w-0">
            <span
              class="text-text-muted text-[11px] transition-transform inline-block"
              :class="expanded[imageKey(img)] ? 'rotate-90' : ''"
            >▸</span>
            <span class="font-mono text-[13.5px] font-semibold truncate">{{ img.package }}</span>
            <span class="text-[11px] text-text-muted whitespace-nowrap">{{ pgMajor(img.project) }} · {{ img.repo }}</span>
          </span>
          <span class="font-mono text-[12.5px] text-text-secondary break-words">{{ img.version }}</span>
          <span class="sm:text-center font-mono text-[13px] font-semibold">{{ img.total }}<span class="sm:hidden text-[11px] text-text-muted font-normal"> CVEs</span></span>
          <span class="sm:text-center font-mono text-[13px] font-semibold">{{ img.fixed_in_staging }} of {{ img.total }}</span>
          <span class="font-mono text-[12.5px] text-text-secondary break-words">{{ img.staging_version || '—' }}</span>
        </button>

        <div
          v-show="expanded[imageKey(img)]"
          :id="regionId(imageKey(img))"
          class="bg-bg-card-2 border-b border-border pl-10 pr-5 py-3 flex flex-col gap-1"
        >
          <div
            v-for="c in img.cves"
            :key="`${c.id}|${c.pkg}`"
            data-testid="cve-entry"
            class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 text-[12px]"
          >
            <span class="font-mono font-bold text-text-primary">{{ c.id }}</span>
            <span class="text-[10px] font-bold uppercase px-[6px] py-[1px] rounded-md" :class="sevClass(c.severity)">{{ c.severity }}</span>
            <span class="font-mono text-text-secondary">{{ c.pkg }}</span>
            <span class="font-mono text-text-muted">{{ c.installed }} → {{ c.fixed }}</span>
            <span v-if="c.status === 'fixed'" class="text-ok font-semibold">Fixed in staging<template v-if="img.staging_version"> · {{ img.staging_version }}</template></span>
            <span v-else-if="c.status === 'open'" class="flex items-baseline gap-2">
              <span class="text-warn font-semibold">Still open in staging</span>
              <button
                v-if="c.staging_row_key"
                type="button"
                class="font-mono text-text-secondary underline cursor-pointer [background:none] border-none p-0 text-[12px]"
                @click="emit('open-staging-row', c.staging_row_key)"
              >{{ stagingRowName(c.staging_row_key) }}</button>
            </span>
            <span v-else class="text-text-muted">No staging image to compare</span>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
