<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRelease } from '../../composables/useRelease'
import ReleaseChangelogStep from './ReleaseChangelogStep.vue'
import ReleaseConfirm from './ReleaseConfirm.vue'
import ReleaseQaStep from './ReleaseQaStep.vue'
import ReleaseRemovals from './ReleaseRemovals.vue'
import ReleaseRunCards from './ReleaseRunCards.vue'
import { safeHref } from '../../lib/safeHref'
import { hasDrafts } from '../../composables/useChangelogDrafts'
import type { ReleaseState } from '../../types/release'

const props = defineProps<{ release: ReleaseState }>()
const emit = defineEmits<{ back: [] }>()
const { act, busy } = useRelease()

const STEPS = ['Select', 'Changelog', 'Pre-release sync', 'QA', 'Release']
const stepIndex = computed(() => ({ cutting: 1, changelog: 1, sync: 2, qa: 3, releasing: 4 })[props.release.step])
const handMade = computed(() => props.release.id.startsWith('pr-'))
const canAct = computed(() => props.release.dashboard)
const cutRunning = computed(() => props.release.cut?.state === 'queued' || props.release.cut?.state === 'running')
// GitHub holds the cut run until a person approves it there: nothing to retry, a dispatch is already waiting.
const cutHeld = computed(() => props.release.step === 'cutting' && props.release.cut?.state === 'approval')
// A failed first cut is shown at step cutting; a failed re-cut leaves the PR as it was, at step changelog.
const cutFailed = computed(() => (props.release.step === 'cutting' || props.release.step === 'changelog') && props.release.cut?.state === 'failure')
// A first cut that failed left no branch and no PR: there is nothing to abandon.
const nothingToAbandon = computed(() => !props.release.pr && !props.release.head_sha && props.release.cut?.state === 'failure')
const showAbandon = computed(() => canAct.value && props.release.step !== 'releasing' && !nothingToAbandon.value && !(cutHeld.value && !props.release.pr))
// The backend refuses Abandon while a cut is queued or running: the cut would re-create the branch.
const abandonBlocked = computed(() => props.release.step === 'cutting' && cutRunning.value)
const showRecut = computed(() => canAct.value && !handMade.value && (props.release.step === 'sync' || props.release.step === 'qa'))
const showRetry = computed(() => canAct.value && !handMade.value && cutFailed.value)
const readOnly = computed(() => ['sync', 'qa', 'releasing'].includes(props.release.step))
// Unsaved changelog text outlives the changelog step (see useChangelogDrafts).
const draftsKept = computed(() => props.release.step !== 'changelog' && hasDrafts(props.release.id))
const ACTION_WORDS: Record<string, string> = {
  cut: 'started the release', save_changelog: 'edited the changelog', approve_changelog: 'approved the changelog',
  retry_sync: 'retried the sync', approve_qa: 'approved the QA run', retry_qa: 'retried QA', recut: 'updated to latest main',
  release: 'released', retry_release: 'retried the final sync', abandon: 'abandoned',
}
const when = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString()
}
const label = computed(() => props.release.units.length ? props.release.units.map((u) => u.release_id).join(' · ') : props.release.id)

