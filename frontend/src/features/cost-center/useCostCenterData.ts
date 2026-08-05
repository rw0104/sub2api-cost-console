import { computed, ref } from 'vue'
import { adminAPI } from '@/api/admin'
import type { OpsDashboardOverview, OpsThroughputTrendPoint } from '@/api/admin/ops'
import type {
  Account,
  AdminUsageLog,
  DashboardStats,
  ModelStat,
  WindowStats,
} from '@/types'
import type { CostProfile } from './model'
import {
  aggregateUsageWindow,
  localDateParameter,
  usageWindowBounds,
  type CostTrendDataPoint,
} from './usageWindow'

export type CostCenterRange = '5m' | '30m' | '1h' | '6h' | '24h' | '7d'

export interface AccountProbeState {
  loading: boolean
  success?: boolean
  latency_ms?: number
  message?: string
}

const USAGE_PAGE_SIZE = 1000
const MAX_USAGE_PAGES = 25

export function buildCostCenterSnapshotQuery(range: CostCenterRange): {
  time_range: CostCenterRange
  granularity: 'day' | 'hour' | 'minute'
} {
  return {
    time_range: range,
    granularity: range === '7d' ? 'day' : range === '5m' || range === '30m' ? 'minute' : 'hour',
  }
}

function emptyTodayStats(): WindowStats {
  return { requests: 0, tokens: 0, cost: 0, standard_cost: 0, user_cost: 0 }
}

