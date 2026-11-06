import { watch, watchEffect, onMounted, ref, type Ref } from 'vue'
import type { Context } from '../types/api'
import { PPG_STAGING_CONTEXT } from '../lib/contexts'
import type { WindowKey } from '../types/overview'

export type MainTab = 'board' | 'artifacts' | 'overview' | 'settings'

export function contextToKey(ctx: Context): string {
  const parts = ctx.prefix.split(':')
  const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
  if (prIdx >= 0) {
    return parts[prIdx + 1] // e.g. "pr-106"
  }
  return parts[parts.length - 1] // "devel" | "staging" | "releases"
}

export function keyToContext(key: string, contexts: Context[]): Context | undefined {
  return contexts.find(c => contextToKey(c) === key)
}

interface UrlStateOptions {
  mainTab: Ref<MainTab>
  boardCtx: Ref<Context>
  version: Ref<string>
  activeTags: Ref<string[]>
  artifactsCtx: Ref<Context>
  artifactsVersion: Ref<string>
  artifactsTab: Ref<'packages' | 'containers' | 'tarballs'>
  boardContexts: Ref<Context[]>
  artifactsContexts: Ref<Context[]>
  overviewWindow: Ref<WindowKey>
  activeInstances: Ref<string[]>
}

export function useUrlState(state: UrlStateOptions): void {
  const {
    mainTab, boardCtx, version, activeTags,
    artifactsCtx, artifactsVersion, artifactsTab,
    boardContexts, artifactsContexts, overviewWindow,
    activeInstances,
  } = state

  // Pending raw URL keys awaiting context list population (PR contexts load async)
  let pendingBoardKey: string | null = null
  let pendingArtifactsKey: string | null = null
  // Reactive so watchEffect re-runs when onMounted sets it true
  const hydrated = ref(false)

  onMounted(() => {
    const params = new URLSearchParams(window.location.search)

    const tab = params.get('tab')
    if (tab === 'board' || tab === 'artifacts' || tab === 'overview' || tab === 'settings') mainTab.value = tab

    const ver = params.get('version')
    if (ver) version.value = ver

    const tags = params.get('tags')
    if (tags) activeTags.value = tags.split(',').filter(Boolean)

    const instancesParam = params.get('instances')
    if (instancesParam) activeInstances.value = instancesParam.split(',').filter(Boolean)

    const aver = params.get('aversion')
    if (aver) artifactsVersion.value = aver

    const sub = params.get('sub')
    if (sub === 'packages' || sub === 'containers' || sub === 'tarballs') artifactsTab.value = sub

    const owin = params.get('owin')
    if (owin === '24h' || owin === '48h' || owin === '7d') overviewWindow.value = owin

    const ctxKey = params.get('ctx')
    if (ctxKey) {
      const resolved = keyToContext(ctxKey, boardContexts.value)
      if (resolved) {
        boardCtx.value = resolved
      } else {
        pendingBoardKey = ctxKey
      }
    }

    const actxKey = params.get('actx')
    if (actxKey) {
      const resolved = keyToContext(actxKey, artifactsContexts.value)
      if (resolved) {
        artifactsCtx.value = resolved
      } else {
        pendingArtifactsKey = actxKey
      }
    }

    hydrated.value = true
  })

  // Resolve board ctx once context list loads (PR contexts come from prGroups async)
  watch(boardContexts, (contexts) => {
    if (!pendingBoardKey || contexts.length === 0) return
    const resolved = keyToContext(pendingBoardKey, contexts)
    boardCtx.value = resolved ?? PPG_STAGING_CONTEXT
    pendingBoardKey = null
  }, { immediate: true })

  // Resolve artifacts ctx once context list loads
  watch(artifactsContexts, (contexts) => {
    if (!pendingArtifactsKey || contexts.length === 0) return
    const resolved = keyToContext(pendingArtifactsKey, contexts)
    artifactsCtx.value = resolved ?? PPG_STAGING_CONTEXT
    pendingArtifactsKey = null
  }, { immediate: true })

  // Write URL whenever any state ref changes; omit default-value params
  watchEffect(() => {
    if (!hydrated.value) return
    const params = new URLSearchParams()

    if (mainTab.value !== 'overview') params.set('tab', mainTab.value) // overview is the default tab

    const boardKey = contextToKey(boardCtx.value)
    if (boardKey !== 'staging') params.set('ctx', boardKey)

    if (version.value) params.set('version', version.value)

    if (activeTags.value.length > 0) params.set('tags', activeTags.value.join(','))

    if (activeInstances.value.length > 0) params.set('instances', activeInstances.value.join(','))

    const artKey = contextToKey(artifactsCtx.value)
    if (artKey !== 'staging') params.set('actx', artKey)

    if (artifactsVersion.value) params.set('aversion', artifactsVersion.value)

    if (artifactsTab.value !== 'packages') params.set('sub', artifactsTab.value)

    if (overviewWindow.value !== '24h') params.set('owin', overviewWindow.value)

    const search = params.toString()
    history.replaceState(null, '', search ? `?${search}` : window.location.pathname)
  })
}
