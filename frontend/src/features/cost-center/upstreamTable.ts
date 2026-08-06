export interface AccountProbeState {
  loading: boolean
  success?: boolean
  latency_ms?: number
  message?: string
}

export interface CurrentAccountStateInput {
  status: 'active' | 'inactive' | 'error'
  error_message?: string | null
  rate_limited_at?: string | null
  rate_limit_reset_at?: string | null
}

type SchedulerSettings = Record<string, unknown> | null | undefined

const SCHEDULER_BASE_WEIGHTS = [
  ['openai_advanced_scheduler_effective_weight_priority', 1],
  ['openai_advanced_scheduler_effective_weight_load', 1],
  ['openai_advanced_scheduler_effective_weight_queue', 0.7],
  ['openai_advanced_scheduler_effective_weight_error_rate', 0.8],
  ['openai_advanced_scheduler_effective_weight_ttft', 0.5],
  ['openai_advanced_scheduler_effective_weight_reset', 0],
  ['openai_advanced_scheduler_effective_weight_quota_headroom', 0],
  ['openai_advanced_scheduler_effective_weight_upstream_cost', 0],
] as const

function nonNegativeNumber(value: unknown, fallback: number): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback
}

export function resolveSchedulerBaseScoreMax(settings: SchedulerSettings): number {
  const total = SCHEDULER_BASE_WEIGHTS.reduce((sum, [key, fallback]) => {
    return sum + nonNegativeNumber(settings?.[key], fallback)
  }, 0)
  return total > 0 ? total : 4
}

export function normalizeSchedulerScore(rawScore: number, maxScore: number): number {
  if (!Number.isFinite(rawScore) || !Number.isFinite(maxScore) || maxScore <= 0) return 0
  return Math.max(0, Math.min(100, rawScore / maxScore * 100))
}

export function describeCurrentAccountState(account: CurrentAccountStateInput): {
  error: 0 | 1
  limited: 0 | 1
  note: string
} {
  const error = account.status === 'error' ? 1 : 0
  const limited = account.rate_limited_at ? 1 : 0
  if (account.error_message) return { error, limited, note: account.error_message }
  if (limited && account.rate_limit_reset_at) {
    const resetAt = new Date(account.rate_limit_reset_at)
    const resetLabel = Number.isFinite(resetAt.getTime())
      ? resetAt.toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
      : account.rate_limit_reset_at
    return { error, limited, note: `当前限流 · 预计 ${resetLabel} 重置` }
  }
  if (limited) return { error, limited, note: '当前限流 · 等待上游重置' }
  if (account.status === 'inactive') return { error, limited, note: '账号当前未启用' }
  return { error, limited, note: '当前状态正常' }
}
