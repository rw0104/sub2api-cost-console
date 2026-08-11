<template>
  <section class="adaptive-charts" aria-labelledby="adaptive-charts-title">
    <div class="adaptive-charts__heading">
      <div>
        <span>ADAPTIVE TIME SERIES</span>
        <h2 id="adaptive-charts-title">经营与运行趋势</h2>
        <p>每个窗口自动控制采样密度；实线为事实，虚线为当前配置推算，断点表示不可确认区间。</p>
      </div>
      <strong>{{ pointSummary }}</strong>
    </div>

    <div class="adaptive-charts__grid">
      <article class="adaptive-chart-card">
        <header>
          <div><strong>{{ economyTitle }}</strong><span>{{ economySubtitle }}</span></div>
          <select v-model="economyMode" aria-label="经济指标">
            <option value="flow">收支流量</option>
            <option value="unit">单位采购成本</option>
            <option value="return">回本与毛利率</option>
          </select>
        </header>
        <CostLineChart
          :labels="opsLabels"
          :series="economySeries"
          :events="opsEvents"
          :value-prefix="economyMode === 'return' ? '' : '¥'"
          :value-suffix="economyMode === 'unit' ? '/USD' : economyMode === 'return' ? '%' : ''"
          :state="opsState"
          :state-reason="opsReason"
        />
      </article>

      <article class="adaptive-chart-card">
        <header>
          <div><strong>请求质量</strong><span>成功、失败与上游错误码</span></div>
          <select v-model="qualityMode" aria-label="请求质量指标">
            <option value="outcome">请求结果</option>
            <option value="status">429 / 402 / 其他</option>
          </select>
        </header>
        <CostLineChart
          :labels="opsLabels"
          :series="qualitySeries"
          :events="opsEvents"
          :state="opsState"
          :state-reason="opsReason"
        />
      </article>

      <article class="adaptive-chart-card">
        <header><div><strong>账号健康</strong><span>持久经济样本中的状态分布</span></div></header>
        <CostLineChart
          :labels="healthLabels"
          :series="healthSeries"
          :events="healthEvents"
          :state="healthState"
          :state-reason="healthReason"
        />
      </article>

      <article class="adaptive-chart-card">
        <header>
          <div><strong>延迟与 Token 结构</strong><span>空延迟样本保持断点</span></div>
          <select v-model="experienceMode" aria-label="体验指标">
            <option value="latency">TTFT / 总耗时</option>
            <option value="tokens">输入 / 输出 / 缓存写入</option>
            <option value="cache">缓存读取 / 命中率</option>
          </select>
        </header>
        <CostLineChart
          :labels="opsLabels"
          :series="experienceSeries"
          :value-suffix="experienceMode === 'latency' ? 'ms' : experienceMode === 'cache' ? '%' : ''"
          :events="opsEvents"
          :state="opsState"
          :state-reason="opsReason"
        />
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { AccountEconomicsSnapshot } from '@/api/admin/accounts'
import type { OpsErrorTrendPoint, OpsThroughputTrendPoint } from '@/api/admin/ops'
import type { DataAvailability } from '../dataState'
import CostLineChart, { type CostChartEvent, type CostChartSeries } from './CostLineChart.vue'

const props = defineProps<{
  opsTrend: OpsThroughputTrendPoint[]
  errorTrend: OpsErrorTrendPoint[]
  economics: AccountEconomicsSnapshot | null
  cnyPerUsd: number
  opsBucketHours: number
  procurementHourlyCny: number | null
  opsState: DataAvailability
  opsReason: string
  healthState: DataAvailability
  healthReason: string
}>()

const economyMode = ref<'flow' | 'unit' | 'return'>('flow')
const qualityMode = ref<'outcome' | 'status'>('outcome')
const experienceMode = ref<'latency' | 'tokens' | 'cache'>('latency')

