import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { COST_ALGORITHM_VERSION, convertCurrency, economicCostSnapshot, resolveCostProfile, type CostAccount } from '../model'

const cases = JSON.parse(readFileSync(resolve(process.cwd(), '../backend/internal/service/testdata/cost_accounting_parity.json'), 'utf8')) as Array<{
  name: string; type: CostAccount['type']; plan?: string; parent_account_id?: number; created_at: string; now: string
  profile?: Record<string, unknown>; expected_currency: string; expected_cost: number; expected_hourly: number
}>

describe('shared frontend/backend cost accounting cases', () => {
  it.each(cases)('$name', tc => {
    const account: CostAccount = {
      type: tc.type, platform: 'openai', created_at: tc.created_at, credentials: {},
      // Shadow rows carry the parent's plan for display; it must not become a second subscription.
      parent_plan_type: tc.parent_account_id ? tc.plan : undefined,
      parent_account_id: tc.parent_account_id ?? null,
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

  // The desktop shows ALGORITHM_VERSION while the core reports CostAlgorithmVersion.
  // Both are edited by hand, so keep them from drifting apart.
  it('keeps the frontend algorithm version equal to the backend constant', () => {
    const source = readFileSync(resolve(process.cwd(), '../backend/internal/service/cost_algorithm_version.go'), 'utf8')
    const backendVersion = /const CostAlgorithmVersion = "([^"]+)"/.exec(source)?.[1]
    expect(backendVersion).toBeDefined()
    expect(COST_ALGORITHM_VERSION).toBe(backendVersion)
  })
})
