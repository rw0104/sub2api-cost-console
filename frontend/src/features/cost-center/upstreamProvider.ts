import type { Account } from '@/types'

export type UpstreamProviderKey =
  | 'codex'
  | 'openai'
  | 'deepseek'
  | 'anthropic'
  | 'gemini'
  | 'antigravity'
  | 'grok'
  | 'azure-openai'
  | 'mistral'
  | 'groq'
  | 'together'
  | 'fireworks'
  | 'cohere'
  | 'perplexity'
  | 'zai'
  | 'moonshot'
  | 'minimax'
  | 'alibaba'
  | 'siliconflow'
  | 'openrouter'
  | 'volcengine'
  | 'bedrock'
  | 'relay'
  | 'other'

export type UpstreamProviderFilter = 'all' | UpstreamProviderKey

export interface UpstreamProviderTab {
  key: UpstreamProviderFilter
  label: string
  count: number
}

const providerOrder: UpstreamProviderKey[] = [
  'codex',
  'openai',
  'deepseek',
  'anthropic',
  'gemini',
  'antigravity',
  'grok',
  'azure-openai',
  'mistral',
  'groq',
  'together',
  'fireworks',
  'cohere',
  'perplexity',
  'zai',
  'moonshot',
  'minimax',
  'alibaba',
  'siliconflow',
  'openrouter',
  'volcengine',
  'bedrock',
  'relay',
  'other',
]

const providerLabels: Record<UpstreamProviderKey, string> = {
  codex: 'Codex',
  openai: 'OpenAI 官方',
  deepseek: 'DeepSeek 官方',
  anthropic: 'Claude 官方',
  gemini: 'Gemini 官方',
  antigravity: 'Antigravity',
  grok: 'Grok 官方',
  'azure-openai': 'Azure OpenAI',
  mistral: 'Mistral 官方',
  groq: 'Groq',
  together: 'Together AI',
  fireworks: 'Fireworks AI',
  cohere: 'Cohere 官方',
  perplexity: 'Perplexity 官方',
  zai: '智谱 / Z.ai',
  moonshot: 'Moonshot / Kimi',
  minimax: 'MiniMax 官方',
  alibaba: '阿里云百炼',
  siliconflow: '硅基流动',
  openrouter: 'OpenRouter',
  volcengine: '火山方舟',
  bedrock: 'AWS Bedrock',
  relay: 'API 中转',
  other: '其他',
}

function readString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

export function accountUpstreamBaseUrl(account: Account): string {
  if (account.custom_base_url_enabled === true) {
    const customBaseUrl = readString(account.custom_base_url)
    if (customBaseUrl) return customBaseUrl
  }
  return readString(account.credentials?.base_url)
}

function parseHostname(baseUrl: string): string {
  if (!baseUrl) return ''
  try {
    return new URL(baseUrl).hostname.toLowerCase()
  } catch {
    return ''
  }
}

function isGoogleOfficialHost(host: string): boolean {
  return host === 'generativelanguage.googleapis.com'
    || host === 'aiplatform.googleapis.com'
    || host.endsWith('-aiplatform.googleapis.com')
    || host === 'cloudcode-pa.googleapis.com'
}

function providerFromOfficialHost(host: string): UpstreamProviderKey | null {
  if (host === 'api.deepseek.com') return 'deepseek'
  if (host === 'api.openai.com') return 'openai'
  if (host === 'api.anthropic.com') return 'anthropic'
  if (host === 'api.x.ai' || host.endsWith('.api.x.ai')) return 'grok'
  if (isGoogleOfficialHost(host)) return 'gemini'
  if (host.endsWith('.openai.azure.com')) return 'azure-openai'
  if (host === 'api.mistral.ai') return 'mistral'
  if (host === 'api.groq.com') return 'groq'
  if (host === 'api.together.xyz') return 'together'
  if (host === 'api.fireworks.ai') return 'fireworks'
  if (host === 'api.cohere.com' || host === 'api.cohere.ai') return 'cohere'
  if (host === 'api.perplexity.ai') return 'perplexity'
  if (host === 'api.z.ai' || host === 'open.bigmodel.cn') return 'zai'
  if (host === 'api.moonshot.cn' || host === 'api.moonshot.ai') return 'moonshot'
  if (host === 'api.minimax.chat' || host === 'api.minimax.io') return 'minimax'
  if (host === 'dashscope.aliyuncs.com' || host === 'dashscope-intl.aliyuncs.com') return 'alibaba'
  if (host === 'api.siliconflow.cn' || host === 'api.siliconflow.com') return 'siliconflow'
  if (host === 'openrouter.ai') return 'openrouter'
  if (host.endsWith('.volces.com')) return 'volcengine'
  return null
}

export function classifyUpstreamProvider(account: Account): UpstreamProviderKey {
  if (account.type === 'bedrock') return 'bedrock'

  const baseUrl = accountUpstreamBaseUrl(account)
  const host = parseHostname(baseUrl)
  const providerFromHost = providerFromOfficialHost(host)
  if (providerFromHost) return providerFromHost

  // A configured non-official endpoint is a relay even when it reuses the
  // OpenAI/Anthropic protocol. This distinction is important for cost truth.
  if (baseUrl) return 'relay'
  if (account.type === 'upstream') return 'relay'

  if (account.platform === 'openai') {
    return account.type === 'oauth' || account.type === 'setup-token' ? 'codex' : 'openai'
  }
  if (account.platform === 'anthropic') return 'anthropic'
  if (account.platform === 'gemini') return 'gemini'
  if (account.platform === 'antigravity') return 'antigravity'
  if (account.platform === 'grok') return 'grok'
  return 'other'
}

export function matchesUpstreamProvider(account: Account, filter: UpstreamProviderFilter): boolean {
  return filter === 'all' || classifyUpstreamProvider(account) === filter
}

export function buildUpstreamProviderTabs(accounts: Account[]): UpstreamProviderTab[] {
  const counts = new Map<UpstreamProviderKey, number>()
  for (const account of accounts) {
    const key = classifyUpstreamProvider(account)
    counts.set(key, (counts.get(key) || 0) + 1)
  }

  return [
    { key: 'all', label: '全部', count: accounts.length },
    ...providerOrder
      .filter((key) => (counts.get(key) || 0) > 0)
      .map((key) => ({ key, label: providerLabels[key], count: counts.get(key) || 0 })),
  ]
}

// The cost pool covers every configured upstream account. Keep these named
// entry points separate from the ranking table so the pool cannot regress to
// a hand-written list of selected vendors again.
export function buildAccountPoolProviderTabs(accounts: Account[]): UpstreamProviderTab[] {
  return buildUpstreamProviderTabs(accounts)
}

export function matchesAccountPoolProvider(account: Account, filter: UpstreamProviderFilter): boolean {
  return matchesUpstreamProvider(account, filter)
}

export function describeUpstreamOrigin(account: Account): string {
  const provider = classifyUpstreamProvider(account)
  if (provider === 'relay') {
    const host = parseHostname(accountUpstreamBaseUrl(account))
    return host ? `API 中转 · ${host}` : 'API 中转'
  }
  if (provider === 'codex') return 'Codex OAuth'
  if (provider === 'other') return `${account.platform} · ${account.type}`
  return providerLabels[provider]
}
