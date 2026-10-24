import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { convertCurrency, economicCostSnapshot, resolveCostProfile, type CostAccount } from '../model'

const cases = JSON.parse(readFileSync(resolve(process.cwd(), '../backend/internal/service/testdata/cost_accounting_parity.json'), 'utf8')) as Array<{
  name: string; type: CostAccount['type']; plan?: string; created_at: string; now: string
  profile?: Record<string, unknown>; expected_currency: string; expected_cost: number; expected_hourly: number
}>

describe('shared frontend/backend cost accounting cases', () => {
  it.each(cases)('$name', tc => {
    const account: CostAccount = {
      type: tc.type, platform: 'openai', created_at: tc.created_at, credentials: {}, parent_plan_type: undefined,
      extra: { plan_type: tc.plan, ...(tc.profile ? { cost_profile: tc.profile } : {}) },
    }
    const profile = resolveCostProfile(account)
    const result = economicCostSnapshot(profile, null, tc.now)
    expect(profile.currency).toBe(tc.expected_currency)
    expect(result.procurementCost).toBeCloseTo(tc.expected_cost, 8)
    expect(result.hourlyRate).toBeCloseTo(tc.expected_hourly, 8)
    const factor = tc.expected_currency === 'USD' ? 7 : 1
    expect(convertCurrency(result.procurementCost, profile.currency, 'CNY', 7)).toBeCloseTo(tc.expected_cost * factor, 8)
    expect(convertCurrency(result.hourlyRate, profile.currency, 'CNY', 7)).toBeCloseTo(tc.expected_hourly * factor, 8)
  })
})
