<template>
  <div v-if="desktop" class="desktop-update" :class="{ 'desktop-update--open': open }">
    <button
      type="button"
      class="desktop-update__trigger"
      :class="{ available: hasUpdate, busy: isBusy }"
      :aria-expanded="open"
      title="版本与更新"
      @click="open = !open"
    >
      <Download :size="15" />
      <span>v{{ appVersion }}</span>
      <i v-if="hasUpdate"></i>
    </button>

    <aside v-if="open" class="desktop-update__panel" aria-label="版本与更新">
      <header>
        <div>
          <span>RELEASE CONTROL</span>
          <h2>版本与更新</h2>
        </div>
        <button type="button" aria-label="关闭更新面板" @click="open = false"><X :size="17" /></button>
      </header>

      <dl class="desktop-update__versions">
        <div><dt>桌面端</dt><dd>v{{ appVersion }}</dd></div>
        <div><dt>Sub2API 上游基线</dt><dd :title="`上游提交 ${upstreamCommit}`">v{{ coreVersion }}</dd></div>
        <div><dt>成本算法</dt><dd>v{{ algorithmVersion }}</dd></div>
      </dl>

      <div v-if="progressStage" class="desktop-update__progress" aria-live="polite">
        <div><span>{{ progressMessage }}</span><b>{{ progressPercent }}%</b></div>
        <p><i :style="{ width: `${progressPercent}%` }"></i></p>
      </div>

      <section v-if="coreUpdate?.available" class="desktop-update__release desktop-update__release--primary">
        <div class="desktop-update__release-title">
          <div><span>上游内核更新</span><strong>v{{ coreUpdate.update?.version }}</strong></div>
          <em>算法 v{{ coreUpdate.update?.algorithm_version }} · 提交 {{ coreUpdate.update?.upstream_commit || '未提供' }}</em>
        </div>
        <p>{{ coreUpdate.update?.notes || '稳定性、成本核算与本地服务更新。' }}</p>
        <button type="button" :disabled="isBusy" @click="installCore">
          <Download :size="15" /> 下载、校验并安全重启
        </button>
      </section>

      <section v-if="appUpdate" class="desktop-update__release">
        <div class="desktop-update__release-title">
          <div><span>桌面更新</span><strong>v{{ appUpdate.version }}</strong></div>
          <em>完整安装包</em>
        </div>
        <p>{{ appUpdate.body || '界面与桌面运行时更新。' }}</p>
        <button type="button" :disabled="isBusy" @click="installDesktop">
          <Download :size="15" /> 安装桌面更新
        </button>
      </section>

      <section v-if="!hasUpdate && !checking" class="desktop-update__current">
        <CheckCircle2 :size="20" />
        <div><strong>当前已是最新版本</strong><span>{{ lastCheckedLabel }}</span></div>
      </section>

      <p v-if="errorMessage" class="desktop-update__error" role="alert">{{ errorMessage }}</p>

      <footer>
        <button type="button" :disabled="checking || isBusy" @click="checkAll(false)">
          <RefreshCcw :size="14" :class="{ spinning: checking }" />
          {{ checking ? '正在检查' : '检查更新' }}
        </button>
        <button
          v-if="coreUpdate?.previous_version"
          type="button"
          :disabled="isBusy"
          class="desktop-update__rollback"
          @click="rollbackCore"
        >
          <History :size="14" /> 回滚内核 v{{ coreUpdate.previous_version }}
        </button>
      </footer>
    </aside>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { getVersion } from '@tauri-apps/api/app'
import { invoke } from '@tauri-apps/api/core'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import { relaunch } from '@tauri-apps/plugin-process'
import { check, type Update } from '@tauri-apps/plugin-updater'
import { CheckCircle2, Download, History, RefreshCcw, X } from '@lucide/vue'
import { isDesktopRuntime } from '@/api/url'

interface BackendStatus {
  core_version: string
  algorithm_version: string
  upstream_commit: string
}

interface CoreManifest {
  version: string
  algorithm_version: string
  upstream_commit?: string
  notes: string
}

interface CoreUpdateCheck {
  available: boolean
  current_version: string
  current_algorithm_version: string
  upstream_commit: string
  update: CoreManifest | null
  previous_version: string | null
}

interface CoreProgress {
  stage: string
  downloaded: number
  total: number | null
  message: string
}

const desktop = isDesktopRuntime()
const open = ref(false)
const appVersion = ref('0.0.0')
const coreVersion = ref('0.0.0')
const algorithmVersion = ref('1.0.0')
const upstreamCommit = ref('unknown')
const appUpdate = ref<Update | null>(null)
const coreUpdate = ref<CoreUpdateCheck | null>(null)
const checking = ref(false)
const operation = ref<'core' | 'desktop' | 'rollback' | null>(null)
const errorMessage = ref('')
const progressStage = ref('')
const progressMessage = ref('')
const progressDownloaded = ref(0)
const progressTotal = ref<number | null>(null)
const lastChecked = ref<Date | null>(null)
let interval: number | null = null
const unlisteners: UnlistenFn[] = []

