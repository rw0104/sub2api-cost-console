import { describe, expect, it } from 'vitest'
import {
  describeCurrentAccountState,
  normalizeSchedulerScore,
  resolveSchedulerBaseScoreMax,
} from '../upstreamTable'

describe('upstream asset table scheduler score', () => {
  it('normalizes the default upstream 4 point score onto a percentage scale', () => {
    expect(normalizeSchedulerScore(3.8, 4)).toBe(95)
    expect(normalizeSchedulerScore(0.8, 4)).toBe(20)
  })

  it('uses the effective scheduler base weights when settings are available', () => {
    expect(resolveSchedulerBaseScoreMax({
      openai_advanced_scheduler_effective_weight_priority: '0.4',
      openai_advanced_scheduler_effective_weight_load: '1.0',
      openai_advanced_scheduler_effective_weight_queue: '1.0',
      openai_advanced_scheduler_effective_weight_error_rate: '0.2',
      openai_advanced_scheduler_effective_weight_ttft: '0.1',
      openai_advanced_scheduler_effective_weight_reset: '0.3',
      openai_advanced_scheduler_effective_weight_quota_headroom: '0.5',
      openai_advanced_scheduler_effective_weight_upstream_cost: '0.0',
    })).toBe(3.5)
  })
})

describe('upstream asset table current account state', () => {
  it('does not invent recovery events for a healthy account', () => {
    expect(describeCurrentAccountState({
      status: 'active',
      error_message: null,
      rate_limited_at: null,
      rate_limit_reset_at: null,
    })).toEqual({ error: 0, limited: 0, note: '当前状态正常' })
  })

  it('shows current errors and rate limits without presenting them as event counts', () => {
    expect(describeCurrentAccountState({
      status: 'error',
      error_message: 'token expired',
      rate_limited_at: '2026-08-05T12:00:00Z',
      rate_limit_reset_at: '2026-08-05T13:00:00Z',
    })).toMatchObject({ error: 1, limited: 1, note: 'token expired' })
  })
})
