import { computed, ref } from 'vue'
import { adminAPI } from '@/api/admin'
import type { OpsDashboardOverview, OpsThroughputTrendPoint } from '@/api/admin/ops'
import type { SystemSettings } from '@/api/admin/settings'
import type { Channel, ModelDefaultPricing, PricingCatalogStatus } from '@/api/admin/channels'
import type {
  Account,
  AccountUsageInfo,
  AdminUsageLog,
  DashboardStats,
  ModelStat,
  WindowStats,
} from '@/types'
import type { CostProfile } from './model'
import { aggregateModelStatsFromUsageLogs, type ModelCostSource } from './modelCostAnalysis'
import { buildModelRouteRows, type ModelRouteRow } from './modelRouteAnalysis'
import { loadUsdCnyExchangeRate, type UsdCnyExchangeRate } from './exchangeRate'
import type { AccountProbeState } from './upstreamTable'
import {
  aggregateUsageWindow,
  fillCostTrendBuckets,
  localDateParameter,
  usageWindowBounds,
  type CostTrendDataPoint,
} from './usageWindow'

export type CostCenterRange = 'today' | '1m' | '5m' | '30m' | '1h' | '6h' | '24h' | '7d' | '30d'

export const DEFAULT_COST_CENTER_RANGE: CostCenterRange = '1h'
export const DEFAULT_MODEL_COST_RANGE: CostCenterRange = '1h'

const USAGE_PAGE_SIZE = 1000
const MAX_USAGE_PAGES = 25
const MAX_MODEL_PRICING_LOOKUPS = 100
const ACCOUNT_USAGE_CACHE_TTL_MS = 5 * 60 * 1000

export function buildCostCenterSnapshotQuery(range: CostCenterRange): {
  time_range?: Exclude<CostCenterRange, 'today'>
  start_time?: string
  end_time?: string
  granularity: 'day' | 'hour' | 'minute'
} {
  if (range === 'today') {
    const now = new Date()
    const start = new Date(now)
    start.setHours(0, 0, 0, 0)
    const end = new Date(start)
    end.setDate(end.getDate() + 1)
    return { start_time: start.toISOString(), end_time: end.toISOString(), granularity: 'hour' }
  }
  return {
    time_range: range,
    granularity: range === '7d' || range === '30d' ? 'day' : range === '1m' || range === '5m' || range === '30m' || range === '1h' ? 'minute' : 'hour',
  }
}

export function buildCostCenterDataQueries(observationRange: CostCenterRange, modelRange: CostCenterRange) {
  return {
    observation: buildCostCenterSnapshotQuery(observationRange),
    model: buildCostCenterSnapshotQuery(modelRange),
  }
}

export function snapshotMatchesRequestedWindow(
  snapshot: { start_time?: string; end_time?: string },
  requestedStart: Date,
  requestedEnd: Date,
): boolean {
  const actualStart = new Date(snapshot.start_time || '').getTime()
  const actualEnd = new Date(snapshot.end_time || '').getTime()
  if (!Number.isFinite(actualStart) || !Number.isFinite(actualEnd)) return false
  const toleranceMs = 60_000
  return Math.abs(actualStart - requestedStart.getTime()) <= toleranceMs
    && Math.abs(actualEnd - requestedEnd.getTime()) <= toleranceMs
}

export function selectExactWindowModelStats(
  snapshot: { start_time?: string; end_time?: string; models?: ModelStat[] } | null,
  compatibility: { logs: AdminUsageLog[]; truncated: boolean } | null,
  requestedStart: Date,
  requestedEnd: Date,
  source: ModelCostSource,
): {
  models: ModelStat[]
  usedCompatibilityAggregation: boolean
  compatibilityTruncated: boolean
} {
  if (snapshot && snapshotMatchesRequestedWindow(snapshot, requestedStart, requestedEnd)) {
    return {
      models: snapshot.models ?? [],
      usedCompatibilityAggregation: false,
      compatibilityTruncated: false,
    }
  }
  if (!compatibility) {
    return { models: [], usedCompatibilityAggregation: false, compatibilityTruncated: false }
  }
  if (compatibility.truncated) {
    return { models: [], usedCompatibilityAggregation: false, compatibilityTruncated: true }
  }
  return {
    models: aggregateModelStatsFromUsageLogs(compatibility.logs, source),
    usedCompatibilityAggregation: true,
    compatibilityTruncated: false,
  }
}

function buildOpsSnapshotRange(range: CostCenterRange): Exclude<CostCenterRange, 'today'> {
  // Ops snapshots only accept rolling windows. The dashboard snapshot above still
  // uses exact local-day bounds for the authoritative cost and usage trend.
  return range === 'today' ? '24h' : range
}

function emptyTodayStats(): WindowStats {
  return { requests: 0, tokens: 0, cost: 0, standard_cost: 0, user_cost: 0 }
}

