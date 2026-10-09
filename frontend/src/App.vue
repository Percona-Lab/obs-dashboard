<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import AppHeader from './components/AppHeader.vue'
import ContextBar from './components/ContextBar.vue'
import HealthHeader from './components/HealthHeader.vue'
import MainGrid from './components/MainGrid.vue'
import ArtifactsPanel from './components/ArtifactsPanel.vue'
import OverviewPanel from './components/OverviewPanel.vue'
import type { Context } from './types/api'
import type { WindowKey } from './types/overview'
import { PPG_DEVEL_CONTEXT, PPG_STAGING_CONTEXT, RELEASES_CONTEXT, prArtifactsContexts } from './lib/contexts'
import { usePackages } from './composables/usePackages'
import { useEvents } from './composables/useEvents'
import { usePRPackages } from './composables/usePRPackages'
import { useRealtimeStream } from './composables/useRealtimeStream'
import { useUrlState } from './composables/useUrlState'
import { usePresenceHeartbeat } from './composables/usePresenceHeartbeat'

// Main tab (Overview is the default landing view)
const mainTab = ref<'board' | 'artifacts' | 'overview'>('overview')

// Heartbeats drive the backend's idle-mode gate: only Builds/Artifacts
// viewers need live OBS polling (Overview stays fresh via MQ events).
usePresenceHeartbeat(computed(() => mainTab.value !== 'overview'))

// Theme
// Default to the OS color scheme; a manual toggle is remembered in
// localStorage and takes precedence over the OS from then on.
const THEME_STORAGE_KEY = 'theme'
const prefersDark = window.matchMedia('(prefers-color-scheme: dark)')

function osTheme(): 'light' | 'dark' {
  return prefersDark.matches ? 'dark' : 'light'
}

const storedTheme = localStorage.getItem(THEME_STORAGE_KEY)
const theme = ref<'light' | 'dark'>(
  storedTheme === 'light' || storedTheme === 'dark' ? storedTheme : osTheme(),
)

watch(theme, (val) => {
  document.documentElement.setAttribute('data-theme', val === 'dark' ? 'dark' : '')
}, { immediate: true })

// Follow live OS changes only while the user hasn't set a manual override.
prefersDark.addEventListener('change', (e) => {
  if (localStorage.getItem(THEME_STORAGE_KEY)) return
  theme.value = e.matches ? 'dark' : 'light'
})

function toggleTheme() {
  theme.value = theme.value === 'light' ? 'dark' : 'light'
  localStorage.setItem(THEME_STORAGE_KEY, theme.value)
}

// Context
const selectedContext = ref<Context>(PPG_STAGING_CONTEXT)
const selectedPrefix = computed(() => selectedContext.value.prefix)

// Navigation state
const version = ref('')
const activeTags = ref<string[]>([])
const activeInstances = ref<string[]>([])
const spotlightStates = ref<string[]>([])

function toggleSpotlight(states: string[]) {
  const key = states.slice().sort().join(',')
  const cur = spotlightStates.value.slice().sort().join(',')
  spotlightStates.value = cur === key ? [] : states
}

function toggleTag(tag: string) {
  const idx = activeTags.value.indexOf(tag)
  if (idx >= 0) {
    activeTags.value = activeTags.value.filter(s => s !== tag)
  } else {
    activeTags.value = [...activeTags.value, tag]
  }
}

function toggleInstance(slug: string) {
  const idx = activeInstances.value.indexOf(slug)
  if (idx >= 0) {
    activeInstances.value = activeInstances.value.filter(s => s !== slug)
  } else {
    activeInstances.value = [...activeInstances.value, slug]
  }
}

function selectContext(ctx: Context) {
  selectedContext.value = ctx
  activeTags.value = []
  activeInstances.value = []
  // The selectedContext watcher below performs the fetch, so a context set
  // by URL hydration (ctx=pr-121 resolved once PR groups load) refetches
  // exactly like a click does.
}

// Event window state
const windowMin = ref(60)
const customFrom = ref<string | null>(null)
const customTo = ref<string | null>(null)

// Data fetching
const apiBase = computed(() => selectedContext.value.apiBase)
const { data: allPackages, rawData: rawPackages, availableVersions, refresh: refreshPackages, filterByTags } = usePackages(apiBase, version, selectedContext)
const { data: events, refresh: refreshEvents, filterEvents } = useEvents(apiBase, version)
const { data: prGroups, refresh: refreshPR } = usePRPackages()

// Reset to highest available only when a specific version is selected but no longer exists.
// When version is '' (All), leave it alone — All is always valid.
watch(availableVersions, (vers) => {
  if (vers.length > 0 && version.value !== '' && !vers.includes(version.value)) {
    version.value = vers[0]
  }
})

// Derive available contexts from PR groups data — one context per PR (all subprojects).
const contexts = computed<Context[]>(() => {
  const seen = new Set<string>()
  const prContexts: Context[] = []

  for (const group of prGroups.value) {
    for (const pkg of group.packages) {
      const parts = pkg.project.split(':')
      const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
      if (prIdx < 0 || prIdx + 1 >= parts.length) continue
      const prSegment = parts[prIdx + 1] // "pr-92"
      if (seen.has(prSegment)) continue
      seen.add(prSegment)
      const prNum = prSegment.replace(/^pr-/i, '')
      prContexts.push({
        label: `PR #${prNum}`,
        apiBase: `/api/pr/${prSegment}`,
        prefix: `PR:${prSegment}`,
      })
    }
  }

  prContexts.sort((a, b) => {
    const na = parseInt(a.prefix.split(':')[1]?.replace(/^pr-/i, '') ?? '0')
    const nb = parseInt(b.prefix.split(':')[1]?.replace(/^pr-/i, '') ?? '0')
    return nb - na
  })

  return [PPG_DEVEL_CONTEXT, PPG_STAGING_CONTEXT, ...prContexts]
})

