<template>
  <section class="cost-api-access" aria-labelledby="api-access-title">
    <div class="cost-api-access__heading">
      <div>
        <span class="cost-api-access__eyebrow">LOCAL GATEWAY / AGENT ACCESS</span>
        <h2 id="api-access-title">API 接入中心</h2>
        <p>为 Codex、OpenCode、Cursor、Cline 与自定义 Agent 提供可复制的本地接口配置。</p>
      </div>
      <div class="cost-api-access__heading-actions">
        <button type="button" class="cost-api-button cost-api-button--quiet" :disabled="loadingKeys" @click="loadKeys">
          <RefreshCcw :size="15" :class="{ 'cost-api-spin': loadingKeys }" />
          {{ loadingKeys ? '读取中' : '刷新密钥' }}
        </button>
        <button type="button" class="cost-api-button cost-api-button--outline" @click="openKeyManagement">
          <ExternalLink :size="15" /> 管理 API Key
        </button>
      </div>
    </div>

    <div class="cost-api-status-grid">
      <div class="cost-api-status-card cost-api-status-card--lime">
        <span>本地网关地址</span>
        <strong>{{ gatewayBase }}</strong>
        <button type="button" class="cost-api-copy" @click="copyToClipboard(gatewayBase, '接口地址已复制')">
          <Copy :size="14" /> 复制
        </button>
        <small>{{ desktopMode ? '仅本机可访问 · 桌面内核运行时有效' : '使用当前站点公开接口地址' }}</small>
      </div>
      <div class="cost-api-status-card">
        <span>鉴权方式</span>
        <strong>Bearer API Key</strong>
        <small>请求头：Authorization</small>
        <small>不会使用 tauri.localhost 或 ChatGPT OAuth Cookie</small>
      </div>
      <div class="cost-api-status-card" :class="connectionClass">
        <span>接口诊断</span>
        <strong>{{ connectionLabel }}</strong>
        <small v-if="lastProbeMs !== null">模型清单请求 {{ lastProbeMs }} ms</small>
        <small v-else>只测试 /v1/models，不产生模型调用成本</small>
        <small v-if="opsOverview">上游 TTFT P95：{{ formatLatency(opsOverview.ttft?.p95_ms) }}</small>
      </div>
    </div>

    <div class="cost-api-access__grid">
      <div class="cost-api-card cost-api-card--setup">
        <div class="cost-api-card__title">
          <div><span>01 / SELECT CREDENTIAL</span><strong>选择 API Key 与 Agent</strong></div>
          <ShieldCheck :size="18" />
        </div>

        <label class="cost-api-field">
          <span>API Key</span>
          <select v-model="selectedKeyId" :disabled="loadingKeys || keys.length === 0">
            <option value="">请选择一个启用中的 API Key</option>
            <option v-for="key in activeKeys" :key="key.id" :value="String(key.id)">
              {{ key.name }} · {{ key.group?.name || '自动分组' }} · {{ maskKey(key.key) }}
            </option>
          </select>
        </label>
        <p v-if="keyLoadState === 'unavailable'" class="cost-api-empty is-error" role="alert">API Key 无数据：{{ keyLoadError }}。这不是“没有密钥”，请重试读取。</p>
        <p v-else-if="keyLoadState === 'empty'" class="cost-api-empty">API Key 清单读取成功，但当前确实没有密钥。请先创建密钥，再回来生成 Agent 配置。</p>
        <p v-else-if="keyLoadState === 'loading'" class="cost-api-empty">正在读取 API Key，不使用空清单生成配置。</p>
        <p v-else-if="selectedKey" class="cost-api-hint">
          当前密钥：{{ selectedKey.name }} · {{ selectedKey.group?.platform || '未绑定平台' }} · {{ selectedKey.status }}
        </p>

        <div class="cost-api-field">
          <span>接入客户端</span>
          <div class="cost-api-presets" role="tablist" aria-label="Agent 配置模板">
            <button v-for="item in presets" :key="item.id" type="button" role="tab" :aria-selected="preset === item.id" :class="{ active: preset === item.id }" @click="preset = item.id">
              <component :is="item.icon" :size="14" /> {{ item.label }}
            </button>
          </div>
        </div>

        <label class="cost-api-field">
          <span>默认模型</span>
          <select v-model="selectedModel" :disabled="models.length === 0">
            <option v-if="models.length === 0" value="">先测试接口获取模型</option>
            <option v-for="model in models" :key="model" :value="model">{{ model }}</option>
          </select>
        </label>

        <div class="cost-api-actions">
          <button type="button" class="cost-api-button cost-api-button--primary" :disabled="testing || !selectedKey" @click="testConnection">
            <LoaderCircle v-if="testing" :size="15" class="cost-api-spin" />
            <Wifi v-else :size="15" />
            {{ testing ? '测试中…' : '测试本地接口' }}
          </button>
          <button type="button" class="cost-api-button cost-api-button--quiet" :disabled="!selectedKey" @click="copyKeyCommand">
            <Copy :size="15" /> 复制密钥命令
          </button>
        </div>

        <div v-if="testMessage" class="cost-api-test-result" :class="testOk ? 'is-ok' : 'is-error'" role="status">
          <CheckCircle2 v-if="testOk" :size="16" />
          <TriangleAlert v-else :size="16" />
          <span>{{ testMessage }}</span>
        </div>
      </div>

      <div class="cost-api-card cost-api-card--code">
        <div class="cost-api-card__title">
          <div><span>02 / COPY CONFIGURATION</span><strong>{{ configTitle }}</strong></div>
          <button type="button" class="cost-api-copy" @click="copyToClipboard(configText, '配置已复制')"><Copy :size="14" /> 复制配置</button>
        </div>
        <pre class="cost-api-code"><code>{{ configText }}</code></pre>
        <p class="cost-api-code-note">配置不包含明文密钥。复制右侧“密钥命令”后，在当前用户环境中设置 <code>OPENAI_API_KEY</code>，然后重启 Agent。</p>
      </div>
    </div>

    <div class="cost-api-diagnostics">
      <div>
        <span class="cost-api-access__eyebrow">LATENCY TRIAGE</span>
        <strong>本地请求与上游模型延迟分开统计</strong>
      </div>
      <div class="cost-api-diagnostics__items">
        <span><b>本地网关</b> {{ lastProbeMs === null ? '未测试' : `${lastProbeMs} ms` }}</span>
        <span><b>上游 TTFT P95</b> {{ formatLatency(opsOverview?.ttft?.p95_ms) }}</span>
        <span><b>上游总耗时 P95</b> {{ formatLatency(opsOverview?.duration?.p95_ms) }}</span>
      </div>
      <p>127.0.0.1 的 68–131ms 管理请求属于本地开销；如果 /v1/responses 显示几十秒到数分钟，通常来自上游排队、代理、账号切换或首 token 等待，不是本地网关网络延迟。</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { CheckCircle2, Copy, ExternalLink, KeyRound, LoaderCircle, RefreshCcw, ShieldCheck, Terminal, TriangleAlert, Wifi } from '@lucide/vue'
