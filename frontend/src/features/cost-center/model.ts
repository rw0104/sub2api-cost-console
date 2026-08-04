import type { Account } from '@/types'
import algorithmVersionSource from '../../../ALGORITHM_VERSION?raw'

export type BillingCycle = 'hourly' | 'daily' | 'weekly' | 'monthly' | 'one_time'
export type CostCurrency = 'CNY' | 'USD'
export type CostSource = 'default' | 'custom'
export type CostPlan = 'free' | 'plus' | 'pro' | 'team' | 'business' | 'k12' | 'unknown'

export interface CostProfile {
  amount: number
  currency: CostCurrency
  billing_cycle: BillingCycle
  started_at: string
  source: CostSource
  algorithm_version: string
}

export type CostAccount = Pick<Account, 'created_at' | 'extra' | 'credentials' | 'parent_plan_type'>
export type DateInput = string | number | Date

export const CNY_PER_USD = 7.2
export const USD_TO_CNY_RATE = CNY_PER_USD
export const COST_ALGORITHM_VERSION = algorithmVersionSource.trim()
export const LEGACY_COST_ALGORITHM_VERSION = 'legacy-unversioned'

export const COST_ALGORITHM_MANIFEST = Object.freeze({
  version: COST_ALGORITHM_VERSION,
  monthly_hours: 730,
  cny_per_usd: CNY_PER_USD,
  accrual: 'linear_elapsed_milliseconds',
  start_boundary: 'account_created_at',
})

export const DEFAULT_MONTHLY_PRICES_CNY: Readonly<Record<CostPlan, number>> = {
  free: 0,
  plus: 140,
  pro: 1400,
  team: 210,
  business: 210,
  k12: 30,
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
    amount: DEFAULT_MONTHLY_PRICES_CNY[plan],
    currency: 'CNY',
    billing_cycle: 'monthly',
    started_at: account.created_at,
    source: 'default',
    algorithm_version: COST_ALGORITHM_VERSION,
  }
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

export function accruedCost(profile: CostProfile, now: DateInput = Date.now()): number {
  if (!Number.isFinite(profile.amount) || profile.amount < 0) return 0

  const startedAtMs = timestamp(profile.started_at)
  const nowMs = timestamp(now)
  if (startedAtMs === null || nowMs === null || nowMs < startedAtMs) return 0
  if (profile.billing_cycle === 'one_time') return profile.amount

  return hourlyRate(profile) * elapsedHours(startedAtMs, nowMs)
}

export function convertCurrency(
  amount: number,
  from: CostCurrency,
  to: CostCurrency,
): number {
  if (!Number.isFinite(amount)) return 0
  if (from === to) return amount
  return from === 'USD' ? amount * CNY_PER_USD : amount / CNY_PER_USD
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
