import { describe, expect, it } from 'vitest'
import type { AdminUsageLog } from '@/types'
import { aggregateUsageWindow, fillCostTrendBuckets, localDateParameter, usageWindowBounds } from '../usageWindow'

function usage(overrides: Partial<AdminUsageLog>): AdminUsageLog {
  return {
    id: 1,
    user_id: 1,
    api_key_id: 1,
    account_id: 1,
    request_id: 'req-1',
    model: 'gpt-5',
    group_id: 1,
    subscription_id: null,
    input_tokens: 10,
    output_tokens: 5,
    cache_creation_tokens: 2,
    cache_read_tokens: 3,
    cache_creation_5m_tokens: 0,
    cache_creation_1h_tokens: 0,
    input_cost: 0.1,
    output_cost: 0.2,
    cache_creation_cost: 0.01,
    cache_read_cost: 0.01,
    total_cost: 0.32,
    actual_cost: 0.4,
    rate_multiplier: 1,
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
    user_agent: 'test',
    cache_ttl_overridden: false,
    created_at: '2026-08-05T12:03:20.000Z',
    ...overrides,
  }
}

describe('official core usage-log compatibility aggregation', () => {
  it('uses local calendar-day bounds for today', () => {
    const end = new Date(2026, 7, 6, 13, 45, 30)
    const bounds = usageWindowBounds('today', end)

    expect(bounds.start).toEqual(new Date(2026, 7, 6, 0, 0, 0, 0))
    expect(bounds.end).toEqual(new Date(2026, 7, 7, 0, 0, 0, 0))
  })

  it('uses exact rolling bounds for one minute and one month', () => {
    const end = new Date('2026-08-06T12:00:00.000Z')

    expect(usageWindowBounds('1m', end).start.toISOString()).toBe('2026-08-06T11:59:00.000Z')
    expect(usageWindowBounds('30d', end).start.toISOString()).toBe('2026-07-07T12:00:00.000Z')
  })

  it('keeps only records inside the requested rolling window', () => {
    const end = new Date('2026-08-05T12:05:00.000Z')
    const { start } = usageWindowBounds('5m', end)
    const result = aggregateUsageWindow([
      usage({ created_at: '2026-08-05T12:03:20.000Z' }),
      usage({ id: 2, created_at: '2026-08-05T11:59:59.000Z' }),
    ], '5m', start, end)

    expect(result).toHaveLength(5)
    expect(result.map((point) => point.requests)).toEqual([0, 0, 0, 1, 0])
    expect(result[3]).toMatchObject({ requests: 1, total_tokens: 20, cost: 0.32, actual_cost: 0.4 })
  })

  it('calculates real account cost from the stored pricing snapshot and multiplier', () => {
    const result = aggregateUsageWindow([
      usage({ account_stats_cost: 0.25, account_rate_multiplier: 1.2 }),
      usage({ id: 2, account_stats_cost: null, total_cost: 0.5, account_rate_multiplier: 2 }),
    ], '30m', new Date('2026-08-05T11:35:00.000Z'), new Date('2026-08-05T12:05:00.000Z'))

    expect(result.reduce((sum, point) => sum + Number(point.account_cost || 0), 0)).toBeCloseTo(1.3)
  })

  it('formats local calendar dates for the upstream date-only API', () => {
    const value = new Date(2026, 7, 5, 23, 59, 0)
    expect(localDateParameter(value)).toBe('2026-08-05')
  })

  it('treats timezone-less dashboard buckets as UTC instead of shifting them by the browser timezone', () => {
    const previousTimezone = process.env.TZ
    process.env.TZ = 'America/Los_Angeles'
    try {
      const result = fillCostTrendBuckets([{
        date: '2026-08-05 12:00',
        requests: 1,
        input_tokens: 10,
        output_tokens: 5,
        cache_creation_tokens: 0,
        cache_read_tokens: 0,
        total_tokens: 15,
        cost: 0.25,
        actual_cost: 0.3,
      }], '5m', new Date('2026-08-05T11:58:00.000Z'), new Date('2026-08-05T12:03:00.000Z'))

      expect(result.map((point) => point.requests)).toEqual([0, 0, 1, 0, 0])
      expect(result[2].actual_cost).toBe(0.3)
    } finally {
      if (previousTimezone == null) delete process.env.TZ
      else process.env.TZ = previousTimezone
    }
  })
})
