<script setup lang="ts">
import { computed } from 'vue'
import { hostUpdateCommands, type ReleaseView } from '../composables/useUpgrades'

const props = defineProps<{ release: ReleaseView; repo: string; hostFilesVersion: string }>()
const commands = computed(() => hostUpdateCommands(props.repo, props.release.requires_host))
const guide = computed(() => `https://github.com/${props.repo}/blob/${props.release.requires_host}/deploy/host/README.md`)
</script>

<template>
  <div data-testid="host-update-steps" class="flex flex-col gap-2 text-[12.5px]">
    <div v-for="i in release.host_instructions" :key="i.version" data-testid="host-instruction" class="flex flex-col gap-[2px]">
      <span class="font-semibold text-text-primary">Host changes in {{ i.version }}</span>
      <span class="whitespace-pre-wrap text-text-secondary">{{ i.host_changes }}</span>
    </div>
    <span class="text-text-secondary">Run as root on the host:</span>
    <pre data-testid="host-commands" class="m-0 overflow-x-auto rounded-[8px] border border-border bg-bg-app p-3 font-mono text-[12px] text-text-primary">{{ commands }}</pre>
    <span class="text-text-muted">
      If your host is managed by a deployment tool, make the equivalent change there instead, so it is not reverted on the
      next run. The dashboard restarts with the new host files, and this notice clears.
    </span>
    <span class="text-text-muted">
      Installed host files:
      <span data-testid="host-files-version" class="font-mono">{{ hostFilesVersion || 'unknown — set OBS_DASHBOARD_HOST_FILES_VERSION' }}</span>
      · <a :href="guide" target="_blank" rel="noopener" class="text-brand-purple">Host install guide</a>
    </span>
  </div>
</template>
