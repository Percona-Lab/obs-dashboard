<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRelease } from '../../composables/useRelease'
import ReleaseChangelogStep from './ReleaseChangelogStep.vue'
import ReleaseConfirm from './ReleaseConfirm.vue'
import type { ReleaseState } from '../../types/release'

const props = defineProps<{ release: ReleaseState }>()
const emit = defineEmits<{ back: [] }>()
const { act, busy } = useRelease()

const STEPS = ['Select', 'Changelog', 'Pre-release sync', 'QA', 'Release']
const stepIndex = computed(() => ({ cutting: 1, changelog: 1, sync: 2, qa: 3, releasing: 4 })[props.release.step])
const handMade = computed(() => props.release.id.startsWith('pr-'))
const canAct = computed(() => props.release.dashboard)
const cutRunning = computed(() => props.release.cut?.state === 'queued' || props.release.cut?.state === 'running')
const cutFailed = computed(() => props.release.step === 'cutting' && props.release.cut?.state === 'failure')
const showAbandon = computed(() => canAct.value && props.release.step !== 'releasing' && !(props.release.step === 'cutting' && cutRunning.value))
const showRecut = computed(() => canAct.value && !handMade.value && (props.release.step === 'sync' || props.release.step === 'qa'))
const showRetry = computed(() => canAct.value && !handMade.value && cutFailed.value)
const readOnly = computed(() => ['sync', 'qa', 'releasing'].includes(props.release.step))
const label = computed(() => props.release.units.length ? props.release.units.map((u) => u.release_id).join(' · ') : props.release.id)

const dialog = ref<'abandon' | 'recut' | null>(null)

function ago(iso: string | null): string {
  const t = iso ? new Date(iso).getTime() : NaN
  if (Number.isNaN(t)) return ''
  const s = Math.max(0, Math.round((Date.now() - t) / 1000))
  if (s < 60) return `${s} s ago`
  if (s < 3600) return `${Math.round(s / 60)} min ago`
  if (s < 86400) return `${Math.round(s / 3600)} h ago`
  return `${Math.round(s / 86400)} d ago`
}

async function confirm() {
  const d = dialog.value
  dialog.value = null
  if (d === 'abandon') {
    if (await act(props.release.id, 'abandon', props.release.head_sha)) emit('back')
  } else if (d === 'recut') {
    await act(props.release.id, 'recut', props.release.head_sha)
  }
}
const retry = () => act(props.release.id, 'recut', props.release.head_sha)

type Line = { icon: string; cls: string; text: string }
const progress = computed<Line[]>(() => {
  const r = props.release
  const st = r.cut?.state
  const gen: Line = st === 'success' ? { icon: '✓', cls: 'text-ok', text: 'Generating release files and changelogs' }
    : st === 'failure' ? { icon: '✗', cls: 'text-fail', text: 'Generating release files and changelogs' }
    : st === 'running' ? { icon: '◐', cls: 'text-warn', text: 'Generating release files and changelogs' }
    : { icon: '○', cls: 'text-text-muted', text: 'Generating release files and changelogs' }
  return [
    { icon: r.cut ? '✓' : '◐', cls: r.cut ? 'text-ok' : 'text-warn', text: 'Release branch created' },
    gen,
    { icon: r.pr ? '✓' : '○', cls: r.pr ? 'text-ok' : 'text-text-muted', text: 'Opening the release PR' },
  ]
})
</script>

