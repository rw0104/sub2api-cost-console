<template>
  <section v-if="view?.editable" class="desktop-listen">
    <div class="desktop-listen__title">
      <span>本地服务监听</span>
      <strong>{{ view.active_host }}:{{ view.active_port }}</strong>
    </div>
    <form @submit.prevent="save">
      <label>
        <span>监听范围</span>
        <select v-model="mode" :disabled="working || busy">
          <option value="127.0.0.1">仅本机（127.0.0.1）</option>
          <option value="0.0.0.0">局域网可访问（0.0.0.0）</option>
          <option value="custom">自定义 IPv4</option>
        </select>
      </label>
      <label v-if="mode === 'custom'">
        <span>IPv4 地址</span>
        <input v-model.trim="customHost" type="text" placeholder="192.168.1.10" autocomplete="off" spellcheck="false" :disabled="working || busy" />
      </label>
      <label>
        <span>端口</span>
        <input v-model.trim="portText" type="text" inputmode="numeric" maxlength="5" autocomplete="off" :disabled="working || busy" />
      </label>
      <button type="submit" :disabled="working || busy || !changed">保存</button>
    </form>
    <p v-if="exposed" class="desktop-listen__warning">
      非本机地址会让局域网内的设备访问管理后台与 API（明文 HTTP），请确认管理员密码足够强并留意系统防火墙提示。首次安装向导完成前始终只监听本机。
    </p>
    <p v-if="errorMessage" class="desktop-listen__error" role="alert">{{ errorMessage }}</p>
    <div v-if="view.restart_required" class="desktop-listen__restart">
      <span>已保存 {{ view.host }}:{{ view.port }}，重启桌面端后生效；已配置的外部客户端需改用新地址。</span>
      <button type="button" :disabled="working || busy" @click="restart">立即重启并应用</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { invoke } from '@tauri-apps/api/core'
import { relaunch } from '@tauri-apps/plugin-process'

interface ListenSettingsView {
  host: string
  port: number
  active_host: string
  active_port: number
  default_port: number
  editable: boolean
  restart_required: boolean
}

defineProps<{ busy?: boolean }>()

const view = ref<ListenSettingsView | null>(null)
const mode = ref<'127.0.0.1' | '0.0.0.0' | 'custom'>('127.0.0.1')
const customHost = ref('')
const portText = ref('')
const working = ref(false)
const errorMessage = ref('')

const host = computed(() => (mode.value === 'custom' ? customHost.value : mode.value))
const port = computed(() => (/^\d{1,5}$/.test(portText.value) ? Number(portText.value) : NaN))
const changed = computed(() => !!view.value && (host.value !== view.value.host || port.value !== view.value.port))
const exposed = computed(() => !/^(localhost|127(\.\d{1,3}){3})$/i.test(host.value))

function accept(next: ListenSettingsView | null | undefined) {
  if (!next) return
  view.value = next
  mode.value = next.host === '127.0.0.1' || next.host === '0.0.0.0' ? next.host : 'custom'
  customHost.value = mode.value === 'custom' ? next.host : ''
  portText.value = String(next.port)
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

async function save() {
  if (working.value) return
  if (!Number.isInteger(port.value)) {
    errorMessage.value = '端口必须是 1024 到 65535 之间的数字'
    return
  }
  working.value = true
  errorMessage.value = ''
  try {
    accept(await invoke<ListenSettingsView>('desktop_listen_settings_save', { host: host.value, port: port.value }))
  } catch (error) {
    errorMessage.value = messageOf(error)
  } finally {
    working.value = false
  }
}

async function restart() {
  if (working.value) return
  working.value = true
  errorMessage.value = ''
  try {
    await invoke('desktop_backend_prepare_relaunch')
    await relaunch()
  } catch (error) {
    errorMessage.value = messageOf(error)
    working.value = false
  }
}

onMounted(async () => {
  try {
    accept(await invoke<ListenSettingsView>('desktop_listen_settings'))
  } catch {
    /* Older shells have no listen settings; the section stays hidden. */
  }
})
</script>

<style scoped>
.desktop-listen { margin: 14px 0 0; padding: 14px 16px; border: 1px solid #303b32; border-radius: 12px; background: #161c17; }
.desktop-listen__title { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }
.desktop-listen__title span { color: #7f8e82; font-size: 10px; }
.desktop-listen__title strong { color: #dce6dd; font: 12px 'Cascadia Mono', monospace; }
.desktop-listen form { display: grid; grid-template-columns: repeat(auto-fit, minmax(120px, 1fr)); align-items: end; gap: 8px; margin-top: 12px; }
.desktop-listen label span { display: block; margin-bottom: 4px; color: #718078; font-size: 10px; }
.desktop-listen input, .desktop-listen select { width: 100%; min-height: 32px; padding: 0 9px; color: #dce6dd; border: 1px solid #38443a; border-radius: 8px; background: #111611; font: 12px 'Cascadia Mono', monospace; }
.desktop-listen button { min-height: 32px; padding: 0 12px; color: #11160f; border: 1px solid #b9e55a; border-radius: 8px; background: #b9e55a; font-size: 11px; font-weight: 700; }
.desktop-listen button:disabled { opacity: .45; }
.desktop-listen p { margin: 10px 0 0; font-size: 10px; line-height: 1.6; }
.desktop-listen__warning { color: #d7b26f; }
.desktop-listen__error { color: #e4a08e; }
.desktop-listen__restart { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-top: 10px; color: #9cbd65; font-size: 10px; line-height: 1.6; }
.desktop-listen__restart button { flex: 0 0 auto; }
</style>
