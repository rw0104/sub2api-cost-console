import { describe, expect, it } from 'vitest'
import { buildModelCostRows, classifyModelProvider, summarizeModelCosts } from '../modelCostAnalysis'

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
