export type DataAvailability =
  | 'loading'
  | 'measured'
  | 'estimated'
  | 'empty'
  | 'partial'
  | 'unavailable'
  | 'stale'

export type CostCenterSourceKey =
  | 'accounts'
  | 'costLoss'
  | 'dashboard'
  | 'todayStats'
  | 'accountUsage'
  | 'models'
  | 'modelRoutes'
  | 'pricing'
  | 'ops'
  | 'settings'
  | 'exchangeRate'
  | 'economics'

export interface DataSourceState {
  key: CostCenterSourceKey
  label: string
  status: DataAvailability
  reason: string
  updatedAt: string | null
}

const SOURCE_LABELS: Record<CostCenterSourceKey, string> = {
  accounts: '账号清单与调度状态',
  costLoss: '封禁损失账本',
  dashboard: '使用记录与成本趋势',
  todayStats: '账号当日用量',
  accountUsage: '上游用量窗口',
  models: '模型成本统计',
  modelRoutes: '模型路由审计',
  pricing: '模型价格目录',
  ops: '运行质量监控',
  settings: '调度设置',
  exchangeRate: '美元兑人民币汇率',
  economics: '经济采样与预测',
}

export const COST_CENTER_SOURCE_KEYS = Object.keys(SOURCE_LABELS) as CostCenterSourceKey[]

export function createDataSourceStates(status: DataAvailability = 'loading'): Record<CostCenterSourceKey, DataSourceState> {
  return Object.fromEntries(COST_CENTER_SOURCE_KEYS.map((key) => [key, {
    key,
    label: SOURCE_LABELS[key],
    status,
    reason: status === 'loading' ? '正在读取' : '',
    updatedAt: null,
  }])) as Record<CostCenterSourceKey, DataSourceState>
}

export function sourceState(
  key: CostCenterSourceKey,
  status: DataAvailability,
  reason = '',
  updatedAt: Date | null = status === 'loading' ? null : new Date(),
): DataSourceState {
  return {
    key,
    label: SOURCE_LABELS[key],
    status,
    reason,
    updatedAt: updatedAt?.toISOString() ?? null,
  }
}

export function hasUsableData(state: DataSourceState | undefined): boolean {
  return state != null && ['measured', 'estimated', 'empty', 'partial', 'stale'].includes(state.status)
}

export function hasMeasuredData(state: DataSourceState | undefined): boolean {
  return state != null && ['measured', 'empty', 'partial', 'stale'].includes(state.status)
}

export function dataAvailabilityLabel(status: DataAvailability): string {
  return ({
    loading: '读取中',
    measured: '实测',
    estimated: '估算',
    empty: '无记录',
    partial: '部分可用',
    unavailable: '不可用',
    stale: '旧数据',
  })[status]
}

export function finiteOrNull(value: unknown): number | null {
  if (value == null || value === '') return null
  const number = Number(value)
  return Number.isFinite(number) ? number : null
}

export function unavailableValueLabel(state?: DataSourceState): string {
  if (!state) return '无数据'
  if (state.status === 'loading') return '读取中'
  if (state.status === 'empty') return '无记录'
  if (state.status === 'stale') return '旧数据'
  return '无数据'
}
