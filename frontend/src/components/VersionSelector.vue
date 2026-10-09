<script setup lang="ts">
import { computed } from 'vue'
import { splitVersionKey, selectorVersions, keyForVersion } from '../lib/versions'

// Two-level version selector: a version row (major numbers, then literal
// pseudo-versions such as "common", optionally followed by All) and a
// subproject row for the selected version, shown only when it has more
// than one key. Keys are the flat list from deriveVersionKeys; the emitted
// value is always one of them (or '' for All).
const props = defineProps<{
  keys: string[]
  modelValue: string
  allowAll?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [key: string]
}>()

const versions = computed(() => selectorVersions(props.keys))
const selectedVersion = computed(() => (props.modelValue ? splitVersionKey(props.modelValue)[0] : ''))
const subKeys = computed(() => props.keys.filter(k => splitVersionKey(k)[0] === selectedVersion.value))
const showSubprojects = computed(() => selectedVersion.value !== '' && subKeys.value.length > 1)

function subLabel(key: string): string {
  return splitVersionKey(key)[1] ?? 'base'
}

function selectVersion(version: string) {
  const key = keyForVersion(props.keys, version)
  if (key !== undefined) emit('update:modelValue', key)
}

const CHIP = 'px-3 py-1 rounded-[7px] border text-[13px] cursor-pointer [font-family:inherit]'
const ACTIVE = 'bg-bg-card text-text-primary font-bold border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)]'
const INACTIVE = 'bg-transparent text-text-muted font-medium border-transparent'

function chipClass(active: boolean): string {
  return `${CHIP} ${active ? ACTIVE : INACTIVE}`
}
</script>

<template>
  <div class="flex items-center gap-2 sm:gap-4 flex-wrap">
    <div class="flex items-center gap-[6px]" data-row="version">
      <span class="text-[11px] text-text-muted font-semibold uppercase [letter-spacing:0.06em] mr-[2px]">Version</span>
      <div class="flex gap-[3px] bg-bg-muted p-[3px] rounded-[9px] border border-border">
        <button
          v-for="v in versions"
          :key="v"
          type="button"
          :class="chipClass(v === selectedVersion)"
          @click="selectVersion(v)"
        >{{ v }}</button>
        <button
          v-if="allowAll"
          type="button"
          :class="chipClass(modelValue === '')"
          @click="emit('update:modelValue', '')"
        >All</button>
      </div>
    </div>

    <div v-if="showSubprojects" class="flex items-center gap-[6px]" data-row="subproject">
      <span class="text-[11px] text-text-muted font-semibold uppercase [letter-spacing:0.06em] mr-[2px]">Subproject</span>
      <div class="flex gap-[3px] bg-bg-muted p-[3px] rounded-[9px] border border-border">
        <button
          v-for="k in subKeys"
          :key="k"
          type="button"
          :class="chipClass(k === modelValue)"
          @click="emit('update:modelValue', k)"
        >{{ subLabel(k) }}</button>
      </div>
    </div>
  </div>
</template>
