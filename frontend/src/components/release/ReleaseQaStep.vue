<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRelease } from '../../composables/useRelease'
import { safeHref } from '../../lib/safeHref'
import ReleaseConfirm from './ReleaseConfirm.vue'
import ReleaseRemovals from './ReleaseRemovals.vue'
import type { Lane, ReleaseState } from '../../types/release'

const props = defineProps<{ release: ReleaseState }>()
const { act, busy } = useRelease()

const qa = computed(() => props.release.qa)
const lanes = computed(() => qa.value?.lanes ?? [])
const failed = computed(() => lanes.value.filter((l) => l.state === 'failure'))
const passed = computed(() => lanes.value.filter((l) => l.state === 'success'))
const running = computed(() => lanes.value.filter((l) => l.state === 'queued' || l.state === 'running'))
const canAct = computed(() => props.release.dashboard)
const canRelease = computed(() => canAct.value && qa.value?.state === 'finished' && !busy.value)

interface Group { key: string; title: string; failed: Lane[]; passed: Lane[]; other: Lane[] }
const groups = computed<Group[]>(() => {
  const titleOf = (unit: string) => props.release.units.find((u) => u.unit === unit)?.release_id || unit
  const map = new Map<string, Group>()
  for (const l of lanes.value) {
    const key = l.unit
    let g = map.get(key)
    if (!g) { g = { key, title: key ? titleOf(key) : 'Other', failed: [], passed: [], other: [] }; map.set(key, g) }
    if (l.state === 'failure') g.failed.push(l)
    else if (l.state === 'success') g.passed.push(l)
    else g.other.push(l)
  }
  const all = [...map.values()]
  // The "Other" group goes last.
  return [...all.filter((g) => g.key), ...all.filter((g) => !g.key)]
})
const showPassed = ref<Record<string, boolean>>({})

function link(l: Lane): { text: string; href: string | undefined } {
  return l.jenkins_url
    ? { text: 'Jenkins job ↗', href: safeHref(l.jenkins_url) }
    : { text: 'GitHub job ↗', href: safeHref(l.url) }
}

// Pins the release id, head and failed-lane state the user saw when the dialog opened.
const dialog = ref<{ id: string; head: string; confirmFailed: boolean } | null>(null)
const openRelease = () => { dialog.value = { id: props.release.id, head: props.release.head_sha, confirmFailed: failed.value.length > 0 } }
const releaseBody = computed(() => failed.value.length
  ? `These QA lanes failed: ${failed.value.map((l) => l.name).join(', ')}. The release merges the PR and cannot be undone.`
  : 'This merges the release PR and cannot be undone.')
async function doRelease() {
  const d = dialog.value
  dialog.value = null
  if (d) await act(d.id, 'release', d.head, d.confirmFailed)
}
const approve = () => act(props.release.id, 'approve-qa', props.release.head_sha)
</script>

<template>
  <div class="flex flex-col gap-3">
    <template v-if="qa?.state === 'waiting' || !qa">
      <ReleaseRemovals :release="release" />
      <div v-if="qa" class="flex items-center gap-3">
        <button v-if="canAct" type="button" data-testid="approve-qa" :disabled="busy" class="cursor-pointer rounded-md border border-brand-purple bg-brand-purple px-3 py-[6px] font-semibold text-white disabled:opacity-50" @click="approve">Approve &amp; run QA</button>
        <small class="text-text-muted">Adds the qa-release label to the PR.</small>
      </div>
    </template>

    <template v-else>
      <div data-testid="qa-counts" class="flex gap-4 text-[13px]">
        <span class="text-ok">{{ passed.length }} passed</span>
        <span class="text-fail">{{ failed.length }} failed</span>
        <span class="text-warn">{{ running.length }} running</span>
      </div>

      <div v-for="g in groups" :key="g.key || 'other'" data-testid="qa-group" class="rounded-[10px] border border-border p-3">
        <div data-testid="qa-group-title" class="mb-2 font-mono text-[12px] font-bold text-text-primary">{{ g.title }}</div>
        <div v-for="l in g.failed" :key="l.name" data-testid="lane-failed" class="flex items-center gap-3 py-1 text-[13px]">
          <span class="text-fail">✗</span><span class="font-mono">{{ l.name }}</span>
          <a v-if="link(l).href" :href="link(l).href" target="_blank" rel="noopener noreferrer" class="ml-auto text-[12px] text-brand-purple">{{ link(l).text }}</a>
        </div>
        <div v-for="l in g.other" :key="l.name" data-testid="lane-running" class="flex items-center gap-3 py-1 text-[13px] text-text-secondary">
          <span class="text-warn">◐</span><span class="font-mono">{{ l.name }}</span>
        </div>
        <template v-if="g.passed.length">
          <button type="button" data-testid="toggle-passed" class="cursor-pointer border-0 bg-transparent p-0 text-[12px] text-brand-purple" @click="showPassed[g.key] = !showPassed[g.key]">{{ g.passed.length }} passed {{ g.passed.length === 1 ? 'lane' : 'lanes' }} · {{ showPassed[g.key] ? 'hide' : 'show' }}</button>
          <div v-if="showPassed[g.key]">
            <div v-for="l in g.passed" :key="l.name" data-testid="lane-passed" class="flex items-center gap-3 py-1 text-[13px] text-text-secondary">
              <span class="text-ok">✓</span><span class="font-mono">{{ l.name }}</span>
              <a v-if="link(l).href" :href="link(l).href" target="_blank" rel="noopener noreferrer" class="ml-auto text-[12px] text-brand-purple">{{ link(l).text }}</a>
            </div>
          </div>
        </template>
      </div>

      <ReleaseRemovals :release="release" />

      <p v-if="failed.length" class="m-0 text-[12px] text-text-muted">Fix the failures outside the dashboard, then use "Update to latest main &amp; restart" above, or release anyway after a confirmation.</p>

      <div v-if="canAct" class="flex items-center gap-3">
        <button type="button" data-testid="release" :disabled="!canRelease" class="cursor-pointer rounded-md border border-brand-purple bg-brand-purple px-3 py-[6px] font-semibold text-white disabled:cursor-not-allowed disabled:opacity-50" @click="openRelease">Release</button>
        <small v-if="qa.state !== 'finished'" class="text-text-muted">Available once every QA lane has finished.</small>
      </div>
    </template>

    <ReleaseConfirm v-if="dialog" :title="failed.length ? 'Release with failed lanes?' : 'Release now?'" :body="releaseBody" :confirm-label="failed.length ? 'Release anyway' : 'Release'" :danger="failed.length > 0" @confirm="doRelease" @cancel="dialog = null" />
  </div>
</template>
