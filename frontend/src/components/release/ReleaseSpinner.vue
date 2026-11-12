<script setup lang="ts">
// `light` is for use on a filled, coloured button.
defineProps<{ light?: boolean }>()
</script>

<template>
  <span data-testid="spinner" aria-hidden="true"
    class="inline-block h-[14px] w-[14px] shrink-0 animate-spin rounded-full border-2 align-[-2px] motion-reduce:animate-none"
    :class="light ? 'border-white/40 border-t-white' : 'border-border-strong border-t-brand-purple'"></span>
</template>
