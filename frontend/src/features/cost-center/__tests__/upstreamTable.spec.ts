import { describe, expect, it } from 'vitest'
import {
  describeCurrentAccountState,
  hasAccountRankingEvidence,
  normalizeSchedulerScore,
  resolveSchedulerBaseScoreMax,
} from '../upstreamTable'

describe('upstream account ranking evidence', () => {
  it('does not rank an idle untested account from its scheduler score alone', () => {
    expect(hasAccountRankingEvidence('score', {
      today: { requests: 0, cost: 0, user_cost: 0 },
      state: 'normal',
    })).toBe(false)
  })

  it('admits factual traffic, a completed probe, or a current failure state', () => {
    expect(hasAccountRankingEvidence('score', {
      today: { requests: 3, cost: 0.2, user_cost: 0.4 },
      state: 'normal',
    })).toBe(true)
    expect(hasAccountRankingEvidence('reliability', {
      today: { requests: 0 },
      probe: { loading: false, success: true, latency_ms: 80 },
      state: 'normal',
    })).toBe(true)
    expect(hasAccountRankingEvidence('score', {
      today: { requests: 0 },
      state: 'limited',
    })).toBe(true)
  })
})

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
    })).toMatchObject({ error: 0, limited: 0, state: 'normal', note: '当前状态正常' })
  })

  it('shows current errors and rate limits without presenting them as event counts', () => {
    expect(describeCurrentAccountState({
      status: 'error',
      error_message: 'token expired',
      rate_limited_at: '2026-08-05T12:00:00Z',
      rate_limit_reset_at: '2026-08-05T13:00:00Z',
    }, undefined, new Date('2026-08-05T12:30:00Z'))).toMatchObject({ error: 1, limited: 1, note: 'token expired' })
  })

  it('counts only live cooldowns and includes temporary and model-level scheduler blocks', () => {
    const now = new Date('2026-08-05T12:00:00Z')

    expect(describeCurrentAccountState({
      status: 'active',
      schedulable: true,
      rate_limited_at: '2026-08-05T10:00:00Z',
      rate_limit_reset_at: '2026-08-05T11:00:00Z',
    }, undefined, now)).toMatchObject({ state: 'normal', limited: 0 })

    expect(describeCurrentAccountState({
      status: 'active',
      schedulable: true,
      temp_unschedulable_until: '2026-08-05T12:30:00Z',
      temp_unschedulable_reason: 'upstream 402 cooldown',
    }, undefined, now)).toMatchObject({ state: 'limited', limited: 1, note: 'upstream 402 cooldown' })

    expect(describeCurrentAccountState({
      status: 'active',
      schedulable: true,
      extra: {
        model_rate_limits: {
          'gpt-5': { rate_limited_at: '2026-08-05T11:59:00Z', rate_limit_reset_at: '2026-08-05T13:00:00Z' },
        },
      },
    }, undefined, now)).toMatchObject({ state: 'limited', limited: 1 })
  })

  it('surfaces a failed live probe immediately instead of leaving the account green', () => {
    expect(describeCurrentAccountState({
      status: 'active',
      schedulable: true,
    }, {
      loading: false,
      success: false,
      message: 'API returned 429: usage_limit_reached',
    }, new Date('2026-08-05T12:00:00Z'))).toMatchObject({
      state: 'limited',
      limited: 1,
      note: 'API returned 429: usage_limit_reached',
    })
  })
})
