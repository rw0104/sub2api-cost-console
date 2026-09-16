import { afterEach, describe, expect, it, vi } from 'vitest'
import { economicCostSnapshot, procurementCostInWindow, resolveAccountBillingMode, resolveCostProfile, type CostAccount } from '../model'
import { buildCostCenterSnapshotQuery, buildEconomicsWindowQuery, economicsWindowHours } from '../useCostCenterData'
import { accruedWindowBounds } from '../usageWindow'

vi.mock('@/api/admin', () => ({ adminAPI: { accounts: {}, dashboard: {}, ops: {} } }))

const originalTZ = process.env.TZ
const originalTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllEnvs()
  // On Windows, deleting TZ alone can retain the previous native timezone.
  process.env.TZ = originalTZ || originalTimezone
  if (originalTZ === undefined) delete process.env.TZ
})

// Accounting invariants are part of the regular regression suite.
describe('cost accounting regressions', () => {
  it.each([
    ['spring-forward', '2026-03-08T12:00:00-04:00', 11],
    ['fall-back', '2026-11-01T12:00:00-05:00', 13],
    ['midnight', '2026-09-16T00:00:00-04:00', 0],
  ] as const)('uses actual elapsed calendar hours at %s', (_name, timestamp, hours) => {
    vi.stubEnv('TZ', 'America/New_York')
    const now = new Date(timestamp)
    const query = buildEconomicsWindowQuery('today', now)
    expect(query.window_hours).toBe(hours)
    expect(query.timezone).toBe('America/New_York')
    expect(query.end_time).toBe(now.toISOString())
    expect(query.start_time).toBe(buildCostCenterSnapshotQuery('today', now).start_time)
  })

  it.each(['apikey', 'upstream', 'bedrock', 'service_account'] as const)('keeps explicit fixed overhead for %s without adding a default subscription', type => {
    const account: CostAccount = {
      platform: 'openai', type, created_at: '2026-09-01T00:00:00Z', credentials: {}, parent_plan_type: undefined,
      extra: { plan_type: 'pro' },
    }
    expect(resolveCostProfile(account).amount).toBe(0)
    account.extra = { ...account.extra, cost_profile: { amount: 24, currency: 'CNY', billing_cycle: 'daily', started_at: account.created_at } }
    const snapshot = economicCostSnapshot(resolveCostProfile(account), null, '2026-09-01T12:00:00Z')
    expect(snapshot.procurementCost).toBe(12)
    expect(snapshot.hourlyRate).toBe(1)
  })
  it('uses the same calendar-day start for usage and asset economics', () => {
    const now = new Date(2026, 8, 16, 12, 0, 0)
    vi.useFakeTimers()
    vi.setSystemTime(now)
    const usageStart = buildCostCenterSnapshotQuery('today').start_time
    const economicsStart = new Date(now.getTime() - economicsWindowHours('today') * 3_600_000).toISOString()
    expect(economicsStart).toBe(usageStart)
  })

  it('does not count the remaining future hours as already accrued today', () => {
    const now = new Date(2026, 8, 16, 12, 0, 0)
    const bounds = accruedWindowBounds('today', now)
    const profile = {
      amount: 24, currency: 'CNY' as const, billing_cycle: 'daily' as const,
      started_at: new Date(2026, 8, 1).toISOString(), source: 'custom' as const, algorithm_version: '1.6.0',
    }
    const spent = procurementCostInWindow(profile, bounds.start, bounds.end)
    expect(spent).toBe(12)
  })

  it('does not invent fixed procurement fees for a metered API key', () => {
    const account: CostAccount = {
      platform: 'openai', type: 'apikey', created_at: '2026-09-01T00:00:00Z',
      extra: { plan_type: 'pro' }, credentials: {}, parent_plan_type: undefined,
    }
    expect(resolveAccountBillingMode(account)).toBe('metered')
    const profile = resolveCostProfile(account)
    const result = economicCostSnapshot(profile, null, Date.parse(account.created_at) + 730 * 3_600_000)
    expect(result.procurementCost).toBe(0)
  })

  it('does not charge a running hourly fee before the configured start', () => {
    const account: CostAccount = {
      platform: 'openai', type: 'oauth', created_at: '2026-09-01T00:00:00Z', credentials: {},
      extra: { cost_profile: { amount: 24, currency: 'CNY', billing_cycle: 'daily', started_at: '2026-09-17T00:00:00Z' } },
      parent_plan_type: undefined,
    }
    const result = economicCostSnapshot(resolveCostProfile(account), null, '2026-09-16T00:00:00Z')
    expect(result.procurementCost).toBe(0)
    expect(result.hourlyRate).toBe(0)
  })
})
