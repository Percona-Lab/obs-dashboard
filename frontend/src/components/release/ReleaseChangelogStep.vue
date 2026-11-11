<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRelease } from '../../composables/useRelease'
import ReleaseConfirm from './ReleaseConfirm.vue'
import { parseChangelog, segments } from '../../lib/changelogPreview'
import type { ReleaseState } from '../../types/release'

const props = defineProps<{ release: ReleaseState }>()
const { saveChangelog, resetChangelog, act, busy } = useRelease()

const active = ref(props.release.units[0]?.unit ?? '')
const text = reactive<Record<string, string>>({})
const synced = reactive<Record<string, string>>({}) // changelog text the editor was last synced with
const syncedHead = reactive<Record<string, string>>({}) // head_sha it came from
const conflict = reactive<Record<string, boolean>>({})

const editable = computed(() => props.release.dashboard)

// Per unit: `synced` is the changelog the editor was last synced with. A unit is in conflict only
// when its changelog changed upstream while it has unsaved edits; a head move alone is not a conflict.
watch(() => props.release, (r) => {
  for (const u of r.units) {
    if (!(u.unit in text)) {
      text[u.unit] = u.changelog; synced[u.unit] = u.changelog; syncedHead[u.unit] = r.head_sha
      continue
    }
    if (u.changelog === text[u.unit]) { // saved, reset or discarded: in line with the snapshot
      synced[u.unit] = u.changelog; syncedHead[u.unit] = r.head_sha; conflict[u.unit] = false
    } else if (u.changelog === synced[u.unit]) { // unchanged upstream, the edits stay
      syncedHead[u.unit] = r.head_sha
    } else if (text[u.unit] === synced[u.unit]) { // changed upstream, no edits: take it
      text[u.unit] = u.changelog; synced[u.unit] = u.changelog; syncedHead[u.unit] = r.head_sha; conflict[u.unit] = false
    } else {
      conflict[u.unit] = true
    }
  }
  if (!r.units.some((u) => u.unit === active.value)) active.value = r.units[0]?.unit ?? ''
}, { immediate: true, deep: true })

const unit = computed(() => props.release.units.find((u) => u.unit === active.value))
const dirty = (name: string) => {
  const u = props.release.units.find((x) => x.unit === name)
  return !!u && (text[name] ?? '') !== u.changelog
}
const anyDirty = computed(() => props.release.units.some((u) => dirty(u.unit)))
const nodes = computed(() => parseChangelog(text[active.value] ?? ''))

async function save() {
  const u = unit.value
  if (!u) return
  await saveChangelog(props.release.id, props.release.head_sha, u.unit, text[u.unit])
}
const resetDialog = ref<{ id: string; head: string; unit: string; releaseId: string } | null>(null)
function reset() {
  const u = unit.value
  if (!u) return
  const target = { id: props.release.id, head: props.release.head_sha, unit: u.unit, releaseId: u.release_id }
  if (dirty(u.unit)) resetDialog.value = target
  else void doReset(target)
}
function confirmReset() {
  const t = resetDialog.value
  resetDialog.value = null
  if (t) void doReset(t)
}
async function doReset(t: { id: string; head: string; unit: string }) {
  if (!(await resetChangelog(t.id, t.head, t.unit))) return
  const cur = props.release.units.find((x) => x.unit === t.unit)
  const gen = cur?.generated ?? cur?.changelog
  if (gen == null) return
  text[t.unit] = gen; synced[t.unit] = gen; syncedHead[t.unit] = props.release.head_sha; conflict[t.unit] = false
}
async function approve() {
  await act(props.release.id, 'approve-changelog', props.release.head_sha)
}
</script>

