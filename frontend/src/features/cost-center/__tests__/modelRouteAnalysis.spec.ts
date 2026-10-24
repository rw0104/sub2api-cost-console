import { describe, expect, it } from 'vitest'
import type { AdminUsageLog } from '@/types'
import type { Channel } from '@/api/admin/channels'
import { buildModelRouteRows } from '../modelRouteAnalysis'

function usage(overrides: Partial<AdminUsageLog> = {}): AdminUsageLog {
  return {
    id: 1,
    user_id: 1,
    api_key_id: 1,
    account_id: 13,
    request_id: 'req-1',
    model: 'deepseek-chat',
    upstream_model: 'deepseek-v4-flash',
    model_mapping_chain: 'deepseek-chat → deepseek-v4-flash',
    channel_id: 7,
    inbound_endpoint: '/v1/chat/completions',
    upstream_endpoint: '/chat/completions',
    group_id: 3,
    subscription_id: null,
    input_tokens: 98,
    output_tokens: 356,
    cache_creation_tokens: 0,
    cache_read_tokens: 128,
    cache_creation_5m_tokens: 0,
    cache_creation_1h_tokens: 0,
    input_cost: 0.00001372,
    output_cost: 0.00009968,
    cache_creation_cost: 0,
    cache_read_cost: 0.0000003584,
    total_cost: 0.0001137584,
    actual_cost: 0.0002,
    rate_multiplier: 1,
    account_rate_multiplier: 1,
    long_context_billing_applied: false,
    billing_type: 0,
    stream: false,
    duration_ms: 100,
    first_token_ms: 50,
    image_count: 0,
    image_size: null,
    image_input_size: null,
    image_output_size: null,
    image_size_source: null,
    image_size_breakdown: null,
    image_input_tokens: 0,
    image_input_cost: 0,
    image_output_tokens: 0,
    image_output_cost: 0,
    user_agent: null,
    cache_ttl_overridden: false,
    created_at: '2026-08-06T20:00:00.000Z',
    account: { id: 13, name: 'deepseek-official' },
    group: { id: 3, name: 'DeepSeek API' } as any,
    ...overrides,
  }
}

describe('actual model route aggregation', () => {
  it('distinguishes the sent model from a mismatched model declared by upstream', () => {
    const [row] = buildModelRouteRows([
      usage({
        upstream_model: 'deepseek-v4-pro',
        upstream_response_model: 'deepseek-v4-flash',
        upstream_model_mismatch: true,
      } as any),
    ], [])

    expect(row).toMatchObject({
      requestedModel: 'deepseek-chat',
      upstreamModel: 'deepseek-v4-pro',
      upstreamResponseModel: 'deepseek-v4-flash',
      upstreamModelMismatch: true,
      modelAuditStatus: 'mismatch',
    })
  })

  it('uses only stored request, upstream, channel and endpoint values', () => {
    const channels = [{ id: 7, name: '官方直连', model_pricing: [] }] as Channel[]
    const rows = buildModelRouteRows([usage()], channels)

    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({
      requestedModel: 'deepseek-chat',
      upstreamModel: 'deepseek-v4-flash',
      provider: 'DeepSeek',
      channelName: '官方直连',
      accountName: 'deepseek-official',
      inboundEndpoint: '/v1/chat/completions',
      upstreamEndpoint: '/chat/completions',
      cacheReadTokens: 128,
      firstSeen: '2026-08-06T20:00:00.000Z',
      lastSeen: '2026-08-06T20:00:00.000Z',
    })
  })

  it('keeps the first and last real call time while aggregating a route', () => {
    const rows = buildModelRouteRows([
      usage({ id: 1, created_at: '2026-08-06T20:00:00.000Z' }),
      usage({ id: 2, created_at: '2026-08-06T20:08:00.000Z' }),
    ], [])

    expect(rows[0]).toMatchObject({
      requests: 2,
      firstSeen: '2026-08-06T20:00:00.000Z',
      lastSeen: '2026-08-06T20:08:00.000Z',
    })
  })

  it('attaches only a matching channel custom price rule', () => {
    const channels = [{
      id: 7,
      name: '中转渠道',
      model_pricing: [
        { platform: 'openai', models: ['gpt-*'], billing_mode: 'token', input_price: 1e-6 },
        { platform: 'openai', models: ['deepseek-v4-*'], billing_mode: 'token', input_price: 1.4e-7 },
      ],
    }] as Channel[]
    const [row] = buildModelRouteRows([usage()], channels)
    expect(row.channelPricing?.input_price).toBe(1.4e-7)
  })

  it('preserves deleted-account history instead of inventing a live account', () => {
    const [row] = buildModelRouteRows([usage({ account_id: null, account: undefined, channel_id: null })], [])
    expect(row.accountName).toBe('已删除账号（历史）')
    expect(row.channelName).toBe('直属账号（无渠道）')
  })
})
