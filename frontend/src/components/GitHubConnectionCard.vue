<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useGitHubConnection } from '../composables/useGitHubConnection'

const { status, loadError, loading, load } = useGitHubConnection()
onMounted(load)

const reason = computed(() => {
  const s = status.value
  if (!s || s.ok) return ''
  switch (s.failed_step) {
    case 'config': return s.error
    case 'app': return "GitHub rejected the app's key or ID."
    case 'installation':
      return s.error.endsWith('HTTP 404')
        ? `The app is not installed on ${s.repo}.`
        : "Could not look up the app's installation."
    case 'token': return 'GitHub would not issue a token.'
    case 'repository': return `The token cannot reach ${s.repo}.`
    default: return ''
  }
})
const showError = computed(() => !!status.value && !status.value.ok && status.value.failed_step !== 'config' && !!status.value.error)
const anyPermBad = computed(() => (status.value?.permissions ?? []).some(p => !p.ok))
const tokenTimes = computed(() => {
  const t = status.value?.token
  if (!t || !t.minted_at) return ''
  return `issued ${t.minted_at.slice(11, 16)}, expires ${(t.expires_at ?? '').slice(11, 16)}`
})
</script>

<template>
  <section data-testid="github-zone" class="rounded-[14px] border border-border bg-bg-card p-5 flex flex-col gap-4">
    <header class="flex items-start gap-3">
      <div class="flex flex-col gap-1">
        <h2 class="m-0 text-[16px] font-bold text-text-primary">GitHub connection</h2>
        <p v-if="status?.mode === 'app'" class="m-0 text-[12.5px] text-text-muted">The Release tab acts on GitHub as this app.</p>
      </div>
      <button
        data-testid="github-test"
        :disabled="loading"
        class="ml-auto px-[14px] py-2 rounded-[10px] border border-border-strong bg-bg-card text-brand-purple text-[13px] font-semibold cursor-pointer disabled:opacity-50"
        @click="load"
      >Test connection</button>
    </header>

    <div v-if="loadError" data-testid="github-load-error" class="text-[12.5px] text-fail">{{ loadError }}</div>

    <template v-if="status">
      <div v-if="status.mode === 'off'" data-testid="github-off" class="text-[12.5px] text-text-muted">
        Not configured. The Release tab is hidden.
        <span class="font-mono">RELEASE_GITHUB_APP_ID</span>
        <span class="font-mono">RELEASE_GITHUB_APP_PRIVATE_KEY</span>
      </div>

      <template v-else>
        <div data-testid="github-state" class="text-[13px] font-bold" :class="status.ok ? 'text-text-primary' : 'text-fail'">
          {{ status.ok ? 'Connected' : 'Not connected' }}
        </div>
        <div v-if="reason" data-testid="github-reason" class="text-[12.5px] text-text-primary">{{ reason }}</div>
        <div v-if="showError" data-testid="github-error" class="font-mono text-[12px] text-fail">{{ status.error }}</div>

        <div v-if="status.mode === 'token'" data-testid="github-token-mode" class="text-[12.5px] text-text-muted">
          Using a personal access token. Set RELEASE_GITHUB_APP_ID and RELEASE_GITHUB_APP_PRIVATE_KEY to switch to the GitHub App.
        </div>

        <div class="flex flex-col gap-1 text-[12.5px] text-text-secondary">
          <div v-if="status.app">{{ status.app.name }} ({{ status.app.slug }}) · app {{ status.app.id }}</div>
          <div v-if="status.mode === 'token'">{{ status.repo }}</div>
          <div v-else-if="status.installation_id !== undefined">{{ status.repo }} · installation {{ status.installation_id }}</div>
          <div v-if="tokenTimes">{{ tokenTimes }}</div>
        </div>

        <ul v-if="status.permissions?.length" class="m-0 p-0 list-none flex flex-col gap-1 font-mono text-[12px]">
          <li
            v-for="p in status.permissions"
            :key="p.name"
            data-testid="github-perm"
            :class="p.ok ? 'text-text-secondary' : 'text-warn'"
          >{{ p.name }} {{ p.granted }}{{ p.ok ? ' ✓' : '' }}</li>
        </ul>
        <div v-if="anyPermBad" data-testid="github-perm-warning" class="text-[12.5px] text-warn">
          Some release actions will fail until the app is granted these permissions.
        </div>

        <div class="text-[11.5px] text-text-muted">Checked {{ status.checked_at.slice(11, 19) }} UTC</div>
      </template>
    </template>
  </section>
</template>
