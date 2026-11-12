<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useServerLog } from '../composables/useServerLog'

const { lines, live, paused, start, stop } = useServerLog()
const errorsOnly = ref(false)
const view = ref<HTMLElement | null>(null)
const atBottom = ref(true)

const shown = computed(() => (errorsOnly.value ? lines.value.filter(l => l.level !== 'plain') : lines.value))

function scrollToBottom(): void {
  const el = view.value
  if (el) el.scrollTop = el.scrollHeight
  atBottom.value = true
}

function onScroll(): void {
  const el = view.value
  if (el) atBottom.value = el.scrollHeight - el.scrollTop - el.clientHeight <= 24
}

watch(shown, async () => {
  if (!atBottom.value) return
  await nextTick()
  scrollToBottom()
})

onMounted(start)
onUnmounted(stop)
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="flex items-center gap-3 text-[12.5px]">
      <span data-testid="log-state" class="font-semibold" :class="live ? 'text-text-primary' : 'text-text-muted'">{{ live ? 'Live' : 'Reconnecting' }}</span>
      <button
        data-testid="log-pause"
        class="text-[12px] font-semibold px-[10px] py-[4px] rounded-[8px] border border-border bg-bg-card text-text-secondary cursor-pointer"
        @click="paused = !paused"
      >{{ paused ? 'Resume' : 'Pause' }}</button>
      <label class="flex items-center gap-2 text-text-secondary cursor-pointer">
        <input v-model="errorsOnly" data-testid="log-errors-only" type="checkbox" />
        Errors and warnings only
      </label>
      <button
        v-if="!atBottom"
        data-testid="log-jump"
        class="ml-auto text-[12px] font-semibold px-[10px] py-[4px] rounded-[8px] border border-border bg-bg-card text-text-secondary cursor-pointer"
        @click="scrollToBottom"
      >Jump to latest</button>
    </div>
    <div
      ref="view"
      data-testid="log-view"
      class="h-[calc(100vh-220px)] min-h-[320px] overflow-auto rounded-[14px] border border-border bg-bg-card p-3"
      @scroll="onScroll"
    >
      <div
        v-for="l in shown"
        :key="l.id"
        data-testid="log-line"
        class="font-mono text-[12px] whitespace-pre-wrap break-all"
        :class="l.level === 'error' ? 'text-fail' : l.level === 'warn' ? 'text-warn' : 'text-text-secondary'"
      >{{ l.text }}</div>
    </div>
  </div>
</template>
