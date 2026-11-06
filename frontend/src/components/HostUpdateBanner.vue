<script setup lang="ts">
import { useAuth } from '../composables/useAuth'
import { useUpgrades } from '../composables/useUpgrades'
import HostUpdateSteps from './HostUpdateSteps.vue'

const { signedIn } = useAuth()
const { view, blockedRelease } = useUpgrades()
</script>

<template>
  <div
    v-if="signedIn && view && blockedRelease"
    role="alert"
    data-testid="host-update-banner"
    class="flex flex-col gap-3 px-4 py-3 rounded-[10px] border-2 border-warn bg-warn-tint"
  >
    <span class="text-[14px] font-bold text-text-primary">{{ blockedRelease.version }} needs a host update before it can be installed from the UI.</span>
    <HostUpdateSteps :release="blockedRelease" :repo="view.repo" :host-files-version="view.host_files_version" />
  </div>
</template>
