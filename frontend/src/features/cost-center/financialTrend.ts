import type { OpsThroughputTrendPoint } from '@/api/admin/ops'
import type { CostTrendDataPoint } from './usageWindow'

export interface FinancialTrendPoint {
  timestamp: string
  billedUsd: number | null
  accountCostUsd: number | null
  contributionUsd: number | null
  bucketHours: number
  source: 'ops' | 'usage_logs'
  observed?: boolean
}

function finite(value: unknown): number | null {
  if (value == null || value === '') return null
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : null
}

function absoluteFinancialEvidence(values: Array<number | null>): number {
  return values.reduce<number>((sum, value) => sum + Math.abs(value ?? 0), 0)
}

// Some compatible cores expose Ops request buckets before they expose the
// economic columns. Prefer Ops only when those columns carry evidence, or when
// usage_logs also proves that the selected window is financially zero.
export function selectFinancialTrend(
  ops: OpsThroughputTrendPoint[],
  usage: CostTrendDataPoint[],
  opsBucketHours: number,
  usageBucketHours: number,
): FinancialTrendPoint[] {
  const usageBilled = usage.map((point) => finite(point.actual_cost))
  const usageAccountCost = usage.map((point) => finite(point.account_cost ?? point.cost))
  const usageEvidence = absoluteFinancialEvidence([...usageBilled, ...usageAccountCost])

  const opsColumnsPresent = ops.some((point) => (
    finite(point.user_billed_usd) != null || finite(point.account_cost_usd) != null
  ))
  const opsBilled = ops.map((point) => finite(point.user_billed_usd))
  const opsAccountCost = ops.map((point) => finite(point.account_cost_usd))
  const opsEvidence = absoluteFinancialEvidence([...opsBilled, ...opsAccountCost])
  const useOps = ops.length > 0 && opsColumnsPresent && (opsEvidence > 0 || usageEvidence === 0)

  if (useOps) {
    return ops.map((point, index) => {
      const billedUsd = opsBilled[index]
      const accountCostUsd = opsAccountCost[index]
      return {
        timestamp: point.bucket_start,
        billedUsd,
        accountCostUsd,
        contributionUsd: finite(point.contribution_usd)
          ?? (billedUsd != null && accountCostUsd != null ? billedUsd - accountCostUsd : null),
        bucketHours: opsBucketHours,
        source: 'ops' as const,
        observed: true,
      }
    })
  }

  return usage.map((point, index) => {
    const billedUsd = usageBilled[index]
    const accountCostUsd = usageAccountCost[index]
    return {
      timestamp: point.date,
      billedUsd: point.observed === false ? null : billedUsd,
      accountCostUsd: point.observed === false ? null : accountCostUsd,
      contributionUsd: point.observed === false || billedUsd == null || accountCostUsd == null ? null : billedUsd - accountCostUsd,
      bucketHours: usageBucketHours,
      source: 'usage_logs' as const,
      observed: point.observed !== false,
    }
  })
}