import { authAPI, keysAPI } from '@/api'
import type { ApiKey } from '@/types'
import type { OpsDashboardOverview } from '@/api/admin/ops'
import { useClipboard } from '@/composables/useClipboard'

const LOCAL_GATEWAY_BASE = 'http://127.0.0.1:18765/v1'

type PresetId = 'codex' | 'opencode' | 'cursor' | 'cline' | 'python' | 'node' | 'curl'

const props = defineProps<{
  desktopMode: boolean
  opsOverview?: OpsDashboardOverview | null
}>()

const router = useRouter()
const { copyToClipboard } = useClipboard()
const keys = ref<ApiKey[]>([])
const loadingKeys = ref(false)
const keyLoadState = ref<'loading' | 'measured' | 'empty' | 'unavailable'>('loading')
const keyLoadError = ref('')
const selectedKeyId = ref('')
const preset = ref<PresetId>('codex')
const models = ref<string[]>([])
const selectedModel = ref('')
const testing = ref(false)
const lastProbeMs = ref<number | null>(null)
const testMessage = ref('')
const testOk = ref(false)
const publicBaseUrl = ref('')

const presets = [
  { id: 'codex' as const, label: 'Codex CLI', icon: Terminal },
  { id: 'opencode' as const, label: 'OpenCode', icon: KeyRound },
  { id: 'cursor' as const, label: 'Cursor', icon: KeyRound },
  { id: 'cline' as const, label: 'Cline', icon: KeyRound },
  { id: 'python' as const, label: 'Python', icon: Terminal },
  { id: 'node' as const, label: 'Node.js', icon: Terminal },
  { id: 'curl' as const, label: 'curl / SDK', icon: Wifi },
]

