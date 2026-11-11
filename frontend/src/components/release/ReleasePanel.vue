<script setup lang="ts">
import { computed } from 'vue'
import { useRelease } from '../../composables/useRelease'

const { snapshot } = useRelease()
const inFlight = computed(() => snapshot.value?.releases.length ?? 0)
</script>

<template>
  <section data-testid="release-panel" class="rounded-[14px] border border-border bg-bg-card p-5 text-[13px] text-text-secondary">
    <span data-testid="release-in-flight">{{ inFlight }} release{{ inFlight === 1 ? '' : 's' }} in flight</span>
  </section>
</template>
