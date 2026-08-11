import { computed, ref } from 'vue'
import { adminAPI } from '@/api/admin'
import type { OpsDashboardOverview, OpsErrorTrendPoint, OpsThroughputTrendPoint } from '@/api/admin/ops'
import type { SystemSettings } from '@/api/admin/settings'
import type { Channel, ModelDefaultPricing, PricingCatalogStatus } from '@/api/admin/channels'
import type { AccountCostLossState, AccountEconomicsSnapshot } from '@/api/admin/accounts'
import type {
  Account,
  AccountUsageInfo,
  AdminUsageLog,
  DashboardStats,
  ModelStat,
  WindowStats,
} from '@/types'
import type { CostProfile } from './model'
import {
  aggregateModelStatsFromUsageLogs,
  summarizeModelAudit,
  type ModelAuditSummary,
  type ModelCostSource,
} from './modelCostAnalysis'
import { buildModelRouteRows, type ModelRouteRow } from './modelRouteAnalysis'
import { loadUsdCnyExchangeRate, type UsdCnyExchangeRate } from './exchangeRate'
import type { AccountProbeState } from './upstreamTable'
import {
  COST_CENTER_SOURCE_KEYS,
  createDataSourceStates,
  hasMeasuredData,
  sourceState,
  type CostCenterSourceKey,
  type DataAvailability,
} from './dataState'
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

export function economicsWindowHours(range: CostCenterRange): number {
  return ({ today: 24, '1m': 1 / 60, '5m': 5 / 60, '30m': 0.5, '1h': 1, '6h': 6, '24h': 24, '7d': 168, '30d': 720 })[range]
}

function economicsPlatformFilter(filter: string): string | undefined {
  if (filter === 'all') return undefined
  if (filter === 'codex' || filter === 'openai' || filter === 'azure-openai') return 'openai'
  return filter
}

const USAGE_PAGE_SIZE = 1000
const MAX_USAGE_PAGES = 25
const MAX_MODEL_PRICING_LOOKUPS = 100
const ACCOUNT_USAGE_CACHE_TTL_MS = 5 * 60 * 1000

export function filterModelAuditLogs(logs: AdminUsageLog[], mismatchOnly: boolean): AdminUsageLog[] {
  return mismatchOnly ? logs.filter((log) => log.upstream_model_mismatch === true) : logs
}

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
  // Rolling ranges map directly. The natural-day range is sent with explicit
  // local-day boundaries at the call site instead of being mislabeled as 24h.
  return range === 'today' ? '24h' : range
}

function emptyTodayStats(): WindowStats {
  return { requests: 0, tokens: 0, cost: 0, standard_cost: 0, user_cost: 0 }
}

