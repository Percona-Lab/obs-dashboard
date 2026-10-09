<template>
  <div class="flex flex-col h-full min-h-0">
    <ArtifactsVersionBar
      :version="props.artifactsVersion"
      :available-versions="availableVersions"
      :contexts="props.artifactsContexts"
      :selected-context="props.artifactsContext"
      :active-tab="props.artifactsTab"
      @update:version="onVersionChange"
      @update:tab="emit('update:artifactsTab', $event)"
      @update:context="onContextChange"
    />

    <PackagesSubTab
      v-if="props.artifactsTab === 'packages'"
      :package-rows="packageRows"
      :repos="repos"
      :selected-repo="selectedRepo"
      :version="localVersion"
      :art-arch="artArch"
      :copied-key="copiedKey"
      :loading="isLoading"
      @update:art-repo="artRepoObs = $event"
      @update:art-arch="artArch = $event as 'x86_64' | 'aarch64'"
      @copy="onCopy"
    />

    <ContainersSubTab
      v-else-if="props.artifactsTab === 'containers'"
      :container-images="containerImages"
      :copied-key="copiedKey"
      :loading="isLoading"
      @copy="onCopy"
    />

    <TarballsSubTab
      v-else
      :tarballs="tarballs"
      :loading="isLoading"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, nextTick } from 'vue'
import type { Context, CveScan } from '../types/api'
import type { ArtifactBinary, ContainerImage, PackageRow, RepoInfo, Tarball } from '../composables/useArtifacts'
import { useArtifacts } from '../composables/useArtifacts'
import { useArtifactMetadata } from '../composables/useArtifactMetadata'
import { deriveVersionKeys, splitVersionKey } from '../lib/versions'
import ArtifactsVersionBar from './ArtifactsVersionBar.vue'
import PackagesSubTab from './PackagesSubTab.vue'
import ContainersSubTab from './ContainersSubTab.vue'
import TarballsSubTab from './TarballsSubTab.vue'
import { isTarballRepo } from '../lib/tarballs'

const props = defineProps<{
  artifactsContexts: Context[]
  artifactsVersion: string
  artifactsContext: Context
  artifactsTab: 'packages' | 'containers' | 'tarballs'
}>()

const emit = defineEmits<{
  'update:artifactsVersion': [v: string]
  'update:artifactsContext': [ctx: Context]
  'update:artifactsTab': [tab: 'packages' | 'containers' | 'tarballs']
}>()

// Computed aliases so the rest of the component body can use them unchanged
const localVersion = computed(() => props.artifactsVersion)
const isReleaseContext = computed(() => props.artifactsContext.apiBase.startsWith('/api/releases/'))

// Package state (self-fetched)
const artifactsPackages = ref<import('../types/api').Package[]>([])
const pendingFetches = ref(0)

// Version derived from fetched packages
const availableVersions = computed<string[]>(() => {
  const ctx = props.artifactsContext
  return deriveVersionKeys(
    artifactsPackages.value.map(p => p.project),
    ctx.prefix,
    ctx.allowedSubprojects,
  )
})

const artRepoObs = ref<string>('')
const artArch = ref<'x86_64' | 'aarch64'>('x86_64')
const copiedKey = ref<string | null>(null)
const repos = ref<RepoInfo[]>([])
const releaseArtifacts = ref<ReleaseArtifactsResponse | null>(null)

const selectedRepo = computed<RepoInfo | null>(
  () => repos.value.find(r => r.obs === artRepoObs.value) ?? null,
)

async function fetchPackages(ctx: Context) {
  pendingFetches.value++
  try {
    // Fetch all versions for the selected context so the version selector can
    // be derived from the complete package corpus. The tab content filters by
    // localVersion client-side.
    const url = `${ctx.apiBase}/_/packages`
    const res = await fetch(url)
    const data = await res.json()
    artifactsPackages.value = Array.isArray(data) ? data : (data.packages ?? [])
  } catch {
    artifactsPackages.value = []
  } finally {
    pendingFetches.value--
  }
}