const isBusy = computed(() => operation.value !== null)
const hasUpdate = computed(() => Boolean(appUpdate.value || coreUpdate.value?.available))
const progressPercent = computed(() => {
  if (progressStage.value === 'ready' || progressStage.value === 'finished') return 100
  if (!progressTotal.value) return progressStage.value ? 12 : 0
  return Math.min(99, Math.round(progressDownloaded.value / progressTotal.value * 100))
})
const lastCheckedLabel = computed(() => lastChecked.value
  ? `上次检查 ${lastChecked.value.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
  : '尚未检查 GitHub Releases')

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

async function loadRuntimeVersions() {
  appVersion.value = await getVersion()
  const backend = await invoke<BackendStatus>('desktop_backend_status')
  coreVersion.value = backend.core_version
  algorithmVersion.value = backend.algorithm_version
  upstreamCommit.value = backend.upstream_commit
}

async function checkAll(silent = true) {
  if (!desktop || checking.value || isBusy.value) return
  checking.value = true
  if (!silent) errorMessage.value = ''
  const failures: string[] = []
  try {
    await loadRuntimeVersions()
    const [desktopResult, coreResult] = await Promise.allSettled([
      check(),
      invoke<CoreUpdateCheck>('check_core_update'),
    ])
    if (desktopResult.status === 'fulfilled') appUpdate.value = desktopResult.value
    else failures.push(`桌面更新：${messageOf(desktopResult.reason)}`)
    if (coreResult.status === 'fulfilled') {
      coreUpdate.value = coreResult.value
      coreVersion.value = coreResult.value.current_version
      algorithmVersion.value = coreResult.value.current_algorithm_version
      upstreamCommit.value = coreResult.value.upstream_commit
    } else {
      failures.push(`内核更新：${messageOf(coreResult.reason)}`)
    }
    lastChecked.value = new Date()
    // A repository without its first release returns 404. Keep startup silent,
    // while a manual check exposes the actionable diagnostics.
    if (!silent && failures.length) errorMessage.value = failures.join('；')
  } finally {
    checking.value = false
  }
}

async function installCore() {
  operation.value = 'core'
  errorMessage.value = ''
  progressStage.value = 'checking'
  progressMessage.value = '正在验证内核更新'
  try {
    const result = await invoke<{ version: string; algorithm_version: string }>('install_core_update')
    progressStage.value = 'ready'
    progressMessage.value = `内核 v${result.version} 已验证，正在安全重启`
    await relaunch()
  } catch (error) {
    errorMessage.value = messageOf(error)
    progressStage.value = ''
  } finally {
    operation.value = null
  }
}

async function installDesktop() {
  if (!appUpdate.value) return
  operation.value = 'desktop'
  errorMessage.value = ''
  progressStage.value = 'downloading'
  progressMessage.value = '正在下载桌面安装包'
  progressDownloaded.value = 0
  progressTotal.value = null
  try {
    await appUpdate.value.downloadAndInstall((event) => {
      if (event.event === 'Started') {
        progressTotal.value = event.data.contentLength ?? null
      } else if (event.event === 'Progress') {
        progressDownloaded.value += event.data.chunkLength
      } else if (event.event === 'Finished') {
        progressStage.value = 'finished'
        progressMessage.value = '桌面更新已安装，正在重启'
      }
    })
    await relaunch()
  } catch (error) {
    errorMessage.value = messageOf(error)
    progressStage.value = ''
  } finally {
    operation.value = null
  }
}

async function rollbackCore() {
  operation.value = 'rollback'
  errorMessage.value = ''
  try {
    const result = await invoke<{ version: string }>('prepare_core_rollback')
    progressStage.value = 'ready'
    progressMessage.value = `已准备回滚到内核 v${result.version}，正在重启`
    await relaunch()
  } catch (error) {
    errorMessage.value = messageOf(error)
  } finally {
    operation.value = null
  }
}

onMounted(async () => {
  if (!desktop) return
  unlisteners.push(await listen<CoreProgress>('core-update-progress', (event) => {
    progressStage.value = event.payload.stage
    progressMessage.value = event.payload.message
    progressDownloaded.value = event.payload.downloaded
    progressTotal.value = event.payload.total
  }))
  unlisteners.push(await listen<string>('core-update-rollback', (event) => {
    errorMessage.value = event.payload
    open.value = true
    loadRuntimeVersions().catch(() => undefined)
  }))
  await loadRuntimeVersions().catch(() => undefined)
  window.setTimeout(() => checkAll(true), 1800)
  interval = window.setInterval(() => checkAll(true), 6 * 60 * 60 * 1000)
})

onBeforeUnmount(() => {
  if (interval !== null) window.clearInterval(interval)
  unlisteners.forEach((unlisten) => unlisten())
})
</script>

<style scoped>
.desktop-update { position: fixed; z-index: 90; right: 18px; bottom: 16px; color: #dfe7e0; font-family: Inter, 'Segoe UI', sans-serif; }
.desktop-update__trigger { position: relative; display: flex; height: 34px; align-items: center; gap: 7px; padding: 0 11px; color: #87968a; border: 1px solid #2c372f; background: rgb(17 22 18 / 94%); box-shadow: 0 8px 25px rgb(0 0 0 / 22%); font: 11px 'Cascadia Mono', monospace; }
.desktop-update__trigger:hover, .desktop-update__trigger.available { color: #cde98f; border-color: #6d8f34; }
.desktop-update__trigger i { width: 6px; height: 6px; border-radius: 50%; background: #b9e55a; box-shadow: 0 0 10px #b9e55a; }
.desktop-update__panel { position: absolute; right: 0; bottom: 44px; width: min(430px, calc(100vw - 36px)); max-height: min(720px, calc(100vh - 70px)); overflow: auto; border: 1px solid #334038; background: #121713; box-shadow: 0 24px 70px rgb(0 0 0 / 55%); }
.desktop-update__panel > header { position: sticky; z-index: 2; top: 0; display: flex; align-items: center; justify-content: space-between; padding: 18px 20px; border-bottom: 1px solid #2a342c; background: #151b16; }
.desktop-update__panel > header span { color: #91bc43; font: 10px 'Cascadia Mono', monospace; letter-spacing: .1em; }
.desktop-update__panel h2 { margin: 4px 0 0; font-size: 18px; }
.desktop-update__panel header button { display: grid; width: 30px; height: 30px; place-items: center; color: #718078; border: 0; background: transparent; }
.desktop-update__panel header button:hover { color: #e7eee8; background: #232c25; }
.desktop-update__versions { display: grid; grid-template-columns: repeat(3, 1fr); margin: 0; border-bottom: 1px solid #29332b; }
.desktop-update__versions div { padding: 14px 16px; border-right: 1px solid #29332b; }
.desktop-update__versions div:last-child { border-right: 0; }
.desktop-update__versions dt { color: #718078; font-size: 10px; }
.desktop-update__versions dd { margin: 5px 0 0; color: #dce6dd; font: 12px 'Cascadia Mono', monospace; }
.desktop-update__progress { padding: 15px 18px; border-bottom: 1px solid #29332b; background: #171d18; }
.desktop-update__progress > div { display: flex; justify-content: space-between; color: #aab5ac; font-size: 11px; }
.desktop-update__progress b { color: #b9e55a; font-family: 'Cascadia Mono', monospace; }
.desktop-update__progress p { height: 3px; margin: 9px 0 0; background: #303a31; }
.desktop-update__progress i { display: block; height: 100%; background: #b9e55a; transition: width .2s ease; }
.desktop-update__release { margin: 14px; padding: 16px; border: 1px solid #303b32; background: #161c17; }
.desktop-update__release--primary { border-color: #566d32; background: #182015; }
.desktop-update__release-title { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.desktop-update__release-title span { display: block; color: #7f8e82; font-size: 10px; }
.desktop-update__release-title strong { display: block; margin-top: 3px; color: #e8eee8; font: 18px 'Cascadia Mono', monospace; }
.desktop-update__release-title em { color: #9dbc61; font: normal 10px 'Cascadia Mono', monospace; }
.desktop-update__release p { max-height: 110px; overflow: auto; margin: 12px 0; color: #9da9a0; font-size: 11px; line-height: 1.65; white-space: pre-wrap; }
.desktop-update__release button { display: flex; width: 100%; min-height: 36px; align-items: center; justify-content: center; gap: 8px; color: #11160f; border: 1px solid #b9e55a; background: #b9e55a; font-weight: 700; }
.desktop-update__release button:disabled { opacity: .45; }
.desktop-update__current { display: flex; align-items: center; gap: 12px; margin: 18px; color: #9cbd65; }
.desktop-update__current strong, .desktop-update__current span { display: block; }
.desktop-update__current strong { color: #d8e1d9; font-size: 12px; }
.desktop-update__current span { margin-top: 3px; color: #708078; font-size: 10px; }
.desktop-update__error { margin: 14px; padding: 10px 12px; color: #e4a08e; border-left: 2px solid #d56e54; background: #251914; font-size: 11px; line-height: 1.55; word-break: break-word; }
.desktop-update__panel footer { display: flex; flex-wrap: wrap; gap: 8px; padding: 14px 18px; border-top: 1px solid #29332b; }
.desktop-update__panel footer button { display: flex; min-height: 32px; align-items: center; gap: 6px; padding: 0 10px; color: #aeb9b0; border: 1px solid #38443a; background: transparent; font-size: 11px; }
.desktop-update__panel footer button:hover:not(:disabled) { color: #dce6dd; border-color: #77867b; }
.desktop-update__panel footer .desktop-update__rollback { margin-left: auto; color: #d5b079; border-color: #685138; }
.spinning { animation: update-spin .8s linear infinite; }
@keyframes update-spin { to { transform: rotate(360deg); } }
</style>