export function useCostCenterData() {
  const accounts = ref<Account[]>([])
  const costLossStates = ref<AccountCostLossState[]>([])
  const accountEconomics = ref<AccountEconomicsSnapshot | null>(null)
  const accountUsage = ref<Record<string, AccountUsageInfo>>({})
  const todayStats = ref<Record<string, WindowStats>>({})
  const stats = ref<DashboardStats | null>(null)
  const trend = ref<CostTrendDataPoint[]>([])
  const trendUsesAccountCost = ref(false)
  const models = ref<ModelStat[]>([])
  const modelCostSource = ref<ModelCostSource>('upstream')
  const modelCostAccountId = ref<number | null>(null)
  const modelCostRange = ref<CostCenterRange>(DEFAULT_MODEL_COST_RANGE)
  const modelAuditMismatchOnly = ref(false)
  const modelAuditSummary = ref<ModelAuditSummary>(summarizeModelAudit([]))
  const modelRoutes = ref<ModelRouteRow[]>([])
  const modelRoutesTruncated = ref(false)
  const modelStatsExactWindowFallback = ref(false)
  const modelStatsCompatibilityTruncated = ref(false)
  const modelPricing = ref<Record<string, ModelDefaultPricing>>({})
  const pricingStatus = ref<PricingCatalogStatus | null>(null)
  const pricingRefreshing = ref(false)
  const opsOverview = ref<OpsDashboardOverview | null>(null)
  const opsTrend = ref<OpsThroughputTrendPoint[]>([])
  const opsErrorTrend = ref<OpsErrorTrendPoint[]>([])
  const systemSettings = ref<SystemSettings | null>(null)
  const probes = ref<Record<string, AccountProbeState>>({})
  const sourceStates = ref(createDataSourceStates())
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
  let economicsRequestSequence = 0
  let lastAccountUsageSyncAt = 0

  const accountStats = computed(() => (account: Account): WindowStats | null => {
    if (!hasMeasuredData(sourceStates.value.todayStats)) return null
    return todayStats.value[String(account.id)] ?? emptyTodayStats()
  })

  function setSourceState(key: CostCenterSourceKey, status: DataAvailability, reason = '') {
    sourceStates.value[key] = sourceState(key, status, reason)
  }

  function rejectedReason(result: PromiseRejectedResult, fallback: string): string {
    const reason = result.reason
    return reason instanceof Error && reason.message ? reason.message : fallback
  }

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
    for (const key of COST_CENTER_SOURCE_KEYS) {
      if (key !== 'economics') sourceStates.value[key] = sourceState(key, 'loading', '正在刷新')
    }
    const queries = buildCostCenterDataQueries(range, modelCostRange.value)
    const { start: observationStart, end: requestedObservationEnd } = usageWindowBounds(range)
    const observationEnd = range === 'today' ? new Date() : requestedObservationEnd
    const { start: modelStart, end: modelEnd } = usageWindowBounds(modelCostRange.value)

    const [accountResult, costLossResult, dashboardResult, modelResult, modelRoutesResult, channelsResult, pricingStatusResult, opsResult, settingsResult, exchangeRateResult] = await Promise.allSettled([
      adminAPI.accounts.list(1, 1000, {
        include_scheduler_score: 'true',
        sort_by: 'created_at',
        sort_order: 'asc',
      }),
      adminAPI.accounts.listCostLossStates(),
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
        upstream_model_mismatch: modelAuditMismatchOnly.value ? true : undefined,
        include_stats: false,
        include_trend: false,
        include_model_stats: true,
        include_group_stats: false,
        include_users_trend: false,
      }),
      loadModelRouteLogs(modelCostRange.value, modelCostAccountId.value),
      adminAPI.channels.list(1, 1000, { sort_by: 'created_at', sort_order: 'asc' }),
      adminAPI.channels.getPricingStatus(),
      adminAPI.ops.getDashboardSnapshotV2(range === 'today'
        ? { start_time: observationStart.toISOString(), end_time: observationEnd.toISOString(), mode: 'auto' }
        : { time_range: buildOpsSnapshotRange(range), mode: 'auto' }),
      adminAPI.settings.getSettings(),
      loadUsdCnyExchangeRate(),
    ])

    if (sequence !== requestSequence) return
    costLossStates.value = costLossResult.status === 'fulfilled' ? costLossResult.value.states ?? [] : []
    setSourceState(
      'costLoss',
      costLossResult.status === 'fulfilled'
        ? (costLossStates.value.length ? 'measured' : 'empty')
        : 'unavailable',
      costLossResult.status === 'fulfilled'
        ? (costLossStates.value.length ? '不可变损失账本已读取' : '账本中没有终局损失事件')
        : rejectedReason(costLossResult, '封禁损失账本读取失败'),
    )

    setSourceState(
      'accounts',
      accountResult.status === 'fulfilled'
        ? ((accountResult.value.items ?? []).length ? 'measured' : 'empty')
        : 'unavailable',
      accountResult.status === 'fulfilled'
        ? ((accountResult.value.items ?? []).length ? '账号与调度字段已读取' : '当前没有账号')
        : rejectedReason(accountResult, '账号清单读取失败'),
    )

    setSourceState(
      'dashboard',
      dashboardResult.status === 'fulfilled'
        ? ((dashboardResult.value.trend ?? []).length || dashboardResult.value.stats ? 'measured' : 'empty')
        : 'unavailable',
      dashboardResult.status === 'fulfilled' ? 'usage_logs 聚合已读取' : rejectedReason(dashboardResult, '成本趋势读取失败'),
    )

    const dashboardWindowIsExact = dashboardResult.status === 'fulfilled'
      && snapshotMatchesRequestedWindow(dashboardResult.value, observationStart, requestedObservationEnd)
    const compatibilityTrend = dashboardWindowIsExact ? null : loadUsageLogCompatibilityTrend(range)

    if (accountResult.status === 'fulfilled') {
      accounts.value = accountResult.value.items ?? []
      const ids = accounts.value.map((account) => account.id)
      if (ids.length > 0) {
        try {
          const localDayStart = new Date()
          localDayStart.setHours(0, 0, 0, 0)
          const batch = await adminAPI.accounts.getBatchTodayStats(ids, localDayStart.toISOString())
          if (sequence === requestSequence) {
            todayStats.value = batch.stats ?? {}
            setSourceState('todayStats', Object.keys(todayStats.value).length ? 'measured' : 'empty', Object.keys(todayStats.value).length ? '本地自然日账号统计已读取' : '本地自然日内没有账号用量记录')
          }
        } catch (batchError) {
          console.warn('[cost-center] account today stats unavailable', batchError)
          todayStats.value = {}
          setSourceState('todayStats', 'unavailable', batchError instanceof Error ? batchError.message : '账号当日统计读取失败')
        }
      } else {
        todayStats.value = {}
        setSourceState('todayStats', 'empty', '当前没有账号')
      }

      const usageAccounts = accounts.value.filter(shouldLoadUsageWindow)
      const usageCacheIsFresh = Date.now() - lastAccountUsageSyncAt < ACCOUNT_USAGE_CACHE_TTL_MS
      if (!usageCacheIsFresh || usageAccounts.some((account) => !accountUsage.value[String(account.id)])) {
        const usageResults = await Promise.allSettled(
          usageAccounts.map((account) => adminAPI.accounts.getUsage(account.id, accountUsageSource(account))),
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
        const succeeded = usageResults.filter((result) => result.status === 'fulfilled').length
        const retained = usageAccounts.filter((account) => accountUsage.value[String(account.id)]).length
        setSourceState(
          'accountUsage',
          succeeded === usageAccounts.length ? 'measured' : retained > 0 ? 'partial' : 'unavailable',
          succeeded === usageAccounts.length ? '上游用量窗口已同步' : `${succeeded}/${usageAccounts.length} 个用量窗口本次同步成功`,
        )
      } else if (usageAccounts.length === 0) {
        setSourceState('accountUsage', 'empty', '当前账号类型不提供上游用量窗口')
      } else {
        setSourceState('accountUsage', 'measured', '使用 5 分钟内最近一次成功同步的用量窗口')
      }
    } else {
      accounts.value = []
      todayStats.value = {}
      accountUsage.value = {}
      setSourceState('todayStats', 'unavailable', '账号清单不可用，无法读取当日统计')
      setSourceState('accountUsage', 'unavailable', '账号清单不可用，无法读取用量窗口')
    }

    if (sequence !== requestSequence) return

    stats.value = dashboardResult.status === 'fulfilled' ? dashboardResult.value.stats ?? null : null
    if (compatibilityTrend) {
      try {
        trend.value = fillCostTrendBuckets(await compatibilityTrend, range, observationStart, observationEnd)
        if (sequence !== requestSequence) return
        trendUsesAccountCost.value = true
        setSourceState('dashboard', trend.value.some((point) => Number(point.requests || 0) > 0) ? 'partial' : 'empty', '仪表盘窗口不精确，已按 usage_logs 重新聚合')
      } catch (compatibilityError) {
        console.warn('[cost-center] exact usage log aggregation unavailable', compatibilityError)
        trend.value = []
        trendUsesAccountCost.value = false
        setSourceState('dashboard', 'unavailable', compatibilityError instanceof Error ? compatibilityError.message : 'usage_logs 兼容聚合失败')
        error.value = compatibilityError instanceof Error
          ? compatibilityError.message
          : '官方上游内核无法提供精确时间窗口，usage_logs 兼容聚合失败'
      }
    } else if (dashboardResult.status === 'fulfilled') {
      // The legacy dashboard endpoint only exposes minute/hour/day buckets.
      // Keep its native points when that density is coarser than the adaptive
      // chart contract; manufacturing sub-buckets would create false zeros.
      trend.value = range === '30m' || range === '1h'
        ? fillCostTrendBuckets(dashboardResult.value.trend ?? [], range, observationStart, observationEnd)
        : (dashboardResult.value.trend ?? [])
      trendUsesAccountCost.value = false
    } else {
      trend.value = []
      trendUsesAccountCost.value = false
    }

    const modelCompatibility = modelRoutesResult.status === 'fulfilled'
      ? {
          ...modelRoutesResult.value,
          logs: filterModelAuditLogs(modelRoutesResult.value.logs, modelAuditMismatchOnly.value),
        }
      : null
    const modelStatsSelection = selectExactWindowModelStats(
      modelResult.status === 'fulfilled' ? modelResult.value : null,
      modelCompatibility,
      modelStart,
      modelEnd,
      modelCostSource.value,
    )
    models.value = modelStatsSelection.models
    modelStatsExactWindowFallback.value = modelStatsSelection.usedCompatibilityAggregation
    modelStatsCompatibilityTruncated.value = modelStatsSelection.compatibilityTruncated
    if (modelResult.status === 'fulfilled') {
      setSourceState('models', models.value.length ? (modelStatsSelection.usedCompatibilityAggregation ? 'partial' : 'measured') : 'empty', modelStatsSelection.usedCompatibilityAggregation ? '已使用 usage_logs 兼容聚合' : models.value.length ? '模型成本窗口已读取' : '窗口内没有模型成本记录')
    } else if (modelStatsSelection.usedCompatibilityAggregation) {
      setSourceState('models', 'partial', '模型快照不可用，已使用完整 usage_logs 兼容聚合')
    } else {
      setSourceState('models', 'unavailable', rejectedReason(modelResult, '模型成本统计读取失败'))
    }
    if (modelRoutesResult.status === 'fulfilled') {
      const channels: Channel[] = channelsResult.status === 'fulfilled' ? channelsResult.value.items ?? [] : []
      modelAuditSummary.value = summarizeModelAudit(modelRoutesResult.value.logs)
      modelRoutes.value = buildModelRouteRows(modelRoutesResult.value.logs, channels)
      modelRoutesTruncated.value = modelRoutesResult.value.truncated
      const actualModels = [...new Set(modelRoutes.value.map((row) => row.upstreamModel))].slice(0, MAX_MODEL_PRICING_LOOKUPS)
      const pricingResults = await Promise.allSettled(actualModels.map((model) => adminAPI.channels.getModelDefaultPricing(model)))
      if (sequence !== requestSequence) return
      modelPricing.value = Object.fromEntries(actualModels.flatMap((model, index) => {
        const result = pricingResults[index]
        return result.status === 'fulfilled' ? [[model, result.value]] : []
      }))
      setSourceState('modelRoutes', modelRoutes.value.length ? (modelRoutesResult.value.truncated ? 'partial' : 'measured') : 'empty', modelRoutesResult.value.truncated ? '路由记录超过安全读取上限，仅显示完整可读部分' : modelRoutes.value.length ? '模型路由审计已读取' : '窗口内没有路由记录')
    } else {
      modelAuditSummary.value = summarizeModelAudit([])
      modelRoutes.value = []
      modelRoutesTruncated.value = false
      modelPricing.value = {}
      setSourceState('modelRoutes', 'unavailable', rejectedReason(modelRoutesResult, '模型路由审计读取失败'))
    }
    pricingStatus.value = pricingStatusResult.status === 'fulfilled' ? pricingStatusResult.value : null
    setSourceState('pricing', pricingStatusResult.status === 'fulfilled' ? 'measured' : 'unavailable', pricingStatusResult.status === 'fulfilled' ? '价格目录状态已读取' : rejectedReason(pricingStatusResult, '价格目录状态读取失败'))

    if (opsResult.status === 'fulfilled') {
      opsOverview.value = opsResult.value.overview
      opsTrend.value = opsResult.value.throughput_trend?.points ?? []
      opsErrorTrend.value = opsResult.value.error_trend?.points ?? []
      setSourceState(
        'ops',
        opsTrend.value.length ? 'measured' : 'empty',
        opsTrend.value.length ? '运行质量监控已读取' : '所选窗口没有可绘制的运行样本',
      )
    } else {
      // Ops monitoring is feature-gated. The cost console remains useful without it.
      opsOverview.value = null
      opsTrend.value = []
      opsErrorTrend.value = []
      setSourceState('ops', 'unavailable', rejectedReason(opsResult, '运行质量监控未启用或读取失败'))
    }

    systemSettings.value = settingsResult.status === 'fulfilled' ? settingsResult.value : null
    setSourceState('settings', settingsResult.status === 'fulfilled' ? 'measured' : 'unavailable', settingsResult.status === 'fulfilled' ? '调度设置已读取' : rejectedReason(settingsResult, '调度设置读取失败'))
    if (exchangeRateResult.status === 'fulfilled') {
      exchangeRate.value = exchangeRateResult.value
      setSourceState('exchangeRate', exchangeRate.value.source === 'network' ? 'measured' : 'estimated', exchangeRate.value.source === 'network' ? '网络参考汇率' : exchangeRate.value.source === 'cache' ? '使用 12 小时缓存汇率' : '使用离线回退汇率')
    } else {
      setSourceState('exchangeRate', 'estimated', rejectedReason(exchangeRateResult, '汇率读取失败，使用离线回退值'))
    }

    await loadAccountEconomics('all', range)

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

  async function loadAccountEconomics(platform: string, range: CostCenterRange = DEFAULT_COST_CENTER_RANGE, accountIds: number[] = []) {
    const sequence = ++economicsRequestSequence
    accountEconomics.value = null
    setSourceState('economics', 'loading', '正在采集并读取经济样本')
    try {
      const snapshot = await adminAPI.accounts.getEconomicsSnapshot({
        scope: platform,
        platform: economicsPlatformFilter(platform),
        account_ids: accountIds.length ? accountIds.join(',') : undefined,
        cny_per_usd: exchangeRate.value.rate,
        exchange_rate_source: exchangeRate.value.source,
        window_hours: economicsWindowHours(range),
      })
      if (sequence === economicsRequestSequence) {
        accountEconomics.value = snapshot
        const status = snapshot.data_quality.status === 'partial'
          ? 'partial'
          : snapshot.projection.confidence === 'unavailable'
            ? 'partial'
            : 'measured'
        setSourceState('economics', status, snapshot.projection.confidence === 'unavailable' ? '事实数据可用；稳定采样区间不足，预测不可用' : '事实账本与稳定区间预测已读取')
      }
      return snapshot
    } catch (economicsError) {
      console.warn('[cost-center] persistent economics snapshot unavailable', economicsError)
      if (sequence === economicsRequestSequence) {
        accountEconomics.value = null
        setSourceState('economics', 'unavailable', economicsError instanceof Error ? economicsError.message : '经济采样接口读取失败')
      }
      return null
    }
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
    costLossStates,
    accountEconomics,
    accountUsage,
    todayStats,
    stats,
    trend,
    trendUsesAccountCost,
    models,
    modelCostSource,
    modelCostAccountId,
    modelCostRange,
    modelAuditMismatchOnly,
    modelAuditSummary,
    modelRoutes,
    modelRoutesTruncated,
    modelStatsExactWindowFallback,
    modelStatsCompatibilityTruncated,
    modelPricing,
    pricingStatus,
    pricingRefreshing,
    opsOverview,
    opsTrend,
    opsErrorTrend,
    systemSettings,
    probes,
    sourceStates,
    loading,
    saving,
    error,
    lastUpdated,
    exchangeRate,
    accountStats,
    reload,
    loadAccountEconomics,
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

export function accountUsageSource(account: Pick<Account, 'platform' | 'type'>): 'passive' | 'active' {
  return account.platform === 'anthropic' && (account.type === 'oauth' || account.type === 'setup-token')
    ? 'passive'
    : 'active'
}
