import { computed, ref } from 'vue'
import { adminAPI } from '@/api/admin'
import type { OpsDashboardOverview, OpsThroughputTrendPoint } from '@/api/admin/ops'
import type {
  Account,
  DashboardStats,
  ModelStat,
  TrendDataPoint,
  WindowStats,
} from '@/types'
import type { CostProfile } from './model'

export type CostCenterRange = '5m' | '30m' | '1h' | '6h' | '24h' | '7d'

export interface AccountProbeState {
  loading: boolean
  success?: boolean
  latency_ms?: number
  message?: string
}

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
  const trend = ref<TrendDataPoint[]>([])
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

    if (dashboardResult.status === 'fulfilled') {
      stats.value = dashboardResult.value.stats ?? null
      trend.value = dashboardResult.value.trend ?? []
      models.value = dashboardResult.value.models ?? []
    } else {
      stats.value = null
      trend.value = []
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