// The dialog pins the release id and head the user was looking at when it opened.
const dialog = ref<{ kind: 'abandon' | 'recut'; id: string; head: string } | null>(null)
const openDialog = (kind: 'abandon' | 'recut') => { dialog.value = { kind, id: props.release.id, head: props.release.head_sha } }

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
  if (!d) return
  if (d.kind === 'abandon') {
    if (await act(d.id, 'abandon', d.head)) emit('back')
  } else {
    await act(d.id, 'recut', d.head)
  }
}
const retrySync = () => act(props.release.id, 'retry-sync', props.release.head_sha)
const retryFinal = () => act(props.release.id, 'retry-release', props.release.head_sha)
const syncFailed = computed(() => props.release.sync_state === 'failure')
const STATE_CLS: Record<string, string> = { success: 'text-ok', failure: 'text-fail', running: 'text-warn', queued: 'text-text-muted', skipped: 'text-text-muted' }
const STATE_TXT: Record<string, string> = { success: 'done', failure: 'failed', running: 'running', queued: 'queued', skipped: 'skipped' }
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
        <a v-if="release.pr && safeHref(release.pr_url)" :href="safeHref(release.pr_url)" target="_blank" rel="noopener noreferrer" class="text-brand-purple">PR #{{ release.pr }}</a>
        <small class="text-text-muted">started by {{ release.started_by || 'unknown' }}<template v-if="ago(release.started_at)"> · {{ ago(release.started_at) }}</template></small>
      </div>
      <div class="flex gap-2">
        <button v-if="showRecut" type="button" data-testid="recut" :disabled="busy" class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary disabled:opacity-50" @click="openDialog('recut')">Update to latest main &amp; restart</button>
        <button v-if="showAbandon" type="button" data-testid="abandon" :disabled="busy || abandonBlocked" :title="abandonBlocked ? 'Wait for the cut to finish before abandoning.' : undefined" class="cursor-pointer rounded-md border border-fail bg-transparent px-3 py-[6px] text-fail disabled:cursor-not-allowed disabled:opacity-50" @click="openDialog('abandon')">Abandon release</button>
      </div>
    </div>

    <ul v-if="release.actions.length" data-testid="release-activity" aria-label="Activity" class="m-0 flex list-none flex-col gap-[2px] rounded-[10px] border border-border px-3 py-2 text-[12px] text-text-muted">
      <li v-for="(a, i) in release.actions" :key="i" class="flex flex-wrap gap-x-2">
        <span class="font-mono">{{ when(a.at) }}</span>
        <span><span class="text-text-secondary">{{ a.email || 'unknown' }}</span> {{ ACTION_WORDS[a.action] ?? a.action }}</span>
      </li>
    </ul>

    <div v-if="draftsKept" data-testid="drafts-kept" class="rounded-[10px] border border-warn bg-warn-tint px-3 py-2 text-text-primary">This release moved to another step. Your unsaved changelog text is kept and shown if the release returns to the changelog step.</div>

    <div v-if="!canAct" data-testid="fork-note" class="rounded-[10px] border border-warn bg-warn-tint px-3 py-2 text-text-primary">
      This release PR comes from a fork, so the dashboard cannot change it. Manage it on GitHub.
    </div>

    <ol data-testid="step-bar" class="m-0 flex list-none flex-wrap gap-[6px] p-0">
      <li v-for="(s, i) in STEPS" :key="s" data-testid="step" :data-state="i < stepIndex ? 'done' : i === stepIndex ? 'on' : 'todo'"
        class="min-w-[110px] flex-1 rounded-[8px] border px-[10px] py-2 text-[12px]"
        :class="i < stepIndex ? 'border-ok text-ok' : i === stepIndex ? 'border-brand-purple text-text-primary' : 'border-border text-text-muted'">{{ i + 1 }} · {{ s }}</li>
    </ol>

    <div v-if="cutHeld" data-testid="cut-approval" class="flex flex-col gap-2 rounded-[10px] border border-warn bg-warn-tint px-3 py-2 text-text-primary">
      <span>Waiting for approval on GitHub. A person with write access must approve the workflow run before the cut starts.</span>
      <a v-if="release.cut?.url && safeHref(release.cut.url)" :href="safeHref(release.cut.url)" target="_blank" rel="noopener noreferrer" class="self-start text-brand-purple">Open the run on GitHub</a>
    </div>

    <div v-else-if="release.step === 'cutting'" data-testid="cut-progress" class="rounded-[10px] border border-border p-4">
      <div v-for="l in progress" :key="l.text" class="flex items-center gap-[10px] py-1">
        <span class="w-4 text-center" :class="l.cls">{{ l.icon }}</span><span>{{ l.text }}</span>
      </div>
      <div v-if="cutFailed" data-testid="cut-failed" class="mt-3 flex flex-wrap items-center gap-3 text-fail">
        <b>The cut failed</b>
        <a v-if="release.cut?.url && safeHref(release.cut.url)" :href="safeHref(release.cut.url)" target="_blank" rel="noopener noreferrer" class="text-brand-purple">View the run</a>
        <button v-if="showRetry" type="button" data-testid="retry-cut" :disabled="busy" class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary disabled:opacity-50" @click="retry">Retry</button>
      </div>
    </div>

    <template v-else-if="release.step === 'changelog'">
      <div v-if="cutFailed" data-testid="cut-failed" class="flex flex-col gap-2 rounded-[10px] border border-fail p-3">
        <div class="flex flex-wrap items-center gap-3 text-fail">
          <b>The cut failed</b>
          <a v-if="release.cut?.url && safeHref(release.cut.url)" :href="safeHref(release.cut.url)" target="_blank" rel="noopener noreferrer" class="text-brand-purple">View the run</a>
          <button v-if="showRetry" type="button" data-testid="retry-cut" :disabled="busy" class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary disabled:opacity-50" @click="retry">Retry</button>
        </div>
        <small class="text-text-muted">The release was not updated. The changelog below is the one from before and cannot be edited or approved until a cut succeeds.</small>
      </div>
      <ReleaseChangelogStep :release="release" />
    </template>

    <template v-else>
      <details v-if="readOnly" data-testid="changelog-readonly" class="rounded-[10px] border border-border px-3 py-2">
        <summary class="cursor-pointer text-text-primary">Changelog</summary>
        <div v-for="u in release.units" :key="u.unit" class="mt-2">
          <div class="font-mono text-[12px] text-text-muted">{{ u.release_id }}</div>
          <pre class="m-0 whitespace-pre-wrap font-mono text-[12px] text-text-secondary">{{ u.changelog }}</pre>
        </div>
      </details>
      <div v-if="release.step === 'sync'" data-testid="sync-step" class="flex flex-col gap-3">
        <template v-if="!release.sync.length">
          <div v-if="release.sync_state === 'failure'" data-testid="sync-failed" class="flex flex-wrap items-center gap-3 rounded-[10px] border border-fail p-3 text-fail">
            <span>The pre-release sync failed before any instance reported.</span>
            <a v-if="safeHref(release.sync_url)" :href="safeHref(release.sync_url)" target="_blank" rel="noopener noreferrer" class="text-brand-purple">View the run on GitHub</a>
          </div>
          <div v-else-if="release.sync_state === 'running'" data-testid="sync-waiting" class="rounded-[10px] border border-border p-3 text-text-secondary">The sync is starting…</div>
          <div v-else-if="release.sync_state === 'queued' || !release.sync_state" data-testid="sync-waiting" class="rounded-[10px] border border-border p-3 text-text-secondary">Waiting for the sync to start</div>
        </template>
        <ReleaseRunCards :cards="release.sync" />
        <div v-if="syncFailed && canAct" class="flex items-center gap-3">
          <button type="button" data-testid="retry-sync" :disabled="busy" class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary disabled:opacity-50" @click="retrySync">Retry sync</button>
          <small class="text-text-muted">Or use "Update to latest main &amp; restart" above.</small>
        </div>
        <ReleaseRemovals v-if="release.sync_state === 'success'" :release="release" />
      </div>

      <ReleaseQaStep v-else-if="release.step === 'qa'" :release="release" />

      <div v-else-if="release.step === 'releasing'" data-testid="final-step" class="flex flex-col gap-3">
        <div class="text-text-primary"><b>PR #{{ release.pr }} merged</b></div>
        <div v-if="release.final?.tags.length" data-testid="final-tags" class="flex flex-wrap gap-2">
          <span v-for="t in release.final.tags" :key="t" class="rounded-md border border-border px-2 py-[2px] font-mono text-[12px] text-text-secondary">{{ t }}</span>
        </div>
        <ReleaseRunCards :cards="release.final?.instances ?? []" />
        <div v-if="release.final" class="flex flex-col gap-1 text-[13px]">
          <div data-testid="final-github-releases">Publish the GitHub releases: <span :class="STATE_CLS[release.final.github_releases]">{{ STATE_TXT[release.final.github_releases] ?? 'pending' }}</span></div>
          <div data-testid="final-cleanup">Delete the pre-release: <span :class="STATE_CLS[release.final.cleanup]">{{ STATE_TXT[release.final.cleanup] ?? 'pending' }}</span></div>
          <a v-if="safeHref(release.final.url)" :href="safeHref(release.final.url)" target="_blank" rel="noopener noreferrer" class="text-brand-purple">View the run on GitHub</a>
        </div>
        <div v-if="release.final?.state === 'failure'" data-testid="final-failed" class="text-fail">The final sync failed.</div>
        <div v-if="release.final?.state === 'failure' && canAct">
          <button type="button" data-testid="retry-final" :disabled="busy" class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary disabled:opacity-50" @click="retryFinal">Retry final sync</button>
        </div>
      </div>
    </template>

    <ReleaseConfirm v-if="dialog?.kind === 'abandon'" title="Abandon this release?" body="The release PR will be closed and the release branch deleted. The units become selectable again." confirm-label="Abandon release" danger @confirm="confirm" @cancel="dialog = null" />
    <ReleaseConfirm v-if="dialog?.kind === 'recut'" title="Update to latest main and restart?" body="The release is cut again on top of the latest main and all workflows re-run. Your changelog edits will be regenerated, so you edit and approve again." confirm-label="Update & restart" @confirm="confirm" @cancel="dialog = null" />
  </div>
</template>
