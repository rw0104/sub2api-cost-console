import { describe, expect, it } from 'vitest'
import {
  aggregateModelStatsFromUsageLogs,
  buildModelCostRows,
  classifyModelProvider,
  summarizeModelAudit,
  summarizeModelCosts,
} from '../modelCostAnalysis'

describe('model cost analysis', () => {
  it('separates official standard cost, upstream account cost, revenue and gross profit', () => {
    const rows = buildModelCostRows([
      {
        model: 'deepseek-v4-pro',
        requests: 2,
        input_tokens: 1_000_000,
        output_tokens: 500_000,
        cache_creation_tokens: 0,
        cache_read_tokens: 250_000,
        total_tokens: 1_750_000,
        cost: 0.87090625,
        account_cost: 0.435453125,
        actual_cost: 1.2,
      },
    ])

    expect(rows[0]).toMatchObject({
      provider: 'DeepSeek',
      standardCost: 0.87090625,
      accountCost: 0.435453125,
      revenue: 1.2,
      grossProfit: 0.764546875,
    })
    expect(rows[0].grossMargin).toBeCloseTo(0.6371223958)
  })

  it('falls back to standard cost for legacy records without an account-cost snapshot', () => {
    const rows = buildModelCostRows([{ model: 'custom-relay-model', cost: 0.2, actual_cost: 0.3 } as any])
    expect(rows[0].accountCost).toBe(0.2)
    expect(rows[0].accountCostEstimated).toBe(true)
  })

  it('summarizes every visible model', () => {
    const rows = buildModelCostRows([
      { model: 'deepseek-v4-flash', cost: 0.14, account_cost: 0.1, actual_cost: 0.2 } as any,
      { model: 'gpt-5.4', cost: 1, account_cost: 0.8, actual_cost: 1.4 } as any,
    ])
    expect(summarizeModelCosts(rows)).toMatchObject({
      standardCost: 1.14,
      accountCost: 0.9,
      revenue: 1.6,
      grossProfit: 0.7,
    })
  })

  it('rebuilds an exact-window model aggregate from real usage logs', () => {
    const logs = [
      {
        model: 'deepseek-chat', upstream_model: 'deepseek-v4-flash',
        input_tokens: 10, output_tokens: 20, cache_creation_tokens: 0, cache_read_tokens: 5,
        total_cost: 0.1, actual_cost: 0.2, account_stats_cost: 0.08, account_rate_multiplier: 1,
        created_at: '2026-08-07T07:01:00.000Z',
      },
      {
        model: 'deepseek-chat', upstream_model: 'deepseek-v4-flash',
        input_tokens: 30, output_tokens: 40, cache_creation_tokens: 0, cache_read_tokens: 10,
        total_cost: 0.3, actual_cost: 0.5, account_stats_cost: 0.24, account_rate_multiplier: 1,
        created_at: '2026-08-07T07:09:00.000Z',
      },
    ] as any

    expect(aggregateModelStatsFromUsageLogs(logs, 'upstream')).toEqual([expect.objectContaining({
      model: 'deepseek-v4-flash',
      requests: 2,
      input_tokens: 40,
      output_tokens: 60,
      cache_read_tokens: 15,
      cost: 0.4,
      actual_cost: 0.7,
      account_cost: 0.32,
      first_seen: '2026-08-07T07:01:00.000Z',
      last_seen: '2026-08-07T07:09:00.000Z',
    })])
  })

  it('groups audited costs by the model declared in the upstream response without repricing the log', () => {
    const logs = [{
      model: 'deepseek-chat',
      upstream_model: 'deepseek-v4-pro',
      upstream_response_model: 'deepseek-v4-flash',
      upstream_model_mismatch: true,
      input_tokens: 100,
      output_tokens: 20,
      cache_creation_tokens: 0,
      cache_read_tokens: 0,
      total_cost: 0.25,
      actual_cost: 0.5,
      account_stats_cost: 0.2,
      created_at: '2026-08-07T08:00:00.000Z',
    }] as any

    expect(aggregateModelStatsFromUsageLogs(logs, 'response')).toEqual([
      expect.objectContaining({
        model: 'deepseek-v4-flash',
        cost: 0.25,
        account_cost: 0.2,
        actual_cost: 0.5,
      }),
    ])
  })

  it('summarizes matched, mismatched and unobserved upstream model declarations without losing cost snapshots', () => {
    const logs = [
      {
        upstream_model_mismatch: false,
        total_cost: 0.1,
        account_stats_cost: 0.08,
        actual_cost: 0.2,
      },
      {
        upstream_model_mismatch: true,
        total_cost: 0.3,
        account_stats_cost: 0.24,
        actual_cost: 0.5,
      },
      {
        upstream_model_mismatch: null,
        total_cost: 0.4,
        account_stats_cost: 0.32,
        actual_cost: 0.6,
      },
    ] as any

    expect(summarizeModelAudit(logs)).toEqual({
      totalRequests: 3,
      observedRequests: 2,
      matchedRequests: 1,
      mismatchRequests: 1,
      unobservedRequests: 1,
      mismatchRate: 0.5,
      mismatchStandardCost: 0.3,
      mismatchAccountCost: 0.24,
      mismatchRevenue: 0.5,
    })
  })

  it.each([
    ['deepseek-v4-pro', 'DeepSeek'],
    ['gpt-5.4', 'OpenAI'],
    ['claude-sonnet-4-5', 'Anthropic'],
    ['gemini-3-pro', 'Google'],
    ['grok-4', 'xAI'],
    ['mistral-large-latest', 'Mistral'],
    ['meta-llama/llama-4-maverick', 'Meta / Llama'],
    ['qwen3.5-plus', 'Alibaba / Qwen'],
    ['glm-5', 'Z.ai / GLM'],
    ['kimi-k2.5', 'Moonshot / Kimi'],
    ['minimax-m2.1', 'MiniMax'],
    ['command-r-plus', 'Cohere'],
    ['sonar-pro', 'Perplexity'],
    ['relay/private-model', '其他/中转'],
  ])('classifies %s as %s', (model, provider) => {
    expect(classifyModelProvider(model)).toBe(provider)
  })
})