const gatewayBase = computed(() => {
  if (props.desktopMode) return LOCAL_GATEWAY_BASE
  const value = publicBaseUrl.value || window.location.origin
  const trimmed = value.replace(/\/+$/, '')
  return trimmed.endsWith('/v1') ? trimmed : `${trimmed}/v1`
})

const activeKeys = computed(() => keys.value.filter((key) => key.status === 'active'))
const selectedKey = computed(() => keys.value.find((key) => String(key.id) === selectedKeyId.value) || null)
const connectionLabel = computed(() => testing.value ? '正在测试' : testOk.value ? '接口正常' : lastProbeMs.value !== null ? '接口异常' : '未测试')
const connectionClass = computed(() => testOk.value ? 'is-ok' : lastProbeMs.value !== null ? 'is-error' : '')
const configTitle = computed(() => {
  if (preset.value === 'codex') return 'Codex CLI · config.toml'
  if (preset.value === 'opencode') return 'OpenCode · opencode.json'
  if (preset.value === 'cursor') return 'Cursor · OpenAI-compatible provider'
  if (preset.value === 'cline') return 'Cline · OpenAI-compatible provider'
  if (preset.value === 'python') return 'Python · OpenAI SDK'
  if (preset.value === 'node') return 'Node.js · OpenAI SDK'
  return 'curl · PowerShell'
})
const configText = computed(() => {
  const model = selectedModel.value || '从 /v1/models 选择模型'
  if (preset.value === 'opencode') {
    return JSON.stringify({
      '$schema': 'https://opencode.ai/config.json',
      provider: {
        Sub2APILocal: {
          npm: '@ai-sdk/openai-compatible',
          name: 'Sub2API Local',
          options: { baseURL: gatewayBase.value, apiKey: '{env:OPENAI_API_KEY}' },
          models: { [model]: { name: model } },
        },
      },
    }, null, 2)
  }
  if (preset.value === 'curl') {
    return `$env:OPENAI_API_KEY = "<从 API Key 管理页复制>"\n\n$headers = @{ Authorization = "Bearer $env:OPENAI_API_KEY"; "Content-Type" = "application/json" }\n$body = @{ model = "${model}"; input = "请只回复：本地接口正常" } | ConvertTo-Json -Depth 10\nInvoke-RestMethod "${gatewayBase.value}/responses" -Method Post -Headers $headers -Body $body`
  }
  if (preset.value === 'python') {
    return `import os\nfrom openai import OpenAI\n\nclient = OpenAI(\n    api_key=os.environ["OPENAI_API_KEY"],\n    base_url="${gatewayBase.value}",\n)\n\nresponse = client.responses.create(\n    model="${model}",\n    input="请只回复：本地接口正常",\n)\nprint(response.output_text)`
  }
  if (preset.value === 'node') {
    return `import OpenAI from "openai";\n\nconst client = new OpenAI({\n  apiKey: process.env.OPENAI_API_KEY,\n  baseURL: "${gatewayBase.value}",\n});\n\nconst response = await client.responses.create({\n  model: "${model}",\n  input: "请只回复：本地接口正常",\n});\nconsole.log(response.output_text);`
  }
  if (preset.value === 'cursor' || preset.value === 'cline') {
    return `Provider: OpenAI-compatible\nBase URL: ${gatewayBase.value}\nModel: ${model}\nAPI Key: 使用环境变量 OPENAI_API_KEY\n\n说明：在 ${preset.value === 'cursor' ? 'Cursor' : 'Cline'} 的 OpenAI-compatible / 自定义 Provider 设置中填入以上地址；不要把 tauri.localhost 填入外部 Agent。`
  }
  return `# %USERPROFILE%\\.codex\\config.toml\nmodel_provider = "Sub2APILocal"\nmodel = "${model}"\nreview_model = "${model}"\nmodel_reasoning_effort = "xhigh"\ndisable_response_storage = true\nnetwork_access = "enabled"\nwindows_wsl_setup_acknowledged = true\n\n[model_providers.Sub2APILocal]\nname = "Sub2API Local"\nbase_url = "${gatewayBase.value}"\nwire_api = "responses"\nrequires_openai_auth = false\n\n[features]\ngoals = true\n\n# %USERPROFILE%\\.codex\\auth.json\n# { "OPENAI_API_KEY": "<从 API Key 管理页复制>" }`
})

