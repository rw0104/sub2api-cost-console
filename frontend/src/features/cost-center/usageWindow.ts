import type { AdminUsageLog, TrendDataPoint } from '@/types'
import type { CostCenterRange } from './useCostCenterData'

export interface CostTrendDataPoint extends TrendDataPoint {
  account_cost?: number
}

const RANGE_MILLISECONDS: Record<CostCenterRange, number> = {
  today: 24 * 60 * 60 * 1000,
  '1m': 60 * 1000,
  '5m': 5 * 60 * 1000,
  '30m': 30 * 60 * 1000,
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
  '30d': 30 * 24 * 60 * 60 * 1000,
}

const TREND_BUCKET_MILLISECONDS: Record<CostCenterRange, number> = {
  today: 60 * 60 * 1000,
  '1m': 60 * 1000,
  '5m': 60 * 1000,
  '30m': 60 * 1000,
  '1h': 60 * 1000,
  '6h': 60 * 60 * 1000,
  '24h': 60 * 60 * 1000,
  '7d': 24 * 60 * 60 * 1000,
  '30d': 24 * 60 * 60 * 1000,
}

export function costTrendBucketHours(range: CostCenterRange): number {
  return TREND_BUCKET_MILLISECONDS[range] / (60 * 60 * 1000)
}

export function usageWindowBounds(range: CostCenterRange, end = new Date()): { start: Date; end: Date } {
  if (range === 'today') {
    const start = new Date(end)
    start.setHours(0, 0, 0, 0)
    const calendarEnd = new Date(start)
    calendarEnd.setDate(calendarEnd.getDate() + 1)
    return { start, end: calendarEnd }
  }
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
  if (range === '1m' || range === '5m' || range === '30m' || range === '1h') {
    bucket.setSeconds(0)
  } else if (range === '7d' || range === '30d') {
    bucket.setHours(0, 0, 0, 0)
  } else {
    bucket.setMinutes(0, 0, 0)
  }
  return bucket
}

function parseTrendDate(value: string): Date {
  const utcParts = value.match(/^(\d{4})-(\d{2})-(\d{2})(?:[ T](\d{2}):?(\d{2})?:?(\d{2})?)?$/)
  if (utcParts) {
    // Sub2API's dashboard SQL formats timestamptz buckets without an offset.
    // PostgreSQL stores and aggregates those values in UTC, so interpreting the
    // string as browser-local time shifts every point and can filter the entire
    // series out of a rolling window.
    return new Date(Date.UTC(
      Number(utcParts[1]),
      Number(utcParts[2]) - 1,
      Number(utcParts[3]),
      Number(utcParts[4] ?? 0),
      Number(utcParts[5] ?? 0),
      Number(utcParts[6] ?? 0),
    ))
  }
  return new Date(value)
}

function emptyTrendPoint(date: Date): CostTrendDataPoint {
  return {
    date: date.toISOString(),
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
}

function mergeTrendPoint(target: CostTrendDataPoint, source: CostTrendDataPoint): void {
  target.requests += Number(source.requests || 0)
  target.input_tokens += Number(source.input_tokens || 0)
  target.output_tokens += Number(source.output_tokens || 0)
  target.cache_creation_tokens += Number(source.cache_creation_tokens || 0)
  target.cache_read_tokens += Number(source.cache_read_tokens || 0)
  target.total_tokens += Number(source.total_tokens || 0)
  target.cost += Number(source.cost || 0)
  target.actual_cost += Number(source.actual_cost || 0)
  target.account_cost = Number(target.account_cost || 0) + Number(source.account_cost || 0)
}

export function fillCostTrendBuckets(
  points: CostTrendDataPoint[],
  range: CostCenterRange,
  start: Date,
  end: Date,
): CostTrendDataPoint[] {
  if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || start >= end) return []

  const bucketMilliseconds = TREND_BUCKET_MILLISECONDS[range]
  const firstBucket = bucketStart(start, range)
  const lastBucket = bucketStart(new Date(end.getTime() - 1), range)
  const buckets = new Map<number, CostTrendDataPoint>()

  for (const point of points) {
    const parsed = parseTrendDate(point.date)
    if (!Number.isFinite(parsed.getTime())) continue
    const bucket = bucketStart(parsed, range)
    const key = bucket.getTime()
    if (key < firstBucket.getTime() || key > lastBucket.getTime()) continue
    const merged = buckets.get(key) ?? emptyTrendPoint(bucket)
    mergeTrendPoint(merged, point)
    buckets.set(key, merged)
  }

  const result: CostTrendDataPoint[] = []
  for (let cursor = firstBucket.getTime(); cursor <= lastBucket.getTime(); cursor += bucketMilliseconds) {
    result.push(buckets.get(cursor) ?? emptyTrendPoint(new Date(cursor)))
  }
  return result
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
    if (!Number.isFinite(timestamp) || timestamp < startMs || timestamp >= endMs) continue

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

  return fillCostTrendBuckets([...buckets.values()], range, start, end)
}
