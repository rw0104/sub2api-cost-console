export interface AccountProbeState {
  loading: boolean
  success?: boolean
  latency_ms?: number
  message?: string
}

export type AccountRankingMetric = 'score' | 'output' | 'requests' | 'cost' | 'latency' | 'reliability'

export interface AccountRankingEvidenceInput {
  today?: {
    requests?: number | null
    cost?: number | null
    user_cost?: number | null
  } | null
  probe?: AccountProbeState
  state: CurrentAccountState['state']
}

// A scheduler score by itself is not proof that an account produced traffic or
// was successfully tested. Keep unobserved accounts out of numbered rankings so
// a newly-added, idle provider cannot outrank accounts with factual evidence.
export function hasAccountRankingEvidence(
  metric: AccountRankingMetric,
  input: AccountRankingEvidenceInput,
): boolean {
  const requests = Number(input.today?.requests || 0)
  const accountCost = Number(input.today?.cost || 0)
  const billed = Number(input.today?.user_cost || 0)
  const probeCompleted = input.probe?.success != null

  if (metric === 'output') return billed > 0
  if (metric === 'requests') return requests > 0
  if (metric === 'cost') return accountCost > 0
  if (metric === 'latency') return probeCompleted && Number.isFinite(input.probe?.latency_ms)
  return requests > 0 || probeCompleted || input.state !== 'normal'
}

export interface CurrentAccountStateInput {
  status: 'active' | 'inactive' | 'error'
  schedulable?: boolean
  error_message?: string | null
  rate_limited_at?: string | null
  rate_limit_reset_at?: string | null
  overload_until?: string | null
  temp_unschedulable_until?: string | null
  temp_unschedulable_reason?: string | null
  extra?: {
    model_rate_limits?: Record<string, { rate_limit_reset_at?: string | null }>
  } | null
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

function isFuture(value: string | null | undefined, now: Date): boolean {
  if (!value) return false
  const timestamp = new Date(value).getTime()
  return Number.isFinite(timestamp) && timestamp > now.getTime()
}

function resetLabel(value: string): string {
  const resetAt = new Date(value)
  return Number.isFinite(resetAt.getTime())
    ? resetAt.toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
    : value
}

export interface CurrentAccountState {
  error: 0 | 1
  limited: 0 | 1
  note: string
  state: 'normal' | 'limited' | 'error' | 'inactive'
  label: string
}

export function describeCurrentAccountState(
  account: CurrentAccountStateInput,
  probe?: AccountProbeState,
  now = new Date(),
): CurrentAccountState {
  const probeMessage = probe?.success === false ? String(probe.message || '真实上游探测失败') : ''
  const probeIsLimit = /\b429\b|rate.?limit|usage_limit/i.test(probeMessage)
  if (probeMessage) {
    return probeIsLimit
      ? { error: 0, limited: 1, state: 'limited', label: '探测限流', note: probeMessage }
      : { error: 1, limited: 0, state: 'error', label: '探测失败', note: probeMessage }
  }

  const activeRateLimit = isFuture(account.rate_limit_reset_at, now)
  const activeOverload = isFuture(account.overload_until, now)
  const activeTempBlock = isFuture(account.temp_unschedulable_until, now)
  const activeModelLimits = Object.entries(account.extra?.model_rate_limits ?? {})
    .filter(([, value]) => isFuture(value?.rate_limit_reset_at, now))

  if (account.status === 'error') {
    return { error: 1, limited: activeRateLimit ? 1 : 0, state: 'error', label: '错误', note: account.error_message || '账号已被上游错误停用' }
  }
  if (account.status === 'inactive') {
    return { error: 0, limited: 0, state: 'inactive', label: '未启用', note: '账号当前未启用' }
  }
  if (activeRateLimit && account.rate_limit_reset_at) {
    return { error: 0, limited: 1, state: 'limited', label: '429 限流', note: `当前限流 · 预计 ${resetLabel(account.rate_limit_reset_at)} 重置` }
  }
  if (activeTempBlock) {
    return { error: 0, limited: 1, state: 'limited', label: '临时停调度', note: account.temp_unschedulable_reason || '上游临时不可调度' }
  }
  if (activeOverload && account.overload_until) {
    return { error: 0, limited: 1, state: 'limited', label: '上游过载', note: `过载保护至 ${resetLabel(account.overload_until)}` }
  }
  if (activeModelLimits.length > 0) {
    return { error: 0, limited: 1, state: 'limited', label: '模型限流', note: `${activeModelLimits.map(([model]) => model).join('、')} 当前不可调度` }
  }
  if (account.schedulable === false) {
    return { error: 1, limited: 0, state: 'error', label: '已停调度', note: account.error_message || '账号已从调度池移除' }
  }
  return { error: 0, limited: 0, state: 'normal', label: '可调度', note: '当前状态正常' }
}
