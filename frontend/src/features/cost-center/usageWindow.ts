import type { AdminUsageLog, TrendDataPoint } from '@/types'
import type { CostCenterRange } from './useCostCenterData'

export interface CostTrendDataPoint extends TrendDataPoint {
  account_cost?: number
}

const RANGE_MILLISECONDS: Record<CostCenterRange, number> = {
  '5m': 5 * 60 * 1000,
  '30m': 30 * 60 * 1000,
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
}

export function usageWindowBounds(range: CostCenterRange, end = new Date()): { start: Date; end: Date } {
  return {
    start: new Date(end.getTime() - RANGE_MILLISECONDS[range]),
    end,
  }
}

export function localDateParameter(value: Date): string {
  const year = value.getFullYear()
  const month = String(value.getMonth() + 1).padStart(2, '0')
  const day = String(value.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function bucketStart(value: Date, range: CostCenterRange): Date {
  const bucket = new Date(value)
  bucket.setMilliseconds(0)
  if (range === '5m' || range === '30m') {
    bucket.setSeconds(0)
  } else if (range === '7d') {
    bucket.setHours(0, 0, 0, 0)
  } else {
    bucket.setMinutes(0, 0, 0)
  }
  return bucket
}

function accountCost(log: AdminUsageLog): number {
  const base = log.account_stats_cost == null ? Number(log.total_cost || 0) : Number(log.account_stats_cost)
  return base * Number(log.account_rate_multiplier ?? 1)
}

export function aggregateUsageWindow(
  logs: AdminUsageLog[],
  range: CostCenterRange,
  start: Date,
  end: Date,
): CostTrendDataPoint[] {
  const startMs = start.getTime()
  const endMs = end.getTime()
  const buckets = new Map<number, CostTrendDataPoint>()

  for (const log of logs) {
    const createdAt = new Date(log.created_at)
    const timestamp = createdAt.getTime()
    if (!Number.isFinite(timestamp) || timestamp < startMs || timestamp > endMs) continue

    const bucket = bucketStart(createdAt, range)
    const key = bucket.getTime()
    const point = buckets.get(key) ?? {
      date: bucket.toISOString(),
      requests: 0,
      input_tokens: 0,
      output_tokens: 0,
      cache_creation_tokens: 0,
      cache_read_tokens: 0,
      total_tokens: 0,
      cost: 0,
      actual_cost: 0,
      account_cost: 0,
    }

    point.requests += 1
    point.input_tokens += Number(log.input_tokens || 0)
    point.output_tokens += Number(log.output_tokens || 0)
    point.cache_creation_tokens += Number(log.cache_creation_tokens || 0)
    point.cache_read_tokens += Number(log.cache_read_tokens || 0)
    point.total_tokens += Number(log.input_tokens || 0)
      + Number(log.output_tokens || 0)
      + Number(log.cache_creation_tokens || 0)
      + Number(log.cache_read_tokens || 0)
    point.cost += Number(log.total_cost || 0)
    point.actual_cost += Number(log.actual_cost || 0)
    point.account_cost = Number(point.account_cost || 0) + accountCost(log)
    buckets.set(key, point)
  }

  return [...buckets.values()].sort((left, right) => new Date(left.date).getTime() - new Date(right.date).getTime())
}
