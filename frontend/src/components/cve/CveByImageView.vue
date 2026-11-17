<script setup lang="ts">
import { computed, ref } from 'vue'
import { useOverviewData } from '../../composables/useOverviewData'
import { projectAccent } from '../../lib/overview'
import type { WindowKey } from '../../types/overview'
import CveExposureTable from '../CveExposureTable.vue'

const { projects } = useOverviewData(ref<WindowKey>('24h'))

// Same name-sorted accent assignment Overview used.
const accentOrder = computed(() => projects.value.map(p => p.project).sort())
function accentOf(project: string): string {
  return projectAccent(accentOrder.value, project)
}
</script>

<template>
  <CveExposureTable :projects="projects" :accent-of="accentOf" />
</template>
