<script setup lang="ts">
import { computed } from 'vue'
import type { ReleaseState } from '../../types/release'

const props = defineProps<{ release: ReleaseState }>()
const groups = computed(() => {
  const m = new Map<string, string[]>()
  for (const r of props.release.removals) (m.get(r.project) ?? m.set(r.project, []).get(r.project)!).push(r.item)
  return [...m].map(([project, items]) => ({ project, items }))
})
const n = computed(() => props.release.removals.length)
</script>

<template>
  <div data-testid="removals" class="rounded-[10px] border border-border p-3 text-[13px]">
    <div v-if="!n" class="text-text-secondary">Nothing will be removed</div>
    <template v-else>
      <div class="mb-2 text-text-primary">Releasing will remove {{ n }} {{ n === 1 ? 'item' : 'items' }} from {{ groups.length === 1 ? groups[0].project : `${groups.length} projects` }}</div>
      <div v-for="g in groups" :key="g.project" class="mb-1">
        <div class="font-mono text-[12px] text-text-muted">{{ g.project }}</div>
        <ul class="m-0 list-disc pl-5 font-mono text-[12px] text-text-secondary"><li v-for="i in g.items" :key="i">{{ i }}</li></ul>
      </div>
    </template>
  </div>
</template>
