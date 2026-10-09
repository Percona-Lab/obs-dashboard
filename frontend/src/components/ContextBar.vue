<script setup lang="ts">
import type { Context } from '../types/api'
import { useInstances } from '../composables/useInstances'
import VersionSelector from './VersionSelector.vue'
import { versionKeyProject } from '../lib/versions'

defineProps<{
  version: string
  updatedAt: string | null
  refreshing: boolean
  activeTags: string[]
  contexts: Context[]
  selectedContext: Context
  availableVersions: string[]
  activeInstances: string[]
}>()

const emit = defineEmits<{
  'update:version': [version: string]
  'toggle-tag': [tag: string]
  'toggle-instance': [slug: string]
  'update:context': [ctx: Context]
  'refresh': []
}>()

const { instances, multi } = useInstances()

const TAGS = [
  { id: 'ppg', label: 'PPG' },
  { id: 'common', label: 'Common' },
  { id: 'container', label: 'Container' },
  { id: 'tarball', label: 'Tarball' },
]

function formatTime(iso: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const now = new Date()
  const isToday = d.toDateString() === now.toDateString()
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return `${time} · ${isToday ? 'today' : d.toLocaleDateString()}`
}

function tagStyle(_id: string, active: boolean): string {
  return active
    ? 'background: var(--brand-purple-tint); color: var(--brand-purple); padding: 3px 10px; border-radius: 8px; border: 2px solid var(--brand-purple); font-size: 11.5px; font-weight: 600; cursor: pointer; font-family: inherit;'
    : 'background: transparent; color: var(--text-secondary); padding: 4px 11px; border-radius: 8px; border: 1px solid var(--border); font-size: 11.5px; font-weight: 500; cursor: pointer; font-family: inherit;'
}
</script>

<template>
  <div class="bg-bg-card border border-border rounded-[14px] px-[18px] py-[14px] flex flex-col gap-[13px]">
    <!-- Top row: tech badge + context selector + version tabs + updated -->
    <div class="flex items-center gap-2 sm:gap-4 flex-wrap">
      <span class="inline-flex items-center px-3 py-[5px] rounded-lg bg-[var(--tint-postgres)] text-[var(--tech-postgres)] text-xs font-bold border border-[rgba(0,94,214,0.15)]">
        PostgreSQL
      </span>

      <!-- Context selector: dropdown when multiple contexts exist; the path badge always follows -->
      <select
        v-if="contexts.length > 1"
        :value="selectedContext.apiBase"
        @change="e => { const apiBase = (e.target as HTMLSelectElement).value; const ctx = contexts.find(c => c.apiBase === apiBase); if (ctx) emit('update:context', ctx) }"
        class="[font-family:var(--font-mono)] text-[12.5px] text-text-secondary bg-bg-muted px-[10px] py-[5px] rounded-[7px] border border-border cursor-pointer"
      >
        <option v-for="ctx in contexts" :key="ctx.apiBase" :value="ctx.apiBase">{{ ctx.prefix }}</option>
      </select>
      <code
        class="[font-family:var(--font-mono)] text-[12.5px] text-text-secondary bg-bg-muted px-[10px] py-[5px] rounded-[7px]"
      >{{ versionKeyProject(selectedContext.prefix, availableVersions.includes(version) ? version : '') }}</code>

      <!-- Version selector: hidden when no versioned packages exist in the context -->
      <VersionSelector
        v-if="availableVersions.length > 0"
        :keys="availableVersions"
        :model-value="version"
        allow-all
        @update:model-value="emit('update:version', $event)"
      />

      <div class="ml-auto flex items-center gap-4 text-xs text-text-muted">
        <span
          title="Click to refresh"
          class="cursor-pointer select-none"
          @click="emit('refresh')"
        >Updated <strong :style="`color: ${refreshing ? 'var(--text-muted)' : 'var(--text-secondary)'}; font-weight: 600;`">{{ refreshing ? 'Refreshing…' : formatTime(updatedAt) }}</strong></span>
        <span class="inline-flex items-center gap-1.5">
          <span class="w-[7px] h-[7px] rounded-full bg-[var(--ok)]"></span>Live
        </span>
      </div>
    </div>

    <!-- Tag pills -->
    <div class="flex items-center gap-[9px] flex-wrap border-t border-border pt-3">
      <span class="text-[11px] text-text-muted font-semibold uppercase tracking-[0.06em] mr-0.5">Tags</span>
      <button
        v-for="t in TAGS"
        :key="t.id"
        @click="emit('toggle-tag', t.id)"
        :style="tagStyle(t.id, activeTags.includes(t.id))"
      >{{ t.label }}</button>
    </div>

    <!-- Instance chips -->
    <div v-if="multi" class="flex items-center gap-[9px] flex-wrap">
      <span class="text-[11px] text-text-muted font-semibold uppercase tracking-[0.06em] mr-0.5">Instances</span>
      <button
        v-for="inst in instances"
        :key="inst.slug"
        @click="emit('toggle-instance', inst.slug)"
        :style="tagStyle(inst.slug, activeInstances.includes(inst.slug))"
      >{{ inst.name }}</button>
    </div>
  </div>
</template>
