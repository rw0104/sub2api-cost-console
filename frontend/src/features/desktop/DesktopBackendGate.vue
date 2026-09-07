<template>
  <main class="desktop-gate">
    <section class="desktop-gate__card" aria-live="polite">
      <div class="desktop-gate__mark" aria-hidden="true">S</div>
      <p class="desktop-gate__eyebrow">SUB2API DESKTOP RUNTIME</p>
      <h1>{{ title }}</h1>
      <p class="desktop-gate__message">{{ status?.message || '正在连接本地内核…' }}</p>

      <div v-if="status?.phase === 'starting'" class="desktop-gate__progress" aria-label="内核启动中">
        <i></i>
      </div>

      <dl v-if="status" class="desktop-gate__facts">
        <div><dt>连接方式</dt><dd>{{ status.managed ? '安装包受管内核' : '本机现有服务' }}</dd></div>
        <div><dt>服务地址</dt><dd>127.0.0.1:{{ status.port }}</dd></div>
        <div><dt>Sub2API 上游基线 / 成本算法</dt><dd>v{{ status.core_version }} / v{{ status.algorithm_version }}</dd></div>
      </dl>

      <div v-if="canRetry" class="desktop-gate__error" :role="status?.phase === 'error' ? 'alert' : 'status'">
        <strong>{{ status?.problem?.title || '内核未能就绪' }}</strong>
        <p>{{ status?.problem?.message || '请重新检测数据服务并启动内核。如仍失败，可展开下方技术详情。' }}</p>
        <button type="button" :disabled="retrying" @click="retry">
          {{ retrying ? '正在重新检测…' : '重新检测并启动' }}
        </button>
      </div>

      <details v-if="status" class="desktop-gate__details">
        <summary>查看技术详情</summary>
        <p v-if="status.last_log" class="desktop-gate__log">{{ status.last_log }}</p>
        <p class="desktop-gate__path">数据目录：{{ status.data_dir }}</p>
        <p v-if="status.upstream_commit" class="desktop-gate__path">上游提交：{{ status.upstream_commit }}</p>
      </details>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { invoke } from '@tauri-apps/api/core'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'

type BackendPhase = 'starting' | 'waiting_for_dependencies' | 'ready' | 'stopped' | 'error'

interface BackendStatus {
  phase: BackendPhase
  managed: boolean
  pid: number | null
  port: number
  data_dir: string
  core_version: string
  algorithm_version: string
  upstream_commit: string
  message: string
  last_log: string
  problem?: { code: string; title: string; message: string; retryable: boolean } | null
}

const emit = defineEmits<{ ready: [] }>()
const status = ref<BackendStatus | null>(null)
const retrying = ref(false)
let pollTimer: number | null = null
let unlisten: UnlistenFn | null = null
let didEmitReady = false
let disposed = false
let requestSequence = 0
let refreshing = false

const canRetry = computed(() => ['error', 'waiting_for_dependencies', 'stopped'].includes(status.value?.phase || ''))

const title = computed(() => {
  if (status.value?.problem) return status.value.problem.title
  if (status.value?.phase === 'error') return '本地内核需要处理'
  if (status.value?.managed === false) return '正在连接 Sub2API'
  return '正在启动成本运维内核'
})

function acceptStatus(next: BackendStatus) {
  status.value = next
  if (next.phase === 'ready' && !didEmitReady) {
    didEmitReady = true
    emit('ready')
  }
}

async function refreshStatus() {
  if (refreshing || retrying.value || disposed) return
  refreshing = true
  const sequence = ++requestSequence
  try {
    const next = await invoke<BackendStatus>('desktop_backend_status')
    if (!disposed && sequence === requestSequence) acceptStatus(next)
  } catch (error) {
    if (disposed || sequence !== requestSequence) return
    status.value = {
      phase: 'error',
      managed: true,
      pid: null,
      port: 18765,
      data_dir: '',
      core_version: 'unknown',
      algorithm_version: 'unknown',
      upstream_commit: 'unknown',
      message: '无法读取桌面内核状态',
      last_log: error instanceof Error ? error.message : String(error),
    }
  } finally {
    refreshing = false
  }
}

async function retry() {
  if (retrying.value) return
  retrying.value = true
  const sequence = ++requestSequence
  try {
    const next = await invoke<BackendStatus>('desktop_backend_start')
    if (!disposed && sequence === requestSequence) acceptStatus(next)
  } catch (error) {
    if (!disposed && sequence === requestSequence && status.value) {
      status.value.phase = 'error'
      status.value.problem = null
      status.value.message = '重新启动未完成，请查看技术详情后重试'
      status.value.last_log = error instanceof Error ? error.message : String(error)
    }
  } finally {
    retrying.value = false
  }
}

