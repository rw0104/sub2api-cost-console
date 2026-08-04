import { describe, expect, it } from 'vitest'
import {
  accruedCost,
  convertCurrency,
  elapsedHours,
  formatMoney,
  hourlyRate,
  inferPlan,
  resolveCostProfile,
  type CostAccount,
  type CostProfile,
} from '../model'

const JOINED_AT = '2026-08-01T00:00:00.000Z'

function account(overrides: Partial<CostAccount> = {}): CostAccount {
  return {
    created_at: JOINED_AT,
    extra: {},
    credentials: {},
    parent_plan_type: undefined,
    ...overrides,
  }
}

function profile(overrides: Partial<CostProfile> = {}): CostProfile {
  return {
    amount: 140,
    currency: 'CNY',
    billing_cycle: 'monthly',
    started_at: JOINED_AT,
    source: 'default',
    ...overrides,
  }
}

describe('cost center model', () => {
  it('starts recurring cost at zero when the account joins', () => {
    const resolved = resolveCostProfile(account({ extra: { plan_type: 'plus' } }))
    expect(accruedCost(resolved, JOINED_AT)).toBe(0)
  })

  it('accrues a monthly price linearly over actual elapsed milliseconds', () => {
    const resolved = resolveCostProfile(account({ extra: { subscription_tier: 'plus' } }))
    const afterOneDay = '2026-08-02T00:00:00.000Z'

    expect(resolved.amount).toBe(140)
    expect(elapsedHours(resolved.started_at, afterOneDay)).toBe(24)
    expect(hourlyRate(resolved)).toBeCloseTo(140 / 730)
    expect(accruedCost(resolved, afterOneDay)).toBeCloseTo((140 * 24) / 730)
  })

  it('does not accrue recurring or one-time cost before the start', () => {
    const future = '2026-08-05T00:00:00.000Z'
    expect(accruedCost(profile({ started_at: future }), JOINED_AT)).toBe(0)
    expect(accruedCost(profile({ billing_cycle: 'one_time', started_at: future }), JOINED_AT)).toBe(0)
  })

  it('counts a one-time cost in full at and after its start', () => {
    const oneTime = profile({ amount: 50, billing_cycle: 'one_time', source: 'custom' })
    expect(hourlyRate(oneTime)).toBe(0)
    expect(accruedCost(oneTime, JOINED_AT)).toBe(50)
    expect(accruedCost(oneTime, '2026-08-02T00:00:00.000Z')).toBe(50)
  })

  it('clamps a custom start that predates account creation', () => {
    const resolved = resolveCostProfile(account({
      extra: {
        cost_profile: {
          amount: 12,
          currency: 'USD',
          billing_cycle: 'daily',
          started_at: '2026-07-01T00:00:00.000Z',
        },
      },
    }))

    expect(resolved.started_at).toBe(JOINED_AT)
    expect(accruedCost(resolved, JOINED_AT)).toBe(0)
  })

  it('uses a valid custom profile instead of inferred plan pricing', () => {
    const resolved = resolveCostProfile(account({
      extra: {
        plan_type: 'pro',
        cost_profile: {
          amount: 9,
          currency: 'USD',
          billing_cycle: 'weekly',
          started_at: JOINED_AT,
          source: 'default',
        },
      },
    }))

    expect(resolved).toMatchObject({
      amount: 9,
      currency: 'USD',
      billing_cycle: 'weekly',
      source: 'custom',
    })
  })

  it('falls back through credential and parent plan fields', () => {
    expect(inferPlan(account({ credentials: { subscription_tier: 'ChatGPT Pro' } }))).toBe('pro')
    expect(inferPlan(account({ parent_plan_type: 'business' }))).toBe('business')
    expect(resolveCostProfile(account({ credentials: { plan_type: 'team' } })).amount).toBe(210)
    expect(resolveCostProfile(account({ parent_plan_type: 'k-12' })).amount).toBe(30)
  })

  it('converts currencies using the fixed 7.2 CNY per USD rate', () => {
    expect(convertCurrency(10, 'USD', 'CNY')).toBe(72)
    expect(convertCurrency(72, 'CNY', 'USD')).toBe(10)
    expect(convertCurrency(10, 'USD', 'USD')).toBe(10)
    expect(formatMoney(72, 'CNY')).toBe('\u00a572.00')
  })
})
