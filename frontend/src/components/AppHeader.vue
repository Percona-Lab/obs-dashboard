<script setup lang="ts">
import type { MainTab } from '../composables/useUrlState'

defineProps<{
  theme: 'light' | 'dark'
  mainTab: MainTab
}>()
const emit = defineEmits<{
  'toggle-theme': []
  'update:main-tab': [tab: MainTab]
}>()
</script>

<template>
  <header class="flex items-center justify-between gap-5">
    <div class="flex items-center gap-3 min-w-0">
      <div class="flex flex-col gap-[1px] min-w-0">
        <h1 class="m-0 truncate text-[17px] sm:text-[21px] font-bold tracking-[-0.01em] text-text-primary">Percona OBS Dashboard</h1>
        <span class="hidden sm:block text-[12.5px] text-text-muted">Failure-first build monitor across every subproject of a product</span>
      </div>
    </div>
    <div class="tab-switcher">
      <button
        class="tab-pill"
        :class="{ active: mainTab === 'overview' }"
        @click="emit('update:main-tab', 'overview')"
      >
        Overview
      </button>
      <button
        class="tab-pill"
        :class="{ active: mainTab === 'board' }"
        @click="emit('update:main-tab', 'board')"
      >
        Builds
      </button>
      <button
        class="tab-pill"
        :class="{ active: mainTab === 'artifacts' }"
        @click="emit('update:main-tab', 'artifacts')"
      >
        Artifacts
      </button>
    </div>
    <button
      @click="emit('toggle-theme')"
      class="shrink-0 inline-flex items-center gap-2 px-[14px] py-2 rounded-[10px] border border-border bg-bg-card text-text-secondary [font-family:inherit] text-[13px] font-semibold cursor-pointer"
    >
      <span class="w-2 h-2 rounded-full bg-brand-purple"></span>
      <span class="hidden sm:inline">{{ theme === 'dark' ? 'Dark' : 'Light' }} mode</span>
    </button>
  </header>
</template>

<style scoped>
.tab-switcher {
  @apply flex gap-[2px] bg-bg-muted p-[3px] rounded-[11px] border border-border;
}

.tab-pill {
  @apply py-[5px] px-[14px] rounded-[8px] text-[13px] font-medium cursor-pointer border border-transparent bg-transparent text-text-muted;
  transition: background 0.15s, color 0.15s, box-shadow 0.15s;
}

.tab-pill.active {
  @apply bg-bg-card text-brand-purple border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)];
}
</style>