onMounted(async () => {
  try {
    const stopListening = await listen<BackendStatus>('desktop-backend-status', (event) => {
      if (!disposed) { requestSequence += 1; acceptStatus(event.payload) }
    })
    if (disposed) { stopListening(); return }
    unlisten = stopListening
  } catch { /* Polling still recovers when the event channel is unavailable. */ }
  await refreshStatus()
  if (!disposed && !didEmitReady) pollTimer = window.setInterval(refreshStatus, 750)
})

onBeforeUnmount(() => {
  disposed = true
  requestSequence += 1
  if (pollTimer !== null) window.clearInterval(pollTimer)
  unlisten?.()
})
</script>

<style scoped>
.desktop-gate { min-height: 100vh; display: grid; place-items: center; padding: 32px; color: #e8eee8; background-color: #0c100d; background-image: linear-gradient(rgb(99 116 102 / 7%) 1px, transparent 1px), linear-gradient(90deg, rgb(99 116 102 / 7%) 1px, transparent 1px), radial-gradient(circle at 85% 10%, rgb(185 229 90 / 8%), transparent 30%); background-size: 42px 42px, 42px 42px, auto; }
.desktop-gate__card { width: min(560px, 100%); padding: 42px; border: 1px solid #2a332c; background: rgb(18 23 19 / 96%); box-shadow: 0 28px 80px rgb(0 0 0 / 42%); }
.desktop-gate__mark { display: grid; width: 48px; height: 48px; place-items: center; margin-bottom: 28px; color: #0d120e; background: #b9e55a; font: 800 24px/1 'Cascadia Mono', monospace; }
.desktop-gate__eyebrow { margin: 0 0 8px; color: #a7d64b; font: 11px/1.4 'Cascadia Mono', monospace; letter-spacing: .12em; }
h1 { margin: 0; font-size: 27px; line-height: 1.25; }
.desktop-gate__message { min-height: 24px; margin: 12px 0 22px; color: #9ba79e; }
.desktop-gate__progress { height: 3px; overflow: hidden; margin-bottom: 26px; background: #273028; }
.desktop-gate__progress i { display: block; width: 34%; height: 100%; background: #b9e55a; animation: gate-progress 1.25s ease-in-out infinite; }
.desktop-gate__facts { display: grid; grid-template-columns: repeat(3, 1fr); margin: 0; border: 1px solid #273028; }
.desktop-gate__facts div { min-width: 0; padding: 13px; border-right: 1px solid #273028; }
.desktop-gate__facts div:last-child { border-right: 0; }
.desktop-gate__facts dt { color: #708078; font-size: 11px; }
.desktop-gate__facts dd { overflow: hidden; margin: 6px 0 0; color: #d7dfd8; font: 12px/1.4 'Cascadia Mono', monospace; text-overflow: ellipsis; white-space: nowrap; }
.desktop-gate__error { margin-top: 22px; padding: 16px; border-left: 3px solid #dc745c; background: #261b17; }
.desktop-gate__error strong { color: #f0a18e; }
.desktop-gate__error p { margin: 7px 0 13px; color: #c4aaa3; font-size: 12px; line-height: 1.55; word-break: break-word; }
.desktop-gate__error button { min-height: 34px; padding: 0 15px; color: #e9f4d8; border: 1px solid #779c35; background: transparent; }
.desktop-gate__error button:hover:not(:disabled) { color: #10150f; background: #b9e55a; }
.desktop-gate__path { overflow: hidden; margin: 18px 0 0; color: #5e6d63; font: 10px/1.4 'Cascadia Mono', monospace; text-overflow: ellipsis; white-space: nowrap; }
.desktop-gate__details { margin-top: 22px; color: #9ba79e; font-size: 12px; }
.desktop-gate__details summary { cursor: pointer; }
.desktop-gate__log { overflow-wrap: anywhere; white-space: pre-wrap; line-height: 1.6; }
@media (prefers-reduced-motion: reduce) { .desktop-gate__progress i { animation: none; width: 100%; } }
@keyframes gate-progress { 0% { transform: translateX(-110%); } 60%, 100% { transform: translateX(300%); } }
@media (max-width: 620px) { .desktop-gate__card { padding: 28px; }.desktop-gate__facts { grid-template-columns: 1fr; }.desktop-gate__facts div { border-right: 0; border-bottom: 1px solid #273028; }.desktop-gate__facts div:last-child { border-bottom: 0; } }
</style>