async function fetchRepos(versionKey: string) {
  const ctx = props.artifactsContext
  const [ver, sub] = splitVersionKey(versionKey)
  let url = `${ctx.apiBase}/${ver}/repos`
  if (sub) {
    url += `?subproject=${encodeURIComponent(sub)}`
  }
  pendingFetches.value++
  try {
    const res = await fetch(url)
    const data = await res.json() as { rpm: { obs: string; name: string }[]; deb: { obs: string; name: string }[] }
    const next: RepoInfo[] = [
      ...data.rpm.map(r => ({ ...r, type: 'rpm' as const })),
      ...data.deb.map(r => ({ ...r, type: 'deb' as const })),
    ].filter(r => !isTarballRepo(r.obs))
    repos.value = next
    if (repos.value.length > 0 && !repos.value.find(r => r.obs === artRepoObs.value)) {
      artRepoObs.value = repos.value.find(r => r.type === 'rpm')?.obs ?? repos.value[0].obs
    }
  } catch {
    repos.value = []
  } finally {
    pendingFetches.value--
  }
}

async function fetchReleaseArtifacts(version: string) {
  pendingFetches.value++
  try {
    const res = await fetch(`/api/releases/ppg/${version}/artifacts`)
    if (!res.ok) throw new Error(res.statusText)
    const data = await res.json() as ReleaseArtifactsResponse
    releaseArtifacts.value = data
    repos.value = reposFromReleaseArtifacts(data)
    if (repos.value.length > 0 && !repos.value.find(r => r.obs === artRepoObs.value)) {
      artRepoObs.value = repos.value.find(r => r.type === 'rpm')?.obs ?? repos.value[0].obs
    }
  } catch {
    releaseArtifacts.value = null
    repos.value = []
  } finally {
    pendingFetches.value--
  }
}

// When available versions change, snap to first if current version is absent
watch(availableVersions, (versions) => {
  if (versions.length > 0 && (props.artifactsVersion === '' || !versions.includes(props.artifactsVersion))) {
    emit('update:artifactsVersion', versions[0])
  }
})

// Re-fetch repos when version changes; immediate so repos load on mount.
// Guard against empty string — artifactsVersion starts as '' until URL is
// hydrated or availableVersions snaps to the latest version.
watch(() => props.artifactsVersion, (v) => {
  if (!v) return
  if (isReleaseContext.value) {
    fetchReleaseArtifacts(v)
  } else {
    fetchRepos(v)
  }
}, { immediate: true })

onMounted(() => {
  fetchPackages(props.artifactsContext)
})

async function onContextChange(ctx: Context) {
  emit('update:artifactsContext', ctx)
  artRepoObs.value = ''  // clear stale repo so new context auto-selects its first repo
  releaseArtifacts.value = null
  await fetchPackages(ctx)
  // Wait for Vue to flush the context prop so that availableVersions (which uses
  // props.artifactsContext.prefix depth) and fetchRepos (which reads
  // props.artifactsContext) both see the newly-selected context, not the old one.
  await nextTick()
  const versions = availableVersions.value
  if (versions.length > 0 && props.artifactsVersion !== versions[0]) {
    emit('update:artifactsVersion', versions[0])
  } else if (ctx.apiBase.startsWith('/api/releases/')) {
    await fetchReleaseArtifacts(props.artifactsVersion)
  } else {
    await fetchRepos(props.artifactsVersion)
  }
}

function onVersionChange(v: string) {
  emit('update:artifactsVersion', v)
}

const { packageRows: livePackageRows, containerImages: liveContainerImages, tarballs: liveTarballs } = useArtifacts(
  artifactsPackages,
  localVersion,
  selectedRepo,
  artArch,
  computed(() => props.artifactsContext),
)

const { enrichedPackageRows, enrichedContainerImages, isLoading: metadataLoading } = useArtifactMetadata(
  livePackageRows,
  liveContainerImages,
  computed(() => !isReleaseContext.value),
)

const isLoading = computed(() => pendingFetches.value > 0 || metadataLoading.value)

