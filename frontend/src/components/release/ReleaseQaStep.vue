<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRelease } from '../../composables/useRelease'
import { safeHref } from '../../lib/safeHref'
import ReleaseSpinner from './ReleaseSpinner.vue'
import ReleaseConfirm from './ReleaseConfirm.vue'
import ReleaseRemovals from './ReleaseRemovals.vue'
import type { Lane, ReleaseState } from '../../types/release'

const props = defineProps<{ release: ReleaseState }>()
const { act, busy, pending } = useRelease()

const qa = computed(() => props.release.qa)
const lanes = computed(() => qa.value?.lanes ?? [])
const failed = computed(() => lanes.value.filter((l) => l.state === 'failure'))
// Lanes that are neither passed nor failed while QA is finished should not happen; treat them like failures.
const unfinished = computed(() => qa.value?.state === 'finished' ? lanes.value.filter((l) => l.state !== 'success' && l.state !== 'failure') : [])
const passed = computed(() => lanes.value.filter((l) => l.state === 'success'))
const running = computed(() => lanes.value.filter((l) => l.state === 'queued' || l.state === 'running'))
const canAct = computed(() => props.release.dashboard)
const skipped = computed(() => !!qa.value?.skipped)
// Release needs QA to have finished, or a user to have skipped it.
const releasable = computed(() => qa.value?.state === 'finished' || skipped.value)
const canRelease = computed(() => canAct.value && releasable.value && !busy.value)
const canSkip = computed(() => canAct.value && !!qa.value && !releasable.value)

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
const hasBad = computed(() => failed.value.length > 0 || unfinished.value.length > 0)
const openRelease = () => { dialog.value = { id: props.release.id, head: props.release.head_sha, confirmFailed: hasBad.value } }
const releaseBody = computed(() => {
  const parts: string[] = []
  if (qa.value?.state !== 'finished') parts.push('QA was skipped for this release.')
  else if (!lanes.value.length) parts.push('No QA tests ran for this release.')
  if (failed.value.length) parts.push(`These QA lanes failed: ${failed.value.map((l) => l.name).join(', ')}.`)
  if (unfinished.value.length) parts.push(`These QA lanes did not finish: ${unfinished.value.map((l) => l.name).join(', ')}.`)
  parts.push(parts.length ? 'The release merges the PR and cannot be undone.' : 'This merges the release PR and cannot be undone.')
  return parts.join(' ')
})
async function doRelease() {
  const d = dialog.value
  dialog.value = null
  // QA may have gone back to running while the dialog was open.
  if (d && releasable.value) await act(d.id, 'release', d.head, d.confirmFailed)
}
const approve = () => act(props.release.id, 'approve-qa', props.release.head_sha)
const retryQa = () => act(props.release.id, 'retry-qa', props.release.head_sha)
const qaFailed = computed(() => qa.value?.state === 'failure')
// Without QA: the release was skipped and QA has not finished since.
const withoutQa = computed(() => qa.value?.state !== 'finished')
// Pins the release id and head the user saw when the dialog opened.
const skipDialog = ref<{ id: string; head: string } | null>(null)
async function doSkip() {
  const d = skipDialog.value
  skipDialog.value = null
  if (d) await act(d.id, 'skip-qa', d.head)
}
</script>

