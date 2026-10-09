<script setup lang="ts">
import type { Context } from '../types/api'
import { contextToKey } from '../composables/useUrlState'
import VersionSelector from './VersionSelector.vue'
import { versionKeyProject } from '../lib/versions'

defineProps<{
  version: string
  availableVersions: string[]
  activeTab: 'packages' | 'containers' | 'tarballs'
  contexts: Context[]
  selectedContext: Context
}>()

const emit = defineEmits<{
  'update:version': [v: string]
  'update:tab': [tab: 'packages' | 'containers' | 'tarballs']
  'update:context': [ctx: Context]
}>()
</script>

<template>
  <div class="bg-bg-card border border-border rounded-[14px] px-[18px] py-[14px] mt-3 mx-4 flex-shrink-0">
    <div class="flex items-center gap-2 sm:gap-4 flex-wrap">
      <!-- PostgreSQL badge -->
      <span class="inline-flex items-center gap-[7px] px-3 py-[5px] rounded-[8px] bg-[var(--tint-postgres)] text-[var(--tech-postgres)] text-[12px] font-bold border border-[rgba(0,94,214,0.15)]">PostgreSQL</span>

      <!-- Context dropdown when multiple contexts. Key options by contextToKey — stable, unique per context (labels
           and apiBases may change). -->
      <select
        v-if="contexts.length > 1"
        class="font-mono text-[12.5px] text-text-secondary bg-bg-muted px-[10px] py-[5px] rounded-[7px] border-none cursor-pointer [appearance:auto]"
        :value="contextToKey(selectedContext)"
        @change="emit('update:context', contexts.find(c => contextToKey(c) === ($event.target as HTMLSelectElement).value)!)"
      >
        <option
          v-for="ctx in contexts"
          :key="contextToKey(ctx)"
          :value="contextToKey(ctx)"
        >{{ ctx.label }}</option>
      </select>
      <!-- Selected project path -->
      <code class="font-mono text-[12.5px] text-text-secondary bg-bg-muted px-[10px] py-[5px] rounded-[7px]">
        {{ versionKeyProject(selectedContext.prefix, availableVersions.includes(version) ? version : '') }}
      </code>

      <!-- Version selector -->
      <VersionSelector
        v-if="availableVersions.length > 0"
        :keys="availableVersions"
        :model-value="version"
        @update:model-value="emit('update:version', $event)"
      />

      <!-- Tab switcher -->
      <div class="flex items-center gap-[6px] ml-auto">
        <div class="flex gap-[3px] bg-bg-muted p-[3px] rounded-[9px] border border-border">
          <button
            class="px-3 py-1 rounded-[7px] border text-[13px] cursor-pointer [font-family:inherit]"
            :class="activeTab === 'packages'
              ? 'bg-bg-card text-text-primary font-bold border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)]'
              : 'bg-transparent text-text-muted font-medium border-transparent'"
            @click="emit('update:tab', 'packages')"
          >Packages</button>
          <button
            class="px-3 py-1 rounded-[7px] border text-[13px] cursor-pointer [font-family:inherit]"
            :class="activeTab === 'containers'
              ? 'bg-bg-card text-text-primary font-bold border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)]'
              : 'bg-transparent text-text-muted font-medium border-transparent'"
            @click="emit('update:tab', 'containers')"
          >Container Images</button>
          <button
            class="px-3 py-1 rounded-[7px] border text-[13px] cursor-pointer [font-family:inherit]"
            :class="activeTab === 'tarballs'
              ? 'bg-bg-card text-text-primary font-bold border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)]'
              : 'bg-transparent text-text-muted font-medium border-transparent'"
            @click="emit('update:tab', 'tarballs')"
          >Tarballs</button>
        </div>
      </div>
    </div>
  </div>
</template>
