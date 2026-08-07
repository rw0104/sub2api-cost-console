import { describe, expect, it } from 'vitest'
import type { Account } from '@/types'
import {
  buildAccountPoolProviderTabs,
  buildUpstreamProviderTabs,
  classifyUpstreamProvider,
  describeUpstreamOrigin,
  matchesAccountPoolProvider,
  matchesUpstreamProvider,
} from '../upstreamProvider'

function makeAccount(overrides: Partial<Account>): Account {
  return {
    id: 1,
    name: 'account',
    platform: 'openai',
    type: 'apikey',
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    error_message: null,
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: false,
    created_at: '2026-08-06T00:00:00Z',
    updated_at: '2026-08-06T00:00:00Z',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    ...overrides,
  } as Account
}

describe('upstream provider classification', () => {
  it.each([
    [{ platform: 'openai', type: 'oauth' }, 'codex'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.openai.com/v1' } }, 'openai'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.deepseek.com/v1' } }, 'deepseek'],
    [{ platform: 'anthropic', type: 'apikey', credentials: { base_url: 'https://api.anthropic.com' } }, 'anthropic'],
    [{ platform: 'gemini', type: 'service_account', credentials: { base_url: 'https://us-central1-aiplatform.googleapis.com' } }, 'gemini'],
    [{ platform: 'antigravity', type: 'oauth' }, 'antigravity'],
    [{ platform: 'grok', type: 'apikey', credentials: { base_url: 'https://us-east-1.api.x.ai/v1' } }, 'grok'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.mistral.ai/v1' } }, 'mistral'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.groq.com/openai/v1' } }, 'groq'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.together.xyz/v1' } }, 'together'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.fireworks.ai/inference/v1' } }, 'fireworks'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.cohere.com/v2' } }, 'cohere'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.perplexity.ai' } }, 'perplexity'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.z.ai/api/paas/v4' } }, 'zai'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.moonshot.cn/v1' } }, 'moonshot'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.minimax.io/v1' } }, 'minimax'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://dashscope.aliyuncs.com/compatible-mode/v1' } }, 'alibaba'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://api.siliconflow.cn/v1' } }, 'siliconflow'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://openrouter.ai/api/v1' } }, 'openrouter'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://tenant.openai.azure.com/openai/v1' } }, 'azure-openai'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://ark.cn-beijing.volces.com/api/v3' } }, 'volcengine'],
    [{ platform: 'anthropic', type: 'bedrock' }, 'bedrock'],
    [{ platform: 'openai', type: 'apikey', credentials: { base_url: 'https://relay.example.com/v1' } }, 'relay'],
    [{ platform: 'openai', type: 'upstream' }, 'relay'],
  ] as const)('classifies %o as %s', (overrides, expected) => {
    expect(classifyUpstreamProvider(makeAccount(overrides as Partial<Account>))).toBe(expected)
  })

  it('treats an enabled Anthropic custom base URL as a relay', () => {
    const account = makeAccount({
      platform: 'anthropic',
      type: 'oauth',
      custom_base_url_enabled: true,
      custom_base_url: 'https://relay.example.net/anthropic',
    })
    expect(classifyUpstreamProvider(account)).toBe('relay')
    expect(describeUpstreamOrigin(account)).toBe('API 中转 · relay.example.net')
  })

  it('builds only the provider tabs that exist and keeps accurate counts', () => {
    const accounts = [
      makeAccount({ id: 1, type: 'oauth' }),
      makeAccount({ id: 2, credentials: { base_url: 'https://api.deepseek.com' } }),
      makeAccount({ id: 3, credentials: { base_url: 'https://relay.example.com/v1' } }),
      makeAccount({ id: 4, credentials: { base_url: 'https://relay-two.example.com/v1' } }),
    ]

    expect(buildUpstreamProviderTabs(accounts)).toEqual([
      { key: 'all', label: '全部', count: 4 },
      { key: 'codex', label: 'Codex', count: 1 },
      { key: 'deepseek', label: 'DeepSeek 官方', count: 1 },
      { key: 'relay', label: 'API 中转', count: 2 },
    ])
    expect(matchesUpstreamProvider(accounts[1], 'deepseek')).toBe(true)
    expect(matchesUpstreamProvider(accounts[1], 'relay')).toBe(false)
  })

  it('builds the account cost pool from every configured provider instead of two fixed channels', () => {
    const accounts = [
      makeAccount({ id: 1, type: 'oauth' }),
      makeAccount({ id: 2, credentials: { base_url: 'https://api.deepseek.com' } }),
      makeAccount({ id: 3, platform: 'anthropic', type: 'oauth' }),
      makeAccount({ id: 4, credentials: { base_url: 'https://relay.example.com/v1' } }),
    ]

    expect(buildAccountPoolProviderTabs(accounts)).toEqual([
      { key: 'all', label: '全部', count: 4 },
      { key: 'codex', label: 'Codex', count: 1 },
      { key: 'deepseek', label: 'DeepSeek 官方', count: 1 },
      { key: 'anthropic', label: 'Claude 官方', count: 1 },
      { key: 'relay', label: 'API 中转', count: 1 },
    ])
    expect(matchesAccountPoolProvider(accounts[1], 'deepseek')).toBe(true)
    expect(matchesAccountPoolProvider(accounts[2], 'deepseek')).toBe(false)
  })
})