function maskKey(key: string): string {
  const value = String(key || '')
  if (value.length <= 10) return '••••••••'
  return `${value.slice(0, 4)}••••${value.slice(-4)}`
}

function formatLatency(value?: number | null): string {
  const ms = Number(value)
  if (!Number.isFinite(ms) || ms <= 0) return '无样本'
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms)} ms`
}

async function loadKeys() {
  loadingKeys.value = true
  keyLoadState.value = 'loading'
  keyLoadError.value = ''
  try {
    const [keyResult, settingsResult] = await Promise.allSettled([
      keysAPI.list(1, 100, { sort_by: 'created_at', sort_order: 'desc' }),
      authAPI.getPublicSettings(),
    ])
    if (keyResult.status === 'fulfilled') {
      keys.value = keyResult.value.items || []
      keyLoadState.value = keys.value.length ? 'measured' : 'empty'
      if (!selectedKeyId.value || !keys.value.some((key) => String(key.id) === selectedKeyId.value)) {
        selectedKeyId.value = String(activeKeys.value[0]?.id || '')
      }
    } else {
      keys.value = []
      selectedKeyId.value = ''
      keyLoadState.value = 'unavailable'
      keyLoadError.value = keyResult.reason instanceof Error ? keyResult.reason.message : '密钥接口读取失败'
    }
    if (settingsResult.status === 'fulfilled') publicBaseUrl.value = settingsResult.value.api_base_url || ''
  } finally {
    loadingKeys.value = false
  }
}

async function testConnection() {
  if (!selectedKey.value) return
  testing.value = true
  testMessage.value = ''
  testOk.value = false
  const started = performance.now()
  try {
    const controller = new AbortController()
    const timeout = window.setTimeout(() => controller.abort(), 8_000)
    const response = await fetch(`${gatewayBase.value}/models`, {
      headers: { Authorization: `Bearer ${selectedKey.value.key}` },
      signal: controller.signal,
    })
    window.clearTimeout(timeout)
    lastProbeMs.value = Math.round(performance.now() - started)
    const payload = await response.json().catch(() => null) as any
    if (!response.ok) {
      const detail = payload?.error?.message || payload?.message || `HTTP ${response.status}`
      throw new Error(detail)
    }
    const entries = Array.isArray(payload?.data) ? payload.data : Array.isArray(payload?.models) ? payload.models : []
    models.value = entries.map((item: any) => typeof item === 'string' ? item : item?.id || item?.model || item?.slug).filter(Boolean)
    if (!selectedModel.value || !models.value.includes(selectedModel.value)) selectedModel.value = models.value[0] || ''
    testOk.value = true
    testMessage.value = `接口正常，${models.value.length ? `已发现 ${models.value.length} 个模型` : '已通过鉴权'}（${lastProbeMs.value} ms）`
  } catch (error: any) {
    lastProbeMs.value = Math.round(performance.now() - started)
    testMessage.value = error?.name === 'AbortError' ? '接口测试超过 8 秒未返回，请检查内核状态' : error?.message || '连接失败，请确认内核已就绪且 API Key 有效'
  } finally {
    testing.value = false
  }
}

async function copyKeyCommand() {
  if (!selectedKey.value) return
  await copyToClipboard(`$env:OPENAI_API_KEY = "${selectedKey.value.key}"`, 'PowerShell 密钥命令已复制')
}

function openKeyManagement() {
  router.push('/keys')
}

watch(selectedKeyId, () => {
  models.value = []
  selectedModel.value = ''
  testMessage.value = ''
  testOk.value = false
  lastProbeMs.value = null
})

onMounted(loadKeys)
</script>

<style scoped>
.cost-api-access { padding: 26px; color: var(--cost-text, #e9ede6); }
.cost-api-access__heading { display: flex; align-items: flex-end; justify-content: space-between; gap: 20px; margin-bottom: 22px; }
.cost-api-access__heading h2 { margin: 6px 0 0; font-size: clamp(24px, 2.1vw, 34px); letter-spacing: -.02em; }
.cost-api-access__heading p { margin: 7px 0 0; color: var(--cost-muted, #7f8b81); font-size: 13px; }
.cost-api-access__eyebrow, .cost-api-card__title span { color: var(--cost-lime, #b9e55a); font: 10px 'Cascadia Mono', Consolas, monospace; letter-spacing: .07em; }
.cost-api-access__heading-actions, .cost-api-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.cost-api-button { display: inline-flex; align-items: center; justify-content: center; gap: 7px; min-height: 36px; border: 1px solid var(--cost-line, #303830); border-radius: 10px; padding: 0 13px; color: var(--cost-text, #e9ede6); background: #171d18; cursor: pointer; transition: border-color .16s ease, background .16s ease, transform .16s ease; }
.cost-api-button:hover:not(:disabled) { border-color: var(--cost-lime, #b9e55a); background: #202a20; }
.cost-api-button:disabled { cursor: not-allowed; opacity: .48; }
.cost-api-button--primary { color: #10140f; background: var(--cost-lime, #b9e55a); border-color: var(--cost-lime, #b9e55a); font-weight: 700; }
.cost-api-button--outline { border-color: var(--cost-lime, #b9e55a); color: var(--cost-lime, #b9e55a); }
.cost-api-button--quiet { color: var(--cost-muted, #7f8b81); }
.cost-api-status-grid { display: grid; grid-template-columns: 1.35fr 1fr 1fr; border: 1px solid var(--cost-line, #303830); border-radius: 15px; overflow: hidden; background: #121713; }
.cost-api-status-card { min-height: 126px; padding: 17px 19px; border-right: 1px solid var(--cost-line, #303830); }
.cost-api-status-card:last-child { border-right: 0; }
.cost-api-status-card > span { display: block; color: var(--cost-muted, #7f8b81); font-size: 11px; }
.cost-api-status-card > strong { display: block; margin: 10px 0 5px; font: 600 18px 'Cascadia Mono', Consolas, monospace; color: #eef2eb; overflow-wrap: anywhere; }
.cost-api-status-card > small { display: block; margin-top: 4px; color: var(--cost-muted, #7f8b81); font-size: 11px; }
.cost-api-status-card--lime { border-left: 3px solid var(--cost-lime, #b9e55a); }
.cost-api-status-card.is-ok { border-left: 3px solid #6dcf8d; }
.cost-api-status-card.is-error { border-left: 3px solid #d58473; }
.cost-api-copy { display: inline-flex; align-items: center; gap: 5px; border: 0; border-radius: 7px; padding: 4px 7px; color: var(--cost-lime, #b9e55a); background: rgb(185 229 90 / 9%); font-size: 11px; cursor: pointer; }
.cost-api-copy:hover { background: rgb(185 229 90 / 17%); }
.cost-api-access__grid { display: grid; grid-template-columns: minmax(320px, .8fr) minmax(420px, 1.2fr); gap: 14px; margin-top: 14px; }
.cost-api-card { min-width: 0; border: 1px solid var(--cost-line, #303830); border-radius: 15px; background: #121713; }
.cost-api-card--setup { padding: 20px; }
.cost-api-card--code { padding: 20px; display: flex; flex-direction: column; }
.cost-api-card__title { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; margin-bottom: 18px; }
.cost-api-card__title strong { display: block; margin-top: 6px; font-size: 17px; }
.cost-api-card__title > svg { color: var(--cost-lime, #b9e55a); }
.cost-api-field { display: block; margin-top: 14px; }
.cost-api-field > span { display: block; margin-bottom: 7px; color: var(--cost-muted, #7f8b81); font-size: 12px; }
.cost-api-field select { width: 100%; min-height: 38px; border: 1px solid var(--cost-line-strong, #424d43); border-radius: 9px; padding: 0 11px; color: var(--cost-text, #e9ede6); background: #0e130f; outline: none; }
.cost-api-field select:focus { border-color: var(--cost-lime, #b9e55a); }
.cost-api-presets { display: flex; flex-wrap: wrap; gap: 6px; }
.cost-api-presets button { display: inline-flex; align-items: center; gap: 6px; min-height: 34px; border: 1px solid var(--cost-line, #303830); border-radius: 8px; padding: 0 10px; color: var(--cost-muted, #7f8b81); background: #171d18; cursor: pointer; }
.cost-api-presets button.active { border-color: var(--cost-lime, #b9e55a); color: #11160f; background: var(--cost-lime, #b9e55a); font-weight: 700; }
.cost-api-hint, .cost-api-empty { margin: 8px 0 0; color: var(--cost-muted, #7f8b81); font-size: 11px; line-height: 1.55; }
.cost-api-empty { color: #e0bd4e; }
.cost-api-empty.is-error { border-left: 2px solid #d58473; padding-left: 9px; color: #e3a092; }
.cost-api-actions { margin-top: 20px; }
.cost-api-test-result { display: flex; align-items: flex-start; gap: 7px; margin-top: 14px; border-radius: 9px; padding: 10px 11px; font-size: 12px; line-height: 1.45; }
.cost-api-test-result.is-ok { color: #a4e7b6; background: rgb(85 177 109 / 12%); }
.cost-api-test-result.is-error { color: #efa18d; background: rgb(213 132 115 / 12%); }
.cost-api-code { flex: 1; min-height: 330px; margin: 0; overflow: auto; border: 1px solid #2a332b; border-radius: 10px; padding: 15px; color: #d9e5d6; background: #0a0e0b; font: 12px/1.65 'Cascadia Mono', Consolas, monospace; white-space: pre-wrap; word-break: break-word; user-select: text; }
.cost-api-code-note { margin: 12px 0 0; color: var(--cost-muted, #7f8b81); font-size: 11px; line-height: 1.55; }
.cost-api-code-note code { color: var(--cost-lime, #b9e55a); font-family: 'Cascadia Mono', Consolas, monospace; }
.cost-api-diagnostics { display: grid; grid-template-columns: 1fr auto; align-items: center; gap: 14px 24px; margin-top: 14px; border: 1px solid #3e3827; border-radius: 15px; padding: 16px 19px; background: rgb(223 188 76 / 5%); }
.cost-api-diagnostics strong { display: block; margin-top: 6px; font-size: 15px; }
.cost-api-diagnostics__items { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 7px; }
.cost-api-diagnostics__items span { border: 1px solid #3e3827; border-radius: 8px; padding: 8px 10px; color: #d9c77b; font: 11px 'Cascadia Mono', Consolas, monospace; }
.cost-api-diagnostics__items b { color: #f0ead2; font-weight: 600; }
.cost-api-diagnostics p { grid-column: 1 / -1; margin: 0; color: #a29c84; font-size: 11px; line-height: 1.55; }
.cost-api-spin { animation: cost-api-spin .9s linear infinite; }
@keyframes cost-api-spin { to { transform: rotate(360deg); } }
@media (max-width: 980px) { .cost-api-access__heading { align-items: flex-start; flex-direction: column; }.cost-api-status-grid, .cost-api-access__grid { grid-template-columns: 1fr; }.cost-api-status-card { border-right: 0; border-bottom: 1px solid var(--cost-line, #303830); }.cost-api-status-card:last-child { border-bottom: 0; }.cost-api-diagnostics { grid-template-columns: 1fr; }.cost-api-diagnostics__items { justify-content: flex-start; } }
@media (max-width: 620px) { .cost-api-access { padding: 16px; }.cost-api-access__heading-actions, .cost-api-actions { width: 100%; }.cost-api-button { flex: 1; }.cost-api-code { min-height: 260px; font-size: 11px; } }
</style>
