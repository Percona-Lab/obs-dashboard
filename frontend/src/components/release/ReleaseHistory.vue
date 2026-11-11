<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRelease } from '../../composables/useRelease'
import { parseChangelog, segments, type PreviewNode } from '../../lib/changelogPreview'
import type { HistoryRow } from '../../types/release'

const PAGE_SIZE = 20
const { loadHistory } = useRelease()

const rows = ref<HistoryRow[]>([])
const page = ref(0)
const more = ref(false)
const loading = ref(false)
const open = ref<string | null>(null)

async function loadNext() {
  if (loading.value) return
  loading.value = true
  const got = await loadHistory(page.value + 1)
  loading.value = false
  if (got.length) page.value += 1
  rows.value = [...rows.value, ...got]
  more.value = got.length >= PAGE_SIZE
}
onMounted(loadNext)

const date = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString()
}
const nodes = (md: string) => parseChangelog(md).map((n, id) => ({ ...n, id }))
const segs = (n: PreviewNode) => segments(n).map((s, id) => ({ ...s, id }))
const safeHref = (h: string) => /^https?:\/\//.test(h) ? h : undefined
</script>

<template>
  <div data-testid="release-history" class="flex flex-col gap-[6px]">
    <div v-if="!rows.length && !loading" class="text-[13px] text-text-muted">No finished releases yet.</div>
    <div
      v-for="r in rows"
      :key="r.tag"
      data-testid="history-row"
      class="rounded-[10px] border border-border bg-bg-card px-3 py-[10px]"
    >
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px]">
        <button
          data-testid="history-toggle"
          type="button"
          class="cursor-pointer border-0 bg-transparent p-0 text-left font-mono text-text-primary"
          :aria-expanded="open === r.tag"
          @click="open = open === r.tag ? null : r.tag"
        >{{ open === r.tag ? '▾' : '▸' }} {{ r.release_id }}</button>
        <span class="font-mono text-[12px] text-text-muted">{{ r.unit }}</span>
        <span class="text-[12px] text-text-muted">{{ date(r.released_at) }}</span>
        <span v-if="r.released_by" class="text-[12px] text-text-muted">by {{ r.released_by }}</span>
        <span v-if="r.failed_lanes?.length" class="rounded-full bg-warn-tint px-2 py-[2px] text-[11px] text-warn">released with failed QA</span>
        <span class="ml-auto flex gap-3 text-[12px]">
          <a v-if="safeHref(r.release_url)" :href="r.release_url" target="_blank" rel="noopener noreferrer" class="text-brand-purple">GitHub release</a>
          <a v-if="r.pr_url && safeHref(r.pr_url)" :href="r.pr_url" target="_blank" rel="noopener noreferrer" class="text-brand-purple">PR</a>
        </span>
      </div>
      <div v-if="open === r.tag" data-testid="history-notes" class="mt-2 flex flex-col gap-1 border-t border-border pt-2 text-[12.5px] text-text-secondary">
        <template v-for="n in nodes(r.notes)" :key="n.id">
          <component :is="n.kind === 'li' ? 'div' : n.kind === 'p' ? 'p' : n.kind" class="m-0" :class="[n.kind === 'h2' ? 'font-bold text-text-primary' : n.kind === 'h3' ? 'font-semibold' : '', n.kind === 'li' ? 'pl-3' : '']">
            <template v-if="n.kind === 'li'">• </template>
            <template v-for="s in segs(n)" :key="s.id">
              <a v-if="s.href" :href="s.href" target="_blank" rel="noopener noreferrer" class="text-brand-purple">{{ s.text }}</a>
              <template v-else>{{ s.text }}</template>
            </template>
          </component>
        </template>
      </div>
    </div>
    <button
      v-if="more"
      data-testid="history-more"
      type="button"
      class="cursor-pointer self-start rounded-[8px] border border-border bg-bg-card px-3 py-[6px] text-[12px] font-semibold text-text-secondary disabled:opacity-50"
      :disabled="loading"
      @click="loadNext"
    >Load more</button>
  </div>
</template>