<template>
  <div data-testid="changelog-step" class="flex flex-col gap-3">
    <div role="tablist" class="flex flex-wrap gap-[6px]">
      <button v-for="u in release.units" :key="u.unit" type="button" role="tab" data-testid="unit-tab" :aria-selected="u.unit === active"
        class="cursor-pointer rounded-t-md border border-border px-3 py-[6px] font-mono text-[12px]"
        :class="u.unit === active ? 'bg-bg-card text-text-primary' : 'bg-bg-muted text-text-muted'" @click="active = u.unit">
        {{ u.release_id }}<span v-if="dirty(u.unit)" data-testid="dirty-dot" title="unsaved edits" class="ml-[6px] inline-block h-[7px] w-[7px] rounded-full bg-warn"></span>
      </button>
    </div>

    <div v-if="unit && conflict[unit.unit]" data-testid="head-changed" class="rounded-[10px] border border-warn bg-warn-tint px-3 py-2 text-text-primary">
      The release changed on GitHub while you were editing. Your text is kept below; the changelog on GitHub now differs from what you started from. Save to overwrite it with your text, or reset to take the new one.
    </div>

    <div v-if="unit" class="grid grid-cols-1 gap-[10px] md:grid-cols-2">
      <div class="rounded-[10px] border border-border p-3">
        <h5 class="m-0 mb-2 text-[11px] font-semibold uppercase tracking-[.06em] text-text-muted">Edit</h5>
        <textarea v-model="text[unit.unit]" data-testid="changelog-editor" :readonly="!editable" spellcheck="false"
          class="min-h-[250px] w-full resize-y border-0 bg-transparent font-mono text-[12px] leading-[1.55] text-text-primary outline-none"></textarea>
      </div>
      <div class="rounded-[10px] border border-border p-3" data-testid="changelog-preview">
        <h5 class="m-0 mb-2 text-[11px] font-semibold uppercase tracking-[.06em] text-text-muted">Preview</h5>
        <template v-for="(n, i) in nodes" :key="i">
          <h4 v-if="n.kind === 'h2'" class="m-0 mb-[6px] text-[15px] text-text-primary">{{ n.text }}</h4>
          <h6 v-else-if="n.kind === 'h3'" class="m-0 mb-1 mt-[10px] text-[12px] text-brand-purple">{{ n.text }}</h6>
          <ul v-else-if="n.kind === 'li'" class="m-0 pl-[18px] text-[12.5px]">
            <li><template v-for="(s, j) in segments(n)" :key="j"><a v-if="s.href" :href="s.href" target="_blank" rel="noopener noreferrer" class="text-brand-purple">{{ s.text }}</a><template v-else>{{ s.text }}</template></template></li>
          </ul>
          <p v-else class="m-0 text-[12.5px]">{{ n.text }}</p>
        </template>
      </div>
    </div>

    <div v-if="editable && unit" class="flex items-center justify-between text-[12px] text-text-muted">
      <span>{{ dirty(unit.unit) ? `Unsaved edits in ${unit.release_id}` : '' }}</span>
      <span class="flex gap-2">
        <button type="button" data-testid="reset-changelog" :disabled="unit.generated === null || busy" class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary disabled:cursor-not-allowed disabled:opacity-50" @click="reset">Reset to generated text</button>
        <button type="button" data-testid="save-changelog" :disabled="!dirty(unit.unit) || busy" class="cursor-pointer rounded-md border border-border bg-transparent px-3 py-[6px] text-text-primary disabled:cursor-not-allowed disabled:opacity-50" @click="save">Save</button>
      </span>
    </div>

    <div v-if="editable" class="flex items-center justify-between gap-3 border-t border-border pt-3 text-[13px] text-text-muted">
      <span>Approving starts the pre-release sync for all units. Staging is frozen while it copies.</span>
      <button type="button" data-testid="approve-changelog" :disabled="anyDirty || busy" class="cursor-pointer rounded-md border-0 bg-brand-purple px-4 py-2 font-semibold text-white disabled:cursor-not-allowed disabled:opacity-50" @click="approve">Approve changelogs &amp; start sync →</button>
    </div>
    <ReleaseConfirm v-if="resetDialog" title="Discard your edits?" :body="`Discard your edits to ${resetDialog.releaseId} and restore the generated text?`" confirm-label="Discard & restore" danger @confirm="confirmReset" @cancel="resetDialog = null" />
  </div>
</template>
