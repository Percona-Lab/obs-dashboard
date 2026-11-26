<script setup lang="ts">
import { computed, ref } from 'vue'
import PackageCard from './PackageCard.vue'
import GreenStrip from './GreenStrip.vue'
import type { Package } from '../types/api'

const props = defineProps<{ packages: Package[]; spotlightStates: string[]; activeInstances: string[] }>()

const query = ref('')

const isFailing = (p: Package) => p.rollup_state !== 'succeeded' && p.rollup_state !== 'published'

// Case-insensitive substring filter on the package name; an empty query
// passes everything through.
const visiblePackages = computed(() => {
  const q = query.value.toLowerCase()
  if (!q) return props.packages
  return props.packages.filter(p => p.name.toLowerCase().includes(q))
})

// The header's state pills filter the cards: with states selected only
// packages in those states are shown; with none selected, all are.
const matchesState = (p: Package) =>
  props.spotlightStates.length === 0 || props.spotlightStates.includes(p.rollup_state)

const failingPackages = computed(() => visiblePackages.value.filter(p => isFailing(p) && matchesState(p)))
const okPackages = computed(() => visiblePackages.value.filter(p => !isFailing(p)))
const attentionCount = computed(() => failingPackages.value.length)
const totalFailing = computed(() => props.packages.filter(isFailing).length)
</script>

<template>
  <div class="flex flex-col gap-[14px] min-w-0">
    <!-- Section header -->
    <div class="flex items-center gap-[10px]">
      <h2 class="m-0 text-[15px] font-bold text-text-primary">Active packages</h2>
      <span v-if="query" class="text-[12.5px] text-text-muted">{{ attentionCount }} of {{ totalFailing }} package{{ totalFailing !== 1 ? 's' : '' }} · matching "{{ query }}"</span>
      <span v-else class="text-[12.5px] text-text-muted">{{ attentionCount }} package{{ attentionCount !== 1 ? 's' : '' }} · sorted by severity</span>
      <input
        v-model.trim="query"
        type="search"
        placeholder="filter packages…"
        aria-label="Filter packages by name"
        class="ml-auto w-[200px] bg-bg-card border border-border rounded-[8px] px-[10px] py-[5px] text-[12.5px] text-text-primary placeholder:text-text-muted"
        @keydown.escape="query = ''"
      />
    </div>

    <!-- 2-column failure grid -->
    <div v-if="failingPackages.length > 0" class="grid grid-cols-1 sm:grid-cols-[repeat(2,minmax(0,1fr))] gap-[14px]">
      <PackageCard
        v-for="pkg in failingPackages"
        :key="`${pkg.project}/${pkg.name}`"
        :pkg="pkg"
        :active-instances="activeInstances"
      />
    </div>

    <!-- No-match state (only while searching) -->
    <div v-if="query && visiblePackages.length === 0" class="text-center text-text-muted py-8 text-[13px]">
      No packages match "{{ query }}"
    </div>

    <!-- All green state (only when not searching) -->
    <div v-if="!query && totalFailing === 0 && packages.length > 0" class="bg-ok-tint border border-ok rounded-[12px] p-7 flex flex-col items-center gap-2 text-center">
      <span class="text-[26px] text-ok font-extrabold">✓</span>
      <span class="text-[15px] font-bold text-text-primary">All packages green</span>
    </div>

    <!-- Empty state (only when not searching) -->
    <div v-if="!query && packages.length === 0" class="text-center text-text-muted py-8 text-sm">
      No packages found
    </div>

    <!-- Green strip -->
    <GreenStrip
      v-if="okPackages.length > 0 && spotlightStates.length === 0"
      :packages="okPackages"
    />
  </div>
</template>