function timeLabel(value: string): string {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '—'
  return date.toLocaleString(undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function rateFactor(points: OpsThroughputTrendPoint[], index: number): number {
  const current = new Date(points[index]?.bucket_start || '').getTime()
  const neighbor = new Date(points[index > 0 ? index - 1 : index + 1]?.bucket_start || '').getTime()
  const seconds = Math.abs(current - neighbor) / 1000
  if (Number.isFinite(seconds) && seconds > 0) return 3600 / seconds
  return Number.isFinite(props.opsBucketHours) && props.opsBucketHours > 0 ? 1 / props.opsBucketHours : 1
}

function finiteValue(value: unknown): number | null {
  if (value == null || value === '') return null
  const number = Number(value)
  return Number.isFinite(number) ? number : null
}

function hourlyValue(points: OpsThroughputTrendPoint[], index: number, value: unknown): number | null {
  const number = finiteValue(value)
  return number == null ? null : number * rateFactor(points, index)
}

const opsLabels = computed(() => props.opsTrend.map((point) => timeLabel(point.bucket_start)))
const healthLabels = computed(() => (props.economics?.series ?? []).map((point) => timeLabel(point.sampled_at)))
const pointSummary = computed(() => `${props.opsTrend.length || 0} 个运行点 · ${props.economics?.series?.length || 0} 个健康点`)
const economyTitle = computed(() => economyMode.value === 'flow' ? '收入、成本与贡献' : economyMode.value === 'unit' ? '每 1 USD 产出的采购成本' : '回本与毛利率')
const economySubtitle = computed(() => economyMode.value === 'flow' ? '统一换算为 CNY / 小时' : economyMode.value === 'unit' ? '固定采购费率 ÷ 实际用户计费产出' : '固定采购回本率与扣除采购后的贡献毛利率')

const economySeries = computed<CostChartSeries[]>(() => {
  const procurement = props.procurementHourlyCny == null ? null : Math.max(0, props.procurementHourlyCny)
  if (economyMode.value === 'unit') {
    return [{
      label: '采购成本 / 产出',
      color: '#7eb6d8',
      fill: true,
      data: props.opsTrend.map((point, index) => {
        const billedPerHour = hourlyValue(props.opsTrend, index, point.user_billed_usd)
        return billedPerHour != null && billedPerHour > 0 && procurement != null ? procurement / billedPerHour : null
      }),
    }]
  }
  if (economyMode.value === 'return') {
    return [
      {
        label: '固定采购回本率',
        color: '#e0bd4e',
        data: props.opsTrend.map((point, index) => {
          const billedPerHour = hourlyValue(props.opsTrend, index, point.user_billed_usd)
          return billedPerHour != null && procurement != null && procurement > 0 ? billedPerHour * props.cnyPerUsd / procurement * 100 : null
        }),
      },
      {
        label: '贡献毛利率',
        color: '#b9e55a',
        fill: true,
        data: props.opsTrend.map((point, index) => {
          const billedPerHour = hourlyValue(props.opsTrend, index, point.user_billed_usd)
          const contributionPerHour = hourlyValue(props.opsTrend, index, point.contribution_usd)
          if (billedPerHour == null || billedPerHour <= 0 || contributionPerHour == null || procurement == null) return null
          return (contributionPerHour * props.cnyPerUsd - procurement) / (billedPerHour * props.cnyPerUsd) * 100
        }),
      },
    ]
  }
  return [
    { label: '用户计费', color: '#e0bd4e', fill: true, data: props.opsTrend.map((point, index) => { const value = hourlyValue(props.opsTrend, index, point.user_billed_usd); return value == null ? null : value * props.cnyPerUsd }) },
    { label: '上游账号成本', color: '#7eb6d8', data: props.opsTrend.map((point, index) => { const value = hourlyValue(props.opsTrend, index, point.account_cost_usd); return value == null ? null : value * props.cnyPerUsd }) },
    { label: '贡献毛利', color: '#b9e55a', data: props.opsTrend.map((point, index) => { const value = hourlyValue(props.opsTrend, index, point.contribution_usd); return value == null || procurement == null ? null : value * props.cnyPerUsd - procurement }) },
  ]
})

const errorsByBucket = computed(() => new Map(props.errorTrend.map((point) => [new Date(point.bucket_start).getTime(), point])))
const alignedErrors = computed(() => props.opsTrend.map((point) => errorsByBucket.value.get(new Date(point.bucket_start).getTime())))
const qualitySeries = computed<CostChartSeries[]>(() => qualityMode.value === 'outcome'
  ? [
      { label: '成功', color: '#b9e55a', fill: true, data: props.opsTrend.map((point) => finiteValue(point.success_count)) },
      { label: '失败', color: '#d88473', data: props.opsTrend.map((point) => finiteValue(point.error_count)) },
      { label: '切号事件', color: '#7eb6d8', data: props.opsTrend.map((point) => finiteValue(point.switch_count)) },
    ]
  : [
      { label: '429 限流', color: '#e0bd4e', data: alignedErrors.value.map((point) => point ? finiteValue(point.upstream_429_count) : null) },
      { label: '402 封闭空间', color: '#d88473', data: alignedErrors.value.map((point) => point ? finiteValue(point.upstream_402_count) : null) },
      { label: '其他上游错误', color: '#7eb6d8', data: alignedErrors.value.map((point) => {
        if (!point) return null
        const upstream = finiteValue(point.upstream_error_count_excl_429_529)
        const closed = finiteValue(point.upstream_402_count)
        return upstream == null || closed == null ? null : Math.max(0, upstream - closed)
      }) },
    ])

const healthSeries = computed<CostChartSeries[]>(() => [
  { label: '可调度', color: '#b9e55a', fill: true, data: (props.economics?.series ?? []).map((point) => point.normal_count) },
  { label: '429 / 限流', color: '#e0bd4e', data: (props.economics?.series ?? []).map((point) => point.rate_limited_count) },
  { label: '402 / 错误', color: '#d88473', data: (props.economics?.series ?? []).map((point) => point.error_count) },
])

const experienceSeries = computed<CostChartSeries[]>(() => {
  if (experienceMode.value === 'latency') {
    return [
      { label: 'TTFT P50', color: '#7eb6d8', data: props.opsTrend.map((point) => point.ttft_p50_ms ?? null) },
      { label: 'TTFT P95', color: '#e0bd4e', data: props.opsTrend.map((point) => point.ttft_p95_ms ?? null) },
      { label: '总耗时 P95', color: '#d88473', data: props.opsTrend.map((point) => point.duration_p95_ms ?? null) },
    ]
  }
  if (experienceMode.value === 'tokens') {
    return [
      { label: '输入 Token', color: '#7eb6d8', data: props.opsTrend.map((point) => finiteValue(point.input_tokens)) },
      { label: '输出 Token', color: '#b9e55a', data: props.opsTrend.map((point) => finiteValue(point.output_tokens)) },
      { label: '缓存写入', color: '#e0bd4e', data: props.opsTrend.map((point) => finiteValue(point.cache_creation_tokens)) },
    ]
  }
  return [
    { label: '缓存读取占比', color: '#b9e55a', fill: true, data: props.opsTrend.map((point) => {
      const input = finiteValue(point.input_tokens)
      const cacheRead = finiteValue(point.cache_read_tokens)
      if (input == null || cacheRead == null) return null
      const denominator = input + cacheRead
      return denominator > 0 ? cacheRead / denominator * 100 : 0
    }) },
  ]
})

function mapEvents(labels: string[], timestamps: string[]): CostChartEvent[] {
  if (!props.economics?.events?.length || !timestamps.length) return []
  const positions = timestamps
    .map((value, index) => ({ index, time: new Date(value).getTime() }))
    .filter((position) => Number.isFinite(position.time))
  if (!positions.length) return []
  const first = Math.min(...positions.map((position) => position.time))
  const last = Math.max(...positions.map((position) => position.time))
  return props.economics.events.flatMap((event) => {
    const target = new Date(event.occurred_at).getTime()
    if (!Number.isFinite(target) || target < first || target > last) return []
    let nearest = positions[0]
    for (const position of positions.slice(1)) {
      if (Math.abs(position.time - target) < Math.abs(nearest.time - target)) nearest = position
    }
    return [{ index: Math.min(nearest.index, labels.length - 1), label: `${timeLabel(event.occurred_at)} · ${event.label}`, severity: event.severity === 'warning' ? 'warning' : 'info' }]
  })
}

const opsEvents = computed(() => mapEvents(opsLabels.value, props.opsTrend.map((point) => point.bucket_start)))
const healthEvents = computed(() => mapEvents(healthLabels.value, (props.economics?.series ?? []).map((point) => point.sampled_at)))
</script>

<style scoped>
.adaptive-charts { margin-top: 18px; }
.adaptive-charts__heading { display: flex; align-items: end; justify-content: space-between; gap: 24px; margin-bottom: 10px; }
.adaptive-charts__heading span { color: #b9e55a; font: 9px 'Cascadia Mono', monospace; letter-spacing: .08em; }
.adaptive-charts__heading h2 { margin: 6px 0 0; font-size: 20px; }
.adaptive-charts__heading p, .adaptive-charts__heading > strong { margin: 5px 0 0; color: #768079; font-size: 10px; font-weight: 500; }
.adaptive-charts__grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); overflow: hidden; border: 1px solid #303830; border-radius: 13px; background: #111611; }
.adaptive-chart-card { min-width: 0; padding: 15px 16px 13px; border-right: 1px solid #303830; border-bottom: 1px solid #303830; }
.adaptive-chart-card:nth-child(2n) { border-right: 0; }
.adaptive-chart-card:nth-last-child(-n+2) { border-bottom: 0; }
.adaptive-chart-card header { display: flex; min-height: 38px; align-items: start; justify-content: space-between; gap: 12px; }
.adaptive-chart-card header strong, .adaptive-chart-card header span { display: block; }
.adaptive-chart-card header strong { font-size: 12px; }
.adaptive-chart-card header span { margin-top: 3px; color: #768079; font: 9px 'Cascadia Mono', monospace; }
.adaptive-chart-card select { min-height: 30px; padding: 0 26px 0 9px; border: 1px solid #39433a; border-radius: 8px; background: #171d18; color: #c7cec7; font-size: 10px; }
@media (max-width: 1180px) { .adaptive-charts__grid { grid-template-columns: 1fr; } .adaptive-chart-card, .adaptive-chart-card:nth-child(2n), .adaptive-chart-card:nth-last-child(-n+2) { border-right: 0; border-bottom: 1px solid #303830; } .adaptive-chart-card:last-child { border-bottom: 0; } }
</style>
