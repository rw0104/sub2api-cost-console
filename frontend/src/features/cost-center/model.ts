import type { Account } from '@/types'
import algorithmVersionSource from '../../../ALGORITHM_VERSION?raw'

export type BillingCycle = 'hourly' | 'daily' | 'weekly' | 'monthly' | 'one_time'
export type CostCurrency = 'CNY' | 'USD'
export type CostSource = 'default' | 'custom'
export type CostPlan = 'free' | 'plus' | 'pro' | 'team' | 'business' | 'k12' | 'unknown'
export type AccountBillingMode = 'subscription' | 'metered'

export interface CostProfile {
  amount: number
  currency: CostCurrency
  billing_cycle: BillingCycle
  started_at: string
  source: CostSource
  algorithm_version: string
}

export type CostAccount = Pick<Account, 'created_at' | 'extra' | 'credentials' | 'parent_plan_type' | 'platform' | 'type'>
export type DateInput = string | number | Date

// Used only when the daily reference-rate request and its local cache are both unavailable.
export const CNY_PER_USD = 7.2
export const USD_TO_CNY_RATE = CNY_PER_USD
export const COST_ALGORITHM_VERSION = algorithmVersionSource.trim()
export const LEGACY_COST_ALGORITHM_VERSION = 'legacy-unversioned'

export const COST_ALGORITHM_MANIFEST = Object.freeze({
  version: COST_ALGORITHM_VERSION,
  monthly_hours: 730,
  cny_per_usd: CNY_PER_USD,
  default_market: 'US',
  default_price_currency: 'USD',
  default_price_checked_at: '2026-08-05',
  accrual: 'linear_elapsed_milliseconds',
  start_boundary: 'account_created_at',
  terminal_loss: 'confirmed_terminal_event_stops_accrual_and_recognizes_unamortized_prepaid_balance',
  account_billing_modes: Object.freeze({
    subscription: Object.freeze(['oauth', 'setup-token']),
    metered: Object.freeze(['apikey', 'upstream', 'bedrock', 'service_account']),
  }),
  metered_cost_basis: 'usage_tokens_x_model_or_channel_price_x_account_multiplier',
})

export const DEFAULT_MONTHLY_PRICES_USD: Readonly<Record<CostPlan, number>> = {
  free: 0,
  plus: 20,
  pro: 100,
  team: 25,
  business: 25,
  k12: 0,
  unknown: 0,
}

export const HOURS_PER_BILLING_CYCLE: Readonly<Record<Exclude<BillingCycle, 'one_time'>, number>> = {
  hourly: 1,
  daily: 24,
  weekly: 168,
  monthly: 730,
}

const MILLISECONDS_PER_HOUR = 60 * 60 * 1000
const BILLING_CYCLES: readonly BillingCycle[] = ['hourly', 'daily', 'weekly', 'monthly', 'one_time']
const KNOWN_PLANS: readonly Exclude<CostPlan, 'unknown'>[] = [
  'free',
  'plus',
  'pro',
  'team',
  'business',
  'k12',
]

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function timestamp(value: DateInput): number | null {
  const result = value instanceof Date ? value.getTime() : new Date(value).getTime()
  return Number.isFinite(result) ? result : null
}

function stringField(record: Record<string, unknown> | undefined, key: string): unknown {
  return record?.[key]
}

export function normalizeCurrency(value: unknown): CostCurrency | null {
  if (typeof value !== 'string') return null
  const normalized = value.trim().toUpperCase()
  return normalized === 'CNY' || normalized === 'USD' ? normalized : null
}

export function normalizeBillingCycle(value: unknown): BillingCycle | null {
  if (typeof value !== 'string') return null
  const normalized = value.trim().toLowerCase().replace(/[\s-]+/g, '_')
  return BILLING_CYCLES.includes(normalized as BillingCycle) ? normalized as BillingCycle : null
}

export function normalizePlan(value: unknown): CostPlan {
  if (typeof value !== 'string') return 'unknown'

  const normalized = value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')

  if (!normalized) return 'unknown'
  if (normalized === 'k_12' || normalized === 'education' || normalized === 'edu') return 'k12'

  const compact = normalized.replace(/_/g, '')
  for (const plan of KNOWN_PLANS) {
    if (
      normalized === plan ||
      compact === plan ||
      normalized === `chatgpt_${plan}` ||
      normalized === `${plan}_plan` ||
      normalized.split('_').includes(plan)
    ) {
      return plan
    }
  }

  return 'unknown'
}

