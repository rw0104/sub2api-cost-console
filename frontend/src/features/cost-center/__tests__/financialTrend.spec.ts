import { describe, expect, it } from 'vitest'
import { selectFinancialTrend } from '../financialTrend'

describe('financial trend source selection', () => {
  it('falls back to usage_logs when Ops buckets exist but their economics are empty', () => {
    const result = selectFinancialTrend([
      {
        bucket_start: '2026-08-12T00:00:00Z', request_count: 3,
        success_count: 3, error_count: 0, token_consumed: 100,
        input_tokens: 80, output_tokens: 20, cache_creation_tokens: 0,
        cache_read_tokens: 0, user_billed_usd: 0, account_cost_usd: 0,
        contribution_usd: 0, qps: 0, tps: 0,
      },
    ], [
      {
        date: '2026-08-12T00:00:00Z', requests: 3, input_tokens: 80,
        output_tokens: 20, cache_creation_tokens: 0, cache_read_tokens: 0,
        total_tokens: 100, cost: 1, actual_cost: 2, account_cost: 1,
      },
    ], 1, 1)

    expect(result).toMatchObject([{ source: 'usage_logs', billedUsd: 2, accountCostUsd: 1 }])
  })

  it('keeps Ops economics when they carry factual values', () => {
    const result = selectFinancialTrend([
      {
        bucket_start: '2026-08-12T00:00:00Z', request_count: 1,
        success_count: 1, error_count: 0, token_consumed: 1,
        input_tokens: 1, output_tokens: 0, cache_creation_tokens: 0,
        cache_read_tokens: 0, user_billed_usd: 3, account_cost_usd: 1,
        contribution_usd: 2, qps: 0, tps: 0,
      },
    ], [], 0.5, 1)

    expect(result).toMatchObject([{ source: 'ops', billedUsd: 3, accountCostUsd: 1, bucketHours: 0.5 }])
  })

  it('renders successful empty usage buckets as zero activity', () => {
    const result = selectFinancialTrend([], [{
      date: '2026-08-12T00:00:00Z', requests: 0, input_tokens: 0,
      output_tokens: 0, cache_creation_tokens: 0, cache_read_tokens: 0,
      total_tokens: 0, cost: 0, actual_cost: 0, account_cost: 0,
      observed: false,
    }], 1, 1)

    expect(result[0]).toMatchObject({ source: 'usage_logs', observed: false, billedUsd: 0, accountCostUsd: 0, contributionUsd: 0 })
  })
})
