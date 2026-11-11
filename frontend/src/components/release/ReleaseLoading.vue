<script setup lang="ts">
import ReleaseSpinner from './ReleaseSpinner.vue'
</script>

<template>
  <div role="status" data-testid="release-loading" class="flex items-center gap-2 text-[13px] text-text-muted">
    <ReleaseSpinner />
    <span><slot /></span>
  </div>
</template>
