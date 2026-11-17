<script setup lang="ts">
import { computed } from 'vue'
import type { CveStaging } from '../../types/cveReport'
import { allSeenKeys } from '../../lib/cveReport'
import { useCveSeen } from '../../composables/useCveSeen'
import { useCveRescan } from '../../composables/useCveRescan'
import { useAuth } from '../../composables/useAuth'
import CveWorklistTable from './CveWorklistTable.vue'

const props = defineProps<{ staging: CveStaging; focusRowKey: string | null }>()

const { available, markAllSeen } = useCveSeen()
const { signedIn } = useAuth()
const { rescanBusy, rescanMsg, rescanFailed, rescan } = useCveRescan()

const sections = computed(() => [
  { key: 'ours', title: 'Ours to bump', rows: props.staging.ours, first: 'Package to bump', fix: 'Fix', empty: 'Nothing to bump.' },
  { key: 'base_os', title: 'Base OS', rows: props.staging.base_os, first: 'Package', fix: 'Fix available', empty: 'No base OS fixes pending.' },
  { key: 'unclassified', title: 'Not classified yet', rows: props.staging.unclassified, first: 'Package', fix: 'Fix available', empty: '' },
].filter(s => s.key !== 'unclassified' || s.rows.length > 0))
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="flex items-center gap-[10px] flex-wrap">
      <span data-testid="cve-staging-header" class="text-[13px] text-text-secondary">{{ staging.ours.length }} things to bump · {{ staging.cves }} CVEs · {{ staging.images }} images</span>
      <span
        v-if="signedIn && rescanMsg"
        data-testid="cve-rescan-status"
        role="status"
        class="ml-auto text-[12px]"
        :class="rescanFailed ? 'text-fail' : 'text-text-muted'"
      >{{ rescanMsg }}</span>
      <button
        v-if="signedIn"
        type="button"
        data-testid="cve-rescan"
        class="px-[10px] py-[4px] rounded-[8px] border border-border bg-bg-card text-text-secondary text-[12px] font-semibold cursor-pointer disabled:opacity-50 disabled:cursor-default"
        :class="rescanMsg ? '' : 'ml-auto'"
        :disabled="rescanBusy"
        title="Scan again every image that currently has CVEs"
        @click="rescan"
      >Rescan affected</button>
      <button
        v-if="available"
        type="button"
        data-testid="cve-mark-seen"
        class="px-[10px] py-[4px] rounded-[8px] border border-border bg-bg-card text-text-secondary text-[12px] font-semibold cursor-pointer"
        :class="signedIn ? '' : 'ml-auto'"
        @click="markAllSeen(allSeenKeys(staging))"
      >Mark all as seen</button>
    </div>

    <div v-for="s in sections" :key="s.key" class="bg-bg-card border border-border rounded-[14px] overflow-hidden">
      <div class="p-[15px_20px] border-b border-border flex items-baseline gap-[10px]">
        <h2 class="m-0 text-[14px] font-bold">{{ s.title }}</h2>
        <span class="text-[12px] text-text-muted">{{ s.rows.length }}</span>
      </div>
      <div v-if="s.rows.length === 0" data-testid="cve-empty" class="p-[15px_20px] text-[12px] text-text-muted">{{ s.empty }}</div>
      <CveWorklistTable v-else :rows="s.rows" :first-column="s.first" :fix-column="s.fix" :focus-row-key="focusRowKey" />
    </div>
  </div>
</template>