<template>
  <div data-testid="release-wizard" class="flex flex-col gap-4 rounded-[14px] border border-border bg-bg-card p-5">
    <button type="button" data-testid="release-back" class="cursor-pointer self-start border-0 bg-transparent p-0 text-[12px] font-semibold text-brand-purple" @click="emit('back')">← Back</button>

    <div data-testid="release-strip" class="flex flex-wrap items-center justify-between gap-2 rounded-[10px] border border-border px-3 py-[10px]">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
        <b class="font-mono text-text-primary">{{ label }}</b>
        <a v-if="release.pr && release.pr_url" :href="release.pr_url" target="_blank" rel="noopener noreferrer" class="text-brand-purple">PR #{{ release.pr }}</a>
        <small class="text-text-muted">started by {{ release.started_by || 'unknown' }}<template v-if="ago(release.started_at)"> · {{ ago(release.started_at) }}</template></small>
      </div>
      <div class="flex gap-2">
        <button v-if="showRecut" type="button" data-testid="recut" :disabled="busy" class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary disabled:opacity-50" @click="dialog = 'recut'">Update to latest main &amp; restart</button>
        <button v-if="showAbandon" type="button" data-testid="abandon" :disabled="busy" class="cursor-pointer rounded-md border border-fail bg-transparent px-3 py-[6px] text-fail disabled:opacity-50" @click="dialog = 'abandon'">Abandon release</button>
      </div>
    </div>

    <div v-if="!canAct" data-testid="fork-note" class="rounded-[10px] border border-warn bg-warn-tint px-3 py-2 text-text-primary">
      This release PR comes from a fork, so the dashboard cannot change it. Manage it on GitHub.
    </div>

    <ol data-testid="step-bar" class="m-0 flex list-none flex-wrap gap-[6px] p-0">
      <li v-for="(s, i) in STEPS" :key="s" data-testid="step" :data-state="i < stepIndex ? 'done' : i === stepIndex ? 'on' : 'todo'"
        class="min-w-[110px] flex-1 rounded-[8px] border px-[10px] py-2 text-[12px]"
        :class="i < stepIndex ? 'border-ok text-ok' : i === stepIndex ? 'border-brand-purple text-text-primary' : 'border-border text-text-muted'">{{ i + 1 }} · {{ s }}</li>
    </ol>

    <div v-if="release.step === 'cutting'" data-testid="cut-progress" class="rounded-[10px] border border-border p-4">
      <div v-for="l in progress" :key="l.text" class="flex items-center gap-[10px] py-1">
        <span class="w-4 text-center" :class="l.cls">{{ l.icon }}</span><span>{{ l.text }}</span>
      </div>
      <div v-if="cutFailed" data-testid="cut-failed" class="mt-3 flex flex-wrap items-center gap-3 text-fail">
        <b>The cut failed</b>
        <a v-if="release.cut?.url" :href="release.cut.url" target="_blank" rel="noopener noreferrer" class="text-brand-purple">View the run</a>
        <button v-if="showRetry" type="button" data-testid="retry-cut" :disabled="busy" class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary disabled:opacity-50" @click="retry">Retry</button>
      </div>
    </div>

    <ReleaseChangelogStep v-else-if="release.step === 'changelog'" :release="release" />

    <template v-else>
      <details v-if="readOnly" data-testid="changelog-readonly" class="rounded-[10px] border border-border px-3 py-2">
        <summary class="cursor-pointer text-text-primary">Changelog</summary>
        <div v-for="u in release.units" :key="u.unit" class="mt-2">
          <div class="font-mono text-[12px] text-text-muted">{{ u.release_id }}</div>
          <pre class="m-0 whitespace-pre-wrap font-mono text-[12px] text-text-secondary">{{ u.changelog }}</pre>
        </div>
      </details>
      <!-- steps 3-5: Task 13 -->
    </template>

    <ReleaseConfirm v-if="dialog === 'abandon'" title="Abandon this release?" body="The release PR will be closed and the release branch deleted. The units become selectable again." confirm-label="Abandon release" danger @confirm="confirm" @cancel="dialog = null" />
    <ReleaseConfirm v-if="dialog === 'recut'" title="Update to latest main and restart?" body="The release is cut again on top of the latest main and all workflows re-run. Your changelog edits will be regenerated, so you edit and approve again." confirm-label="Update & restart" @confirm="confirm" @cancel="dialog = null" />
  </div>
</template>
