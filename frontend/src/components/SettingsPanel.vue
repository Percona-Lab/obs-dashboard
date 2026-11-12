<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useAuth } from '../composables/useAuth'
import { useSettingsUsers } from '../composables/useSettingsUsers'
import ServiceZone from './ServiceZone.vue'
import ServerLogPanel from './ServerLogPanel.vue'

const { user } = useAuth()
const { admins, users, loadError, list, add, remove } = useSettingsUsers()
const newEmail = ref('')
const addError = ref<string | null>(null)
const removeError = ref<string | null>(null)
const busy = ref(false)
const subTab = ref<'general' | 'log'>('general')
const tabBase = 'px-3 py-1 rounded-[7px] border text-[13px] cursor-pointer [font-family:inherit]'
const tabOn = 'bg-bg-card text-text-primary font-bold border-border-strong shadow-[0_1px_2px_rgba(0,0,0,0.12)]'
const tabOff = 'bg-transparent text-text-muted font-medium border-transparent'

onMounted(list)

async function onAdd() {
  if (!newEmail.value.trim()) return
  busy.value = true
  addError.value = await add(newEmail.value)
  busy.value = false
  if (addError.value === null) newEmail.value = ''
}

async function onRemove(email: string) {
  if (!window.confirm(`Remove ${email}? Their sessions end immediately.`)) return
  removeError.value = await remove(email)
}
</script>

<template>
  <div class="flex flex-col gap-5">
    <div class="flex gap-[3px] bg-bg-muted p-[3px] rounded-[9px] border border-border self-start">
      <button
        data-testid="settings-tab-general"
        :aria-selected="subTab === 'general'"
        :class="[tabBase, subTab === 'general' ? tabOn : tabOff]"
        @click="subTab = 'general'"
      >General</button>
      <button
        data-testid="settings-tab-log"
        :aria-selected="subTab === 'log'"
        :class="[tabBase, subTab === 'log' ? tabOn : tabOff]"
        @click="subTab = 'log'"
      >Server log</button>
    </div>
    <ServerLogPanel v-if="subTab === 'log'" />
    <ServiceZone v-if="subTab === 'general'" />
    <section v-if="subTab === 'general'" data-testid="access-zone" class="rounded-[14px] border border-border bg-bg-card p-5 flex flex-col gap-4">
      <header class="flex flex-col gap-1">
        <h2 class="m-0 text-[16px] font-bold text-text-primary">Access</h2>
        <p class="m-0 text-[12.5px] text-text-muted">
          People who can sign in and use actions such as Rebuild. Only @percona.com Google accounts can sign in.
        </p>
      </header>

      <div v-if="loadError" data-testid="load-error" class="text-[12.5px] text-fail">{{ loadError }}</div>

      <ul class="m-0 p-0 list-none flex flex-col divide-y divide-border border border-border rounded-[10px]">
        <li v-for="a in admins" :key="'admin:' + a" data-testid="admin-row" class="flex items-center gap-3 px-3 py-2">
          <span class="font-mono text-[12.5px] text-text-primary">{{ a }}</span>
          <span class="text-[10.5px] font-bold uppercase tracking-[0.06em] px-[6px] py-[1px] rounded-[6px] bg-bg-muted text-text-muted">config</span>
        </li>
        <li v-for="u in users" :key="u.email" data-testid="user-row" class="flex items-center gap-3 px-3 py-2">
          <span class="font-mono text-[12.5px] text-text-primary">{{ u.email }}</span>
          <span class="text-[11.5px] text-text-muted">added by {{ u.added_by }} · {{ u.added_at.slice(0, 10) }}</span>
          <button
            data-testid="remove"
            class="ml-auto text-[12px] font-semibold px-[10px] py-[4px] rounded-[8px] border border-border bg-bg-card text-text-secondary cursor-pointer disabled:opacity-40 disabled:cursor-default"
            :disabled="u.email === user"
            :title="u.email === user ? 'You cannot remove yourself' : undefined"
            @click="onRemove(u.email)"
          >Remove</button>
        </li>
        <li v-if="admins.length === 0 && users.length === 0 && !loadError" class="px-3 py-2 text-[12.5px] text-text-muted">
          No one is allowed yet.
        </li>
      </ul>
      <div v-if="removeError" data-testid="remove-error" class="text-[12.5px] text-fail">{{ removeError }}</div>

      <form class="flex flex-wrap items-center gap-2" @submit.prevent="onAdd">
        <input
          v-model="newEmail"
          data-testid="add-email"
          type="email"
          aria-label="Email to allow"
          placeholder="name@percona.com"
          class="flex-1 min-w-[220px] px-3 py-2 rounded-[10px] border border-border bg-bg-app text-[13px] text-text-primary font-mono"
        />
        <button
          type="submit"
          data-testid="add"
          :disabled="busy"
          class="px-[14px] py-2 rounded-[10px] border border-border-strong bg-bg-card text-brand-purple text-[13px] font-semibold cursor-pointer disabled:opacity-50"
        >Add</button>
      </form>
      <div v-if="addError" data-testid="add-error" class="text-[12.5px] text-fail">{{ addError }}</div>
    </section>
  </div>
</template>