const packageRows = computed<PackageRow[]>(() => {
  if (!isReleaseContext.value) return enrichedPackageRows.value.filter(row => row.binaries && row.binaries.length > 0)
  const repo = selectedRepo.value
  if (!repo || !releaseArtifacts.value) return []
  return releaseArtifacts.value.packages
    .filter(pkg => pkg.repo === repo.obs && pkg.arch === artArch.value)
    .map(pkg => ({
      project: pkg.project,
      name: pkg.name,
      version: pkg.version ?? '',
      tags: ['release'],
      state: 'succeeded',
      published: true,
      repo: { obs: pkg.repo, name: pkg.repo_name, type: pkg.repo_type as 'rpm' | 'deb' },
      arch: pkg.arch,
      binaries: pkg.binaries,
      builtAt: pkg.built_at,
      instance: pkg.instance,
    }))
})

const containerImages = computed<ContainerImage[]>(() => {
  if (!isReleaseContext.value) return enrichedContainerImages.value
  if (!releaseArtifacts.value) return []
  return releaseArtifacts.value.container_images.map(img => ({
    id: `${img.project}/${img.image_name}`,
    project: img.project,
    imageName: img.image_name,
    baseOs: img.base_os,
    registry: img.registry,
    tags: img.tags,
    pullCmd: img.pull_cmd,
    rollupState: 'succeeded',
    published: true,
    mtime: img.mtime,
    builtAt: img.built_at,
    cveScans: img.cve_scans ?? [],
    instance: img.instance,
  }))
})

const tarballs = computed<Tarball[]>(() => {
  if (!isReleaseContext.value) return liveTarballs.value
  if (!releaseArtifacts.value) return []
  // Backend emits one ReleaseTarballArtifact per (name, repo, arch); group by
  // (project, name, repo) into one card with collected arches + latest built_at,
  // matching the live path's per-(package, ssl-repo) granularity.
  const byKey = new Map<string, Tarball>()
  for (const t of releaseArtifacts.value.tarballs ?? []) {
    const id = `${t.project}/${t.name}/${t.repo}`
    const existing = byKey.get(id)
    if (existing) {
      if (!existing.arches.includes(t.arch)) existing.arches.push(t.arch)
      if (t.built_at > existing.builtAt) existing.builtAt = t.built_at
    } else {
      byKey.set(id, {
        id,
        project: t.project,
        name: t.name,
        version: t.version,
        repo: t.repo,
        arches: [t.arch],
        rollupState: 'succeeded',
        published: true,
        builtAt: t.built_at,
        instance: t.instance,
      })
    }
  }
  return [...byKey.values()]
})

function reposFromReleaseArtifacts(data: ReleaseArtifactsResponse): RepoInfo[] {
  const byObs = new Map<string, RepoInfo>()
  for (const pkg of data.packages) {
    if (!byObs.has(pkg.repo)) {
      byObs.set(pkg.repo, {
        obs: pkg.repo,
        name: pkg.repo_name,
        type: pkg.repo_type,
      })
    }
  }
  return [...byObs.values()].sort((a, b) => {
    if (a.type !== b.type) return a.type === 'rpm' ? -1 : 1
    return a.name.localeCompare(b.name)
  })
}

interface ReleasePackageArtifact {
  project: string
  name: string
  version?: string
  repo: string
  repo_name: string
  repo_type: 'rpm' | 'deb'
  arch: string
  binaries: ArtifactBinary[]
  built_at: string
  instance?: string
}

interface ReleaseContainerArtifact {
  project: string
  image_name: string
  base_os: string
  registry: string
  tags: string[]
  pull_cmd: string
  mtime: number
  built_at: string
  cve_scans?: CveScan[]
  instance?: string
}

interface ReleaseTarballArtifact {
  project: string
  name: string
  version: string
  repo: string
  arch: string
  built_at: string
  instance?: string
}

interface ReleaseArtifactsResponse {
  version: string
  refreshed_at: string
  packages: ReleasePackageArtifact[]
  container_images: ReleaseContainerArtifact[]
  tarballs?: ReleaseTarballArtifact[]
}

let copyTimer: ReturnType<typeof setTimeout> | null = null
function onCopy(key: string, text: string) {
  navigator.clipboard.writeText(text).catch(() => {})
  copiedKey.value = key
  if (copyTimer) clearTimeout(copyTimer)
  copyTimer = setTimeout(() => {
    copiedKey.value = null
    copyTimer = null
  }, 2000)
}
</script>