export function useCostCenterData() {
  const accounts = ref<Account[]>([])
  const accountUsage = ref<Record<string, AccountUsageInfo>>({})
  const todayStats = ref<Record<string, WindowStats>>({})
  const stats = ref<DashboardStats | null>(null)
  const trend = ref<CostTrendDataPoint[]>([])
  const trendUsesAccountCost = ref(false)
  const models = ref<ModelStat[]>([])
  const modelCostSource = ref<ModelCostSource>('upstream')
  const modelCostAccountId = ref<number | null>(null)
  const modelCostRange = ref<CostCenterRange>(DEFAULT_MODEL_COST_RANGE)
  const modelRoutes = ref<ModelRouteRow[]>([])
  const modelRoutesTruncated = ref(false)
  const modelStatsExactWindowFallback = ref(false)
  const modelStatsCompatibilityTruncated = ref(false)
  const modelPricing = ref<Record<string, ModelDefaultPricing>>({})
  const pricingStatus = ref<PricingCatalogStatus | null>(null)
  const pricingRefreshing = ref(false)
  const opsOverview = ref<OpsDashboardOverview | null>(null)
  const opsTrend = ref<OpsThroughputTrendPoint[]>([])
  const systemSettings = ref<SystemSettings | null>(null)
  const probes = ref<Record<string, AccountProbeState>>({})
  const loading = ref(false)
  const saving = ref(false)
  const error = ref('')
  const lastUpdated = ref<Date | null>(null)
  const exchangeRate = ref<UsdCnyExchangeRate>({
    rate: 7.2,
    rateDate: null,
    fetchedAt: null,
    source: 'fallback',
  })
  let requestSequence = 0
  let lastAccountUsageSyncAt = 0

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

  async function loadModelRouteLogs(range: CostCenterRange, accountId: number | null): Promise<{ logs: AdminUsageLog[]; truncated: boolean }> {
    const { start, end } = usageWindowBounds(range)
    const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
    const logs: AdminUsageLog[] = []
    let reachedWindowStart = false

    for (let page = 1; page <= MAX_USAGE_PAGES; page += 1) {
      const response = await adminAPI.usage.list({
        page,
        page_size: USAGE_PAGE_SIZE,
        account_id: accountId ?? undefined,
        start_date: localDateParameter(start),
        end_date: localDateParameter(new Date(end.getTime() - 1)),
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

    return {
      logs: logs.filter((log) => {
        const createdAt = new Date(log.created_at).getTime()
        return createdAt >= start.getTime() && createdAt < end.getTime()
      }),
      truncated: !reachedWindowStart,
    }
  }

  async function reload(range: CostCenterRange = DEFAULT_COST_CENTER_RANGE) {
    const sequence = ++requestSequence
    loading.value = true
    error.value = ''
    const queries = buildCostCenterDataQueries(range, modelCostRange.value)
    const { start: observationStart, end: requestedObservationEnd } = usageWindowBounds(range)
    const observationEnd = range === 'today' ? new Date() : requestedObservationEnd
    const { start: modelStart, end: modelEnd } = usageWindowBounds(modelCostRange.value)

    const [accountResult, dashboardResult, modelResult, modelRoutesResult, channelsResult, pricingStatusResult, opsResult, settingsResult, exchangeRateResult] = await Promise.allSettled([
      adminAPI.accounts.list(1, 1000, {
        include_scheduler_score: 'true',
        sort_by: 'created_at',
        sort_order: 'asc',
      }),
      adminAPI.dashboard.getSnapshotV2({
        ...queries.observation,
        include_stats: true,
        include_trend: true,
        include_model_stats: false,
        include_group_stats: false,
        include_users_trend: false,
      }),
      adminAPI.dashboard.getSnapshotV2({
        ...queries.model,
        account_id: modelCostAccountId.value ?? undefined,
        model_source: modelCostSource.value,
        include_stats: false,
        include_trend: false,
        include_model_stats: true,
        include_group_stats: false,
        include_users_trend: false,
      }),
      loadModelRouteLogs(modelCostRange.value, modelCostAccountId.value),
      adminAPI.channels.list(1, 1000, { sort_by: 'created_at', sort_order: 'asc' }),
      adminAPI.channels.getPricingStatus(),
      adminAPI.ops.getDashboardSnapshotV2({ time_range: buildOpsSnapshotRange(range), mode: 'auto' }),
      adminAPI.settings.getSettings(),
      loadUsdCnyExchangeRate(),
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

      const usageAccounts = accounts.value.filter(shouldLoadUsageWindow)
      const usageCacheIsFresh = Date.now() - lastAccountUsageSyncAt < ACCOUNT_USAGE_CACHE_TTL_MS
      if (!usageCacheIsFresh || usageAccounts.some((account) => !accountUsage.value[String(account.id)])) {
        const usageResults = await Promise.allSettled(
          usageAccounts.map((account) => adminAPI.accounts.getUsage(account.id, 'passive')),
        )
        if (sequence !== requestSequence) return
        const nextUsage: Record<string, AccountUsageInfo> = {}
        usageAccounts.forEach((account, index) => {
          const result = usageResults[index]
          if (result.status === 'fulfilled') nextUsage[String(account.id)] = result.value
          else if (accountUsage.value[String(account.id)]) nextUsage[String(account.id)] = accountUsage.value[String(account.id)]
        })
        accountUsage.value = nextUsage
        lastAccountUsageSyncAt = Date.now()
      }
    } else {
      accounts.value = []
      todayStats.value = {}
      accountUsage.value = {}
    }

    if (sequence !== requestSequence) return

    if (dashboardResult.status === 'fulfilled') {
      stats.value = dashboardResult.value.stats ?? null
      if (compatibilityTrend) {
        try {
          trend.value = fillCostTrendBuckets(await compatibilityTrend, range, observationStart, observationEnd)
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
        trend.value = fillCostTrendBuckets(dashboardResult.value.trend ?? [], range, observationStart, observationEnd)
        trendUsesAccountCost.value = false
      }
    } else {
      stats.value = null
      trend.value = []
      trendUsesAccountCost.value = false
    }

    const modelStatsSelection = selectExactWindowModelStats(
      modelResult.status === 'fulfilled' ? modelResult.value : null,
      modelRoutesResult.status === 'fulfilled' ? modelRoutesResult.value : null,
      modelStart,
      modelEnd,
      modelCostSource.value,
    )
    models.value = modelStatsSelection.models
    modelStatsExactWindowFallback.value = modelStatsSelection.usedCompatibilityAggregation
    modelStatsCompatibilityTruncated.value = modelStatsSelection.compatibilityTruncated
    if (modelRoutesResult.status === 'fulfilled') {
      const channels: Channel[] = channelsResult.status === 'fulfilled' ? channelsResult.value.items ?? [] : []
      modelRoutes.value = buildModelRouteRows(modelRoutesResult.value.logs, channels)
      modelRoutesTruncated.value = modelRoutesResult.value.truncated
      const actualModels = [...new Set(modelRoutes.value.map((row) => row.upstreamModel))].slice(0, MAX_MODEL_PRICING_LOOKUPS)
      const pricingResults = await Promise.allSettled(actualModels.map((model) => adminAPI.channels.getModelDefaultPricing(model)))
      if (sequence !== requestSequence) return
      modelPricing.value = Object.fromEntries(actualModels.flatMap((model, index) => {
        const result = pricingResults[index]
        return result.status === 'fulfilled' ? [[model, result.value]] : []
      }))
    } else {
      modelRoutes.value = []
      modelRoutesTruncated.value = false
      modelPricing.value = {}
    }
    pricingStatus.value = pricingStatusResult.status === 'fulfilled' ? pricingStatusResult.value : null

    if (opsResult.status === 'fulfilled') {
      opsOverview.value = opsResult.value.overview
      opsTrend.value = opsResult.value.throughput_trend?.points ?? []
    } else {
      // Ops monitoring is feature-gated. The cost console remains useful without it.
      opsOverview.value = null
      opsTrend.value = []
    }

    systemSettings.value = settingsResult.status === 'fulfilled' ? settingsResult.value : null
    if (exchangeRateResult.status === 'fulfilled') exchangeRate.value = exchangeRateResult.value

    if (
      accountResult.status === 'rejected' &&
      dashboardResult.status === 'rejected' &&
      modelResult.status === 'rejected' &&
      modelRoutesResult.status === 'rejected' &&
      opsResult.status === 'rejected'
    ) {
      error.value = '无法读取成本中心数据，请检查桌面端连接地址与管理员登录状态。'
    }

    lastUpdated.value = new Date()
    loading.value = false
  }

  async function refreshPricingCatalog() {
    pricingRefreshing.value = true
    try {
      pricingStatus.value = await adminAPI.channels.refreshPricing()
      return pricingStatus.value
    } finally {
      pricingRefreshing.value = false
    }
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

  async function bulkSaveCostProfile(accountIds: number[], profile: CostProfile) {
    saving.value = true
    try {
      return await adminAPI.accounts.bulkUpdate(accountIds, {
        extra: {
          cost_profile: {
            amount: profile.amount,
            currency: profile.currency,
            billing_cycle: profile.billing_cycle,
            started_at: profile.started_at,
            algorithm_version: profile.algorithm_version,
          },
        },
      })
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
    accountUsage,
    todayStats,
    stats,
    trend,
    trendUsesAccountCost,
    models,
    modelCostSource,
    modelCostAccountId,
    modelCostRange,
    modelRoutes,
    modelRoutesTruncated,
    modelStatsExactWindowFallback,
    modelStatsCompatibilityTruncated,
    modelPricing,
    pricingStatus,
    pricingRefreshing,
    opsOverview,
    opsTrend,
    systemSettings,
    probes,
    loading,
    saving,
    error,
    lastUpdated,
    exchangeRate,
    accountStats,
    reload,
    refreshPricingCatalog,
    saveCostProfile,
    bulkSaveCostProfile,
    probeAccount,
  }
}

function shouldLoadUsageWindow(account: Account): boolean {
  if (account.platform === 'gemini') return true
  if (account.platform === 'anthropic') return account.type === 'oauth' || account.type === 'setup-token'
  return account.type === 'oauth' && ['openai', 'antigravity', 'grok'].includes(account.platform)
}
