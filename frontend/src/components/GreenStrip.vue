<script setup lang="ts">
import { computed } from 'vue'
import type { Package, ObsInstance } from '../types/api'
import { useInstances } from '../composables/useInstances'
import { projectUrl, packageUrl } from '../lib/instances'

const props = defineProps<{ packages: Package[] }>()

const { instances, packageInstances } = useInstances()

function groupInstances(pkgs: Package[]): ObsInstance[] {
  const slugs = new Set(pkgs.flatMap(p => packageInstances(p).map(i => i.slug)))
  return instances.value.filter(i => slugs.has(i.slug))
}

const groups = computed(() => {
  const map = new Map<string, Package[]>()
  for (const pkg of props.packages) {
    const list = map.get(pkg.project) ?? []
    list.push(pkg)
    map.set(pkg.project, list)
  }
  return [...map.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([project, pkgs]) => ({ project, pkgs }))
})

// A succeeded package that is not settled still has at least one target
// awaiting publication in a publishing repo — its pill turns yellow until
// the publisher catches up. settled covers the never-publishing-repo case,
// so those packages stay green (nothing will ever flip them to published).
function fullyPublished(pkg: Package): boolean {
  return pkg.rollup_state === 'published' || pkg.settled === true
}

</script>

<template>
  <div class="flex flex-col gap-[14px] bg-bg-card border border-border rounded-[12px] p-[15px]">
    <!-- Summary header -->
    <div class="flex items-center gap-[9px]">
      <span class="w-[10px] h-[10px] rounded-[3px] bg-ok"></span>
      <span class="text-[13px] font-bold text-text-primary">All clear · {{ packages.length }} package{{ packages.length !== 1 ? 's' : '' }} fully built</span>
    </div>
    <!-- Per-project groups -->
    <div
      v-for="(group, index) in groups"
      :key="group.project"
      class="flex flex-col gap-[7px]"
      :style="{ borderTop: index > 0 ? '1px solid var(--border)' : '', paddingTop: index > 0 ? '10px' : '' }"
    >
      <!-- Group header: full OBS project path linking to project page, one anchor per hosting instance -->
      <div class="flex items-center gap-[10px] flex-wrap">
        <a v-for="inst in groupInstances(group.pkgs)" :key="inst.slug"
           :href="projectUrl(inst, group.project) || undefined" target="_blank" rel="noopener"
           class="project-link font-mono text-[11px] text-text-muted no-underline inline-flex items-center gap-[3px]"
        >{{ group.project }}<template v-if="groupInstances(group.pkgs).length > 1"> · {{ inst.name }}</template> ↗</a>
      </div>
      <!-- Package pills linking to individual OBS package pages -->
      <div class="flex gap-[7px] flex-wrap">
        <a
          v-for="pkg in group.pkgs"
          :key="pkg.name"
          :href="packageUrl(packageInstances(pkg)[0], group.project, pkg.name) || undefined"
          target="_blank"
          rel="noopener"
          class="pkg-pill inline-flex items-center gap-[6px] py-[4px] px-[10px] rounded-[7px] no-underline"
          :class="fullyPublished(pkg) ? 'bg-ok-tint' : 'bg-warn-tint'"
          :title="fullyPublished(pkg) ? undefined : 'built — publication pending'"
        >
          <span
            class="w-[6px] h-[6px] rounded-full flex-shrink-0"
            :class="fullyPublished(pkg) ? 'bg-ok' : 'bg-warn'"
          ></span>
          <code class="font-mono text-[11px] text-text-secondary">{{ pkg.name }}</code>
          <!-- Built version (versrel, or the first image tag for containers); aggregates
               resolve to their source package's version in the backend. -->
          <code v-if="pkg.version" data-role="version" class="font-mono text-[11px] text-text-muted">{{ pkg.version }}</code>
        </a>
      </div>
    </div>
  </div>
</template>

<style scoped>
.pkg-pill:hover {
  opacity: 0.75;
}
.project-link:hover {
  color: var(--text-secondary);
}
</style>