export function inferPlan(account: CostAccount): CostPlan {
  const extra = isRecord(account.extra) ? account.extra : undefined
  const credentials = isRecord(account.credentials) ? account.credentials : undefined
  const candidates: unknown[] = [
    stringField(extra, 'plan_type'),
    stringField(extra, 'subscription_tier'),
    stringField(credentials, 'plan_type'),
    stringField(credentials, 'subscription_tier'),
    stringField(credentials, 'plan'),
    stringField(credentials, 'subscription_plan'),
    stringField(credentials, 'tier'),
    account.parent_plan_type,
  ]

  for (const candidate of candidates) {
    const plan = normalizePlan(candidate)
    if (plan !== 'unknown') return plan
  }

  return 'unknown'
}

/**
 * OAuth/setup-token accounts are acquired as quota-bearing subscriptions and
 * can have a fixed procurement profile. API keys and upstream credentials are
 * pay-as-you-go: their actual upstream cost is calculated from usage_logs,
 * model/channel pricing and the account multiplier. A custom cost profile on a
 * metered account is therefore optional fixed overhead, never its token price.
 */
export function resolveAccountBillingMode(account: Pick<CostAccount, 'type'>): AccountBillingMode {
  return account.type === 'oauth' || account.type === 'setup-token' ? 'subscription' : 'metered'
}

function customCostProfile(account: CostAccount): CostProfile | null {
  const extra = isRecord(account.extra) ? account.extra : undefined
  const rawProfile = extra?.cost_profile
  if (!isRecord(rawProfile)) return null

  const amount = rawProfile.amount
  const currency = normalizeCurrency(rawProfile.currency)
  const billingCycle = normalizeBillingCycle(rawProfile.billing_cycle)
  const requestedStart = typeof rawProfile.started_at === 'string' ? rawProfile.started_at.trim() : ''
  const requestedStartMs = requestedStart ? timestamp(requestedStart) : null
  const algorithmVersion = typeof rawProfile.algorithm_version === 'string' && rawProfile.algorithm_version.trim()
    ? rawProfile.algorithm_version.trim()
    : LEGACY_COST_ALGORITHM_VERSION

  if (
    typeof amount !== 'number' ||
    !Number.isFinite(amount) ||
    amount < 0 ||
    !currency ||
    !billingCycle ||
    requestedStartMs === null
  ) {
    return null
  }

  const joinedAtMs = timestamp(account.created_at)
  const startedAt = joinedAtMs !== null && requestedStartMs < joinedAtMs
    ? account.created_at
    : requestedStart

  return {
    amount,
    currency,
    billing_cycle: billingCycle,
    started_at: startedAt,
    source: 'custom',
    algorithm_version: algorithmVersion,
  }
}

export function resolveCostProfile(account: CostAccount): CostProfile {
  const custom = customCostProfile(account)
  if (custom) return custom

  const plan = inferPlan(account)
  return {
    amount: DEFAULT_MONTHLY_PRICES_USD[plan],
    currency: 'USD',
    billing_cycle: 'monthly',
    started_at: account.created_at,
    source: 'default',
    algorithm_version: COST_ALGORITHM_VERSION,
  }
}

export function isDefaultSubscriptionCostProfile(account: CostAccount): boolean {
  return resolveAccountBillingMode(account) === 'subscription' && resolveCostProfile(account).source === 'default'
}

export function billingCycleHours(cycle: BillingCycle): number {
  return cycle === 'one_time' ? 0 : HOURS_PER_BILLING_CYCLE[cycle]
}

export function hourlyRate(profile: Pick<CostProfile, 'amount' | 'billing_cycle'>): number {
  if (!Number.isFinite(profile.amount) || profile.amount < 0 || profile.billing_cycle === 'one_time') {
    return 0
  }
  return profile.amount / HOURS_PER_BILLING_CYCLE[profile.billing_cycle]
}

export function elapsedHours(startedAt: DateInput, now: DateInput = Date.now()): number {
  const startedAtMs = timestamp(startedAt)
  const nowMs = timestamp(now)
  if (startedAtMs === null || nowMs === null || nowMs <= startedAtMs) return 0
  return (nowMs - startedAtMs) / MILLISECONDS_PER_HOUR
}

export function isStartedInLocalMonth(startedAt: DateInput, now: DateInput = Date.now()): boolean {
  const startedAtMs = timestamp(startedAt)
  const nowMs = timestamp(now)
  if (startedAtMs === null || nowMs === null || startedAtMs > nowMs) return false
  const started = new Date(startedAtMs)
  const current = new Date(nowMs)
  return started.getFullYear() === current.getFullYear() && started.getMonth() === current.getMonth()
}

export function isTimestampInWindow(value: DateInput, start: DateInput, end: DateInput): boolean {
  const valueMs = timestamp(value)
  const startMs = timestamp(start)
  const endMs = timestamp(end)
  return valueMs !== null && startMs !== null && endMs !== null && valueMs >= startMs && valueMs <= endMs
}

