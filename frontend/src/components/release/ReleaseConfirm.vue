<script setup lang="ts">
import { onMounted, ref } from 'vue'

defineProps<{ title: string; body: string; confirmLabel: string; danger?: boolean }>()
const emit = defineEmits<{ confirm: []; cancel: [] }>()
const cancelBtn = ref<HTMLButtonElement | null>(null)
onMounted(() => cancelBtn.value?.focus())
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50" @click.self="emit('cancel')" @keydown.esc="emit('cancel')">
    <div role="dialog" aria-modal="true" data-testid="release-confirm" class="w-[min(440px,92vw)] rounded-[14px] border border-border bg-bg-card p-5 text-[13px] text-text-secondary">
      <h3 class="m-0 mb-2 text-[15px] font-bold text-text-primary">{{ title }}</h3>
      <p class="m-0 mb-4">{{ body }}</p>
      <div class="flex justify-end gap-2">
        <button ref="cancelBtn" type="button" data-testid="confirm-cancel" class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary" @click="emit('cancel')">Cancel</button>
        <button type="button" data-testid="confirm-ok" class="cursor-pointer rounded-md border px-3 py-[6px] font-semibold" :class="danger ? 'border-fail text-fail bg-transparent' : 'border-brand-purple bg-brand-purple text-white'" @click="emit('confirm')">{{ confirmLabel }}</button>
      </div>
    </div>
  </div>
</template>