export function useCostCenterData() {
  const accounts = ref<Account[]>([])
  const todayStats = ref<Record<string, WindowStats>>({})
  const stats = ref<DashboardStats | null>(null)
  const trend = ref<CostTrendDataPoint[]>([])
  const trendUsesAccountCost = ref(false)
  const models = ref<ModelStat[]>([])
  const opsOverview = ref<OpsDashboardOverview | null>(null)
  const opsTrend = ref<OpsThroughputTrendPoint[]>([])
  const probes = ref<Record<string, AccountProbeState>>({})
  const loading = ref(false)
  const saving = ref(false)
  const error = ref('')
  const lastUpdated = ref<Date | null>(null)
  let requestSequence = 0

  const accountStats = computed(() => (account: Account): WindowStats => {
    return todayStats.value[String(account.id)] ?? emptyTodayStats()
  })

  async function loadUsageLogCompatibilityTrend(range: CostCenterRange): Promise<CostTrendDataPoint[]> {
    const { start, end } = usageWindowBounds(range)
    const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
    const logs: AdminUsageLog[] = []
    let reachedWindowStart = false

    for (let page = 1; page <= MAX_USAGE_PAGES; page += 1) {
      const response = await adminAPI.usage.list({
        page,
        page_size: USAGE_PAGE_SIZE,
        start_date: localDateParameter(start),
        end_date: localDateParameter(end),
        timezone,
        sort_by: 'created_at',
        sort_order: 'desc',
        exact_total: false,
      })
      const items = response.items ?? []
      logs.push(...items)
      const oldest = items.at(-1)
      const oldestTime = oldest ? new Date(oldest.created_at).getTime() : Number.NEGATIVE_INFINITY
      if (items.length < USAGE_PAGE_SIZE || oldestTime < start.getTime()) {
        reachedWindowStart = true
        break
      }
    }

    if (!reachedWindowStart) {
      throw new Error('所选窗口超过 25,000 条 usage_logs，已停止聚合以避免显示不完整成本')
    }
    return aggregateUsageWindow(logs, range, start, end)
  }

  async function reload(range: CostCenterRange = '1h') {
    const sequence = ++requestSequence
    loading.value = true
    error.value = ''
    const snapshotQuery = buildCostCenterSnapshotQuery(range)

    const [accountResult, dashboardResult, opsResult] = await Promise.allSettled([
      adminAPI.accounts.list(1, 1000, {
        include_scheduler_score: 'true',
        sort_by: 'created_at',
        sort_order: 'asc',
      }),
      adminAPI.dashboard.getSnapshotV2({
        ...snapshotQuery,
        include_stats: true,
        include_trend: true,
        include_model_stats: true,
        include_group_stats: false,
        include_users_trend: false,
      }),
      adminAPI.ops.getDashboardSnapshotV2({ time_range: range, mode: 'auto' }),
    ])

    if (sequence !== requestSequence) return

    const compatibilityTrend = dashboardResult.status === 'fulfilled'
      && (!dashboardResult.value.start_time || !dashboardResult.value.end_time)
      ? loadUsageLogCompatibilityTrend(range)
      : null

    if (accountResult.status === 'fulfilled') {
      accounts.value = accountResult.value.items ?? []
      const ids = accounts.value.map((account) => account.id)
      if (ids.length > 0) {
        try {
          const batch = await adminAPI.accounts.getBatchTodayStats(ids)
          if (sequence === requestSequence) todayStats.value = batch.stats ?? {}
        } catch (batchError) {
          console.warn('[cost-center] account today stats unavailable', batchError)
          todayStats.value = {}
        }
      } else {
        todayStats.value = {}
      }
    } else {
      accounts.value = []
      todayStats.value = {}
    }

    if (sequence !== requestSequence) return

    if (dashboardResult.status === 'fulfilled') {
      stats.value = dashboardResult.value.stats ?? null
      if (compatibilityTrend) {
        try {
          trend.value = await compatibilityTrend
          if (sequence !== requestSequence) return
          trendUsesAccountCost.value = true
        } catch (compatibilityError) {
          console.warn('[cost-center] exact usage log aggregation unavailable', compatibilityError)
          trend.value = []
          trendUsesAccountCost.value = false
          error.value = compatibilityError instanceof Error
            ? compatibilityError.message
            : '官方上游内核无法提供精确时间窗口，usage_logs 兼容聚合失败'
        }
      } else {
        trend.value = dashboardResult.value.trend ?? []
        trendUsesAccountCost.value = false
      }
      models.value = dashboardResult.value.models ?? []
    } else {
      stats.value = null
      trend.value = []
      trendUsesAccountCost.value = false
      models.value = []
    }

    if (opsResult.status === 'fulfilled') {
      opsOverview.value = opsResult.value.overview
      opsTrend.value = opsResult.value.throughput_trend?.points ?? []
    } else {
      // Ops monitoring is feature-gated. The cost console remains useful without it.
      opsOverview.value = null
      opsTrend.value = []
    }

    if (
      accountResult.status === 'rejected' &&
      dashboardResult.status === 'rejected' &&
      opsResult.status === 'rejected'
    ) {
      error.value = '无法读取成本中心数据，请检查桌面端连接地址与管理员登录状态。'
    }

    lastUpdated.value = new Date()
    loading.value = false
  }

  async function saveCostProfile(account: Account, profile: CostProfile) {
    saving.value = true
    try {
      const updated = await adminAPI.accounts.update(account.id, {
        extra: {
          ...(account.extra ?? {}),
          cost_profile: {
            amount: profile.amount,
            currency: profile.currency,
            billing_cycle: profile.billing_cycle,
            started_at: profile.started_at,
            algorithm_version: profile.algorithm_version,
          },
        },
      })
      const index = accounts.value.findIndex((item) => item.id === account.id)
      if (index >= 0) accounts.value.splice(index, 1, updated)
      return updated
    } finally {
      saving.value = false
    }
  }

  async function probeAccount(account: Account) {
    probes.value[String(account.id)] = {
      loading: true,
      message: '正在请求真实上游…',
    }
    try {
      const result = await adminAPI.accounts.testAccount(account.id)
      probes.value[String(account.id)] = {
        loading: false,
        success: result.success,
        latency_ms: result.latency_ms,
        message: result.message,
      }
      return result
    } catch (probeError: any) {
      probes.value[String(account.id)] = {
        loading: false,
        success: false,
        message: probeError?.message || 'Probe failed',
      }
      throw probeError
    }
  }

  return {
    accounts,
    todayStats,
    stats,
    trend,
    trendUsesAccountCost,
    models,
    opsOverview,
    opsTrend,
    probes,
    loading,
    saving,
    error,
    lastUpdated,
    accountStats,
    reload,
    saveCostProfile,
    probeAccount,
  }
}