// Artifacts panel state (lifted for URL sync)
const artifactsVersion = ref('')
const artifactsTab = ref<'packages' | 'containers' | 'tarballs'>('packages')
const artifactsContext = ref<Context>(PPG_STAGING_CONTEXT)

// Overview panel state (lifted for URL sync)
const overviewWindow = ref<WindowKey>('24h')

// Artifacts contexts: PPG devel/staging + Releases + one context per (PR, tier).
const artifactsContexts = computed<Context[]>(() => [
  PPG_DEVEL_CONTEXT,
  PPG_STAGING_CONTEXT,
  RELEASES_CONTEXT,
  ...prArtifactsContexts(prGroups.value),
])

useUrlState({
  mainTab,
  boardCtx: selectedContext,
  version,
  activeTags,
  artifactsCtx: artifactsContext,
  artifactsVersion,
  artifactsTab,
  boardContexts: contexts,
  artifactsContexts,
  overviewWindow,
  activeInstances,
})

const filteredPackages = computed(() => filterByTags(activeTags.value, activeInstances.value))
const filteredEvents = computed(() => filterEvents(activeTags.value, version.value, selectedContext.value, activeInstances.value))
const updatedAt = ref<string | null>(null)
const refreshing = ref(false)

// A refresh requested while one is in flight runs once more afterwards
// instead of being dropped: URL hydration resolves a PR context while the
// initial refresh is still awaiting its PR-groups fetch, and that context
// change must still produce a fetch for the new apiBase.
let refreshQueued = false

async function refresh() {
  if (refreshing.value) {
    refreshQueued = true
    return
  }
  refreshing.value = true
  try {
    const isCustom = windowMin.value === -1
    const hasCustomRange = customFrom.value != null && customTo.value != null
    // Skip fetch when Custom is active but dates aren't set yet — window=-1 returns 400.
    const eventsOpts = isCustom
      ? (hasCustomRange ? { from: customFrom.value!, to: customTo.value! } : null)
      : { window: windowMin.value }
    await Promise.all([
      refreshPackages(),
      eventsOpts ? refreshEvents(eventsOpts) : Promise.resolve(),
      refreshPR(),
    ])
    updatedAt.value = new Date().toISOString()
  } finally {
    refreshing.value = false
    if (refreshQueued) {
      refreshQueued = false
      void refresh()
    }
  }
}

// Initial fetch + SSE real-time stream
onMounted(() => {
  refresh()
})
useRealtimeStream(rawPackages, events, prGroups, selectedPrefix, refresh, refreshPR)

// Re-fetch on window change
watch([windowMin, customFrom, customTo], () => refresh())

// Re-fetch whenever the board context changes, whether by click or by URL
// hydration; both set selectedContext and must swap the data to its apiBase.
watch(selectedContext, () => refresh())
</script>

<template>
  <div class="min-h-screen bg-bg-app pt-6 px-4 sm:px-7 pb-[60px]">
    <div class="max-w-[1360px] mx-auto flex flex-col gap-4">
      <AppHeader :theme="theme" :main-tab="mainTab" @toggle-theme="toggleTheme" @update:main-tab="mainTab = $event" />
      <template v-if="mainTab === 'board'">
        <ContextBar
          :version="version"
          :updated-at="updatedAt"
          :refreshing="refreshing"
          :active-tags="activeTags"
          :contexts="contexts"
          :selected-context="selectedContext"
          :available-versions="availableVersions"
          :active-instances="activeInstances"
          @update:version="version = $event"
          @toggle-tag="toggleTag"
          @toggle-instance="toggleInstance"
          @update:context="selectContext"
          @refresh="refresh"
        />
        <HealthHeader :packages="allPackages" :spotlight="spotlightStates" @toggle-spotlight="toggleSpotlight" />
        <MainGrid
          :packages="filteredPackages"
          :events="filteredEvents"
          :window-min="windowMin"
          :custom-from="customFrom"
          :custom-to="customTo"
          :spotlight-states="spotlightStates"
          :active-instances="activeInstances"
          @update:window-min="windowMin = $event"
          @update:custom-from="customFrom = $event"
          @update:custom-to="customTo = $event"
        />
      </template>
      <ArtifactsPanel
        v-else-if="mainTab === 'artifacts'"
        :artifacts-contexts="artifactsContexts"
        :artifacts-version="artifactsVersion"
        :artifacts-context="artifactsContext"
        :artifacts-tab="artifactsTab"
        @update:artifacts-version="artifactsVersion = $event"
        @update:artifacts-context="artifactsContext = $event"
        @update:artifacts-tab="artifactsTab = $event"
      />
      <OverviewPanel
        v-else-if="mainTab === 'overview'"
        :overview-window="overviewWindow"
        @update:overview-window="overviewWindow = $event"
      />
    </div>
  </div>
</template>