export function procurementCostInWindow(
  profile: CostProfile,
  start: DateInput,
  end: DateInput,
  stoppedAt?: DateInput | null,
): number {
  if (!Number.isFinite(profile.amount) || profile.amount < 0) return 0
  const profileStartMs = timestamp(profile.started_at)
  const windowStartMs = timestamp(start)
  const windowEndMs = timestamp(end)
  if (profileStartMs === null || windowStartMs === null || windowEndMs === null || windowEndMs < windowStartMs) return 0
  const stoppedAtMs = stoppedAt == null ? null : timestamp(stoppedAt)
  const effectiveEndMs = stoppedAtMs === null ? windowEndMs : Math.min(windowEndMs, stoppedAtMs)
  if (profile.billing_cycle === 'one_time') {
    return profileStartMs >= windowStartMs && profileStartMs <= effectiveEndMs ? profile.amount : 0
  }
  const effectiveStartMs = Math.max(windowStartMs, profileStartMs)
  if (effectiveEndMs <= effectiveStartMs) return 0
  return hourlyRate(profile) * ((effectiveEndMs - effectiveStartMs) / MILLISECONDS_PER_HOUR)
}

export function accruedCost(profile: CostProfile, now: DateInput = Date.now()): number {
  if (!Number.isFinite(profile.amount) || profile.amount < 0) return 0

  const startedAtMs = timestamp(profile.started_at)
  const nowMs = timestamp(now)
  if (startedAtMs === null || nowMs === null || nowMs < startedAtMs) return 0
  if (profile.billing_cycle === 'one_time') return profile.amount

  return hourlyRate(profile) * elapsedHours(startedAtMs, nowMs)
}

export interface CostLossStateLike {
  occurred_at: string
  accrued_cost: number
  net_loss: number
  recognized_cost: number
  active: boolean
}

export interface EconomicCostSnapshot {
  procurementCost: number
  impairmentLoss: number
  economicCost: number
  hourlyRate: number
  accrualEndedAt: string | null
}

export function economicCostSnapshot(
  profile: CostProfile,
  loss: CostLossStateLike | null | undefined,
  now: DateInput = Date.now(),
): EconomicCostSnapshot {
  if (loss?.active) {
    const procurementCost = Number.isFinite(loss.accrued_cost) ? Math.max(0, loss.accrued_cost) : 0
    const impairmentLoss = Number.isFinite(loss.net_loss) ? Math.max(0, loss.net_loss) : 0
    return {
      procurementCost,
      impairmentLoss,
      economicCost: procurementCost + impairmentLoss,
      hourlyRate: 0,
      accrualEndedAt: loss.occurred_at || null,
    }
  }
  const procurementCost = accruedCost(profile, now)
  return {
    procurementCost,
    impairmentLoss: 0,
    economicCost: procurementCost,
    hourlyRate: hourlyRate(profile),
    accrualEndedAt: null,
  }
}

export function convertCurrency(
  amount: number,
  from: CostCurrency,
  to: CostCurrency,
  cnyPerUsd = CNY_PER_USD,
): number {
  if (!Number.isFinite(amount)) return 0
  if (from === to) return amount
  const rate = Number.isFinite(cnyPerUsd) && cnyPerUsd > 0 ? cnyPerUsd : CNY_PER_USD
  return from === 'USD' ? amount * rate : amount / rate
}

export function currencySymbol(currency: CostCurrency): '$' | '\u00a5' {
  return currency === 'USD' ? '$' : '\u00a5'
}

export function roundCost(amount: number, fractionDigits = 2): number {
  if (!Number.isFinite(amount)) return 0
  const digits = Math.max(0, Math.min(8, Math.trunc(fractionDigits)))
  const factor = 10 ** digits
  return Math.round((amount + Number.EPSILON) * factor) / factor
}

export function actualUserCost(stats: { user_cost?: number; standard_cost?: number }): number {
  if (typeof stats.user_cost === 'number' && Number.isFinite(stats.user_cost)) return stats.user_cost
  if (typeof stats.standard_cost === 'number' && Number.isFinite(stats.standard_cost)) return stats.standard_cost
  return 0
}

export function formatMoney(
  amount: number,
  currency: CostCurrency,
  fractionDigits = 2,
): string {
  const digits = Math.max(0, Math.min(8, Math.trunc(fractionDigits)))
  const safeAmount = Number.isFinite(amount) ? amount : 0
  const formatted = new Intl.NumberFormat('en-US', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  }).format(safeAmount)
  return `${currencySymbol(currency)}${formatted}`
}
