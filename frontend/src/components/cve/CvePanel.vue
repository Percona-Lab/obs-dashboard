<script setup lang="ts">
import { computed, ref } from 'vue'
import type { CveStaging, CveView, ReleasedImage } from '../../types/cveReport'
import { useCveReport } from '../../composables/useCveReport'
import { useOverviewData } from '../../composables/useOverviewData'
import { projectAccent } from '../../lib/overview'
import type { WindowKey } from '../../types/overview'
import CveExposureTable from '../CveExposureTable.vue'
import CveStagingView from './CveStagingView.vue'
import CveReleasedView from './CveReleasedView.vue'

const props = defineProps<{ view: CveView }>()
const emit = defineEmits<{ 'update:view': [v: CveView] }>()

const { report, loading, error, refresh } = useCveReport()
const { projects } = useOverviewData(ref<WindowKey>('24h'))

// Set by the Released view to jump to a Staging row.
const focusRowKey = ref<string | null>(null)

const VIEWS: { key: CveView; label: string }[] = [
  { key: 'staging', label: 'Staging' },
  { key: 'released', label: 'Released' },
  { key: 'images', label: 'By image' },
]

// Same name-sorted accent assignment Overview used.
const accentOrder = computed(() => projects.value.map(p => p.project).sort())
function accentOf(project: string): string {
  return projectAccent(accentOrder.value, project)
}

function selectView(v: CveView) {
  focusRowKey.value = null
  emit('update:view', v)
}

function openStagingRow(key: string) {
  focusRowKey.value = key
  emit('update:view', 'staging')
}

defineExpose({ focusRowKey })
</script>

<template>
  <div class="flex flex-col gap-4 max-w-[1360px] mx-auto w-full px-4 pb-10">
    <div class="flex items-center justify-end gap-[6px]">
      <div class="flex gap-[3px] bg-bg-muted p-[3px] rounded-[9px] border border-border" role="group" aria-label="CVE view">
        <button
          v-for="v in VIEWS"
          :key="v.key"
          type="button"
          class="px-3 py-1 rounded-[7px] border text-[13px] cursor-pointer [font-family:inherit]"
          :class="props.view === v.key
            ? 'bg-bg-card text-text-primary font-bold border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)]'
            : 'bg-transparent text-text-muted font-medium border-transparent'"
          :aria-pressed="props.view === v.key"
          @click="selectView(v.key)"
        >{{ v.label }}</button>
      </div>
    </div>

    <CveExposureTable v-if="props.view === 'images'" :projects="projects" :accent-of="accentOf" />

    <template v-else>
      <div
        v-if="error && !report"
        data-testid="cve-report-error"
        class="flex items-center justify-between gap-3 text-fail bg-fail-tint rounded-[10px] px-4 py-2 text-[13px]"
      >
        <span>{{ error }}</span>
        <button type="button" class="font-semibold cursor-pointer [background:none] border-none text-fail underline" @click="refresh()">Retry</button>
      </div>
      <div v-else-if="!report && loading" class="flex flex-col gap-2 bg-bg-card border border-border rounded-[14px] p-[15px_20px]">
        <div v-for="i in 5" :key="i" class="h-[38px] bg-bg-muted rounded-[8px] animate-pulse" />
      </div>
      <CveStagingView v-else-if="props.view === 'staging' && report" :staging="report.staging as CveStaging" :focus-row-key="focusRowKey" />
      <CveReleasedView v-else-if="props.view === 'released' && report" :released="report.released as ReleasedImage[]" @open-staging-row="openStagingRow" />
    </template>
  </div>
</template>