<template>
  <div class="flex flex-col gap-3">
    <div v-if="skipped && withoutQa" data-testid="qa-skipped" role="status" class="rounded-[10px] border border-fail p-3 text-fail">
      <b>QA skipped{{ qa?.skipped_by ? ` by ${qa.skipped_by}` : '' }}.</b> This version of the release PR can be released without QA.
    </div>

    <template v-if="qa?.state === 'waiting' || !qa">
      <ReleaseRemovals :release="release" />
      <div v-if="qa" class="flex items-center gap-3">
        <button v-if="canAct" type="button" data-testid="approve-qa" :disabled="busy" class="inline-flex items-center gap-2 cursor-pointer rounded-md border border-brand-purple bg-brand-purple px-3 py-[6px] font-semibold text-white disabled:opacity-50" @click="approve" :aria-busy="pending === 'approve-qa' ? 'true' : undefined"><ReleaseSpinner v-if="pending === 'approve-qa'" light />Approve &amp; run QA</button>
        <small class="text-text-muted">Adds the qa-release label to the PR.</small>
      </div>
    </template>

    <template v-else>
      <div v-if="qaFailed" data-testid="qa-failed" class="flex flex-col gap-2 rounded-[10px] border border-fail p-3">
        <div class="flex flex-wrap items-center gap-3 text-fail">
          <b>{{ qa.error || 'QA could not start.' }}</b>
          <a v-if="safeHref(qa.url)" :href="safeHref(qa.url)" target="_blank" rel="noopener noreferrer" class="text-brand-purple">View the run on GitHub</a>
        </div>
        <div v-if="canAct" class="flex flex-wrap items-center gap-3">
          <button type="button" data-testid="retry-qa" :disabled="busy" class="inline-flex items-center gap-2 cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary disabled:opacity-50" @click="retryQa" :aria-busy="pending === 'retry-qa' ? 'true' : undefined"><ReleaseSpinner v-if="pending === 'retry-qa'" />Retry QA</button>
          <small class="text-text-muted">Or use "Update to latest main &amp; restart" above.</small>
        </div>
      </div>

      <div v-if="qa.state === 'running' && !lanes.length" data-testid="qa-starting" role="status" class="flex items-center gap-2 rounded-[10px] border border-border p-3 text-text-secondary"><ReleaseSpinner /><span>Waiting for QA to start</span></div>
      <div v-else-if="qa.state === 'finished' && !lanes.length" data-testid="qa-none" class="rounded-[10px] border border-border p-3 text-text-secondary">No QA tests are defined for this release, so nothing ran. You can release it.</div>
      <div v-else-if="!qaFailed || lanes.length" data-testid="qa-counts" class="flex gap-4 text-[13px]">
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
          <ReleaseSpinner v-if="l.state === 'queued' || l.state === 'running'" /><span v-else class="text-warn">◐</span><span class="font-mono">{{ l.name }}</span>
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
    </template>

    <template v-if="qa && (qa.state !== 'waiting' || skipped)">
      <div v-if="canAct" class="flex items-center gap-3">
        <button type="button" data-testid="release" :disabled="!canRelease" class="inline-flex items-center gap-2 cursor-pointer rounded-md border border-brand-purple bg-brand-purple px-3 py-[6px] font-semibold text-white disabled:cursor-not-allowed disabled:opacity-50" @click="openRelease" :aria-busy="pending === 'release' ? 'true' : undefined"><ReleaseSpinner v-if="pending === 'release'" light />Release</button>
        <small v-if="!releasable && qaFailed" class="text-text-muted">Not available: QA did not run.</small>
        <small v-else-if="!releasable" class="text-text-muted">Available once every QA lane has finished.</small>
      </div>
    </template>

    <div v-if="canSkip" class="flex items-center gap-3">
      <button type="button" data-testid="skip-qa" :disabled="busy" class="inline-flex items-center gap-2 cursor-pointer rounded-md border border-fail bg-transparent px-3 py-[6px] text-fail disabled:opacity-50" @click="skipDialog = { id: release.id, head: release.head_sha }" :aria-busy="pending === 'skip-qa' ? 'true' : undefined"><ReleaseSpinner v-if="pending === 'skip-qa'" />Skip QA</button>
      <small class="text-text-muted">Makes "Release" available without QA. Asks for confirmation.</small>
    </div>

    <ReleaseConfirm v-if="dialog" :title="withoutQa ? 'Release without QA?' : hasBad ? 'Release with failed lanes?' : 'Release now?'" :body="releaseBody" :confirm-label="withoutQa ? 'Release without QA' : hasBad ? 'Release anyway' : 'Release'" :danger="hasBad || withoutQa" @confirm="doRelease" @cancel="dialog = null" />
    <ReleaseConfirm v-if="skipDialog" title="Skip QA?" warning="The packages of this version of the release PR will not be tested." body="Skip QA only when it already passed on an earlier iteration of this release PR. After this, Release is available without QA, and a comment on the PR says that QA was skipped. Updating to latest main asks for QA again." confirm-label="Skip QA" danger @confirm="doSkip" @cancel="skipDialog = null" />
  </div>
</template>
