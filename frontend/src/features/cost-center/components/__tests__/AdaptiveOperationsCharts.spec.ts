import { shallowMount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { OpsThroughputTrendPoint } from '@/api/admin/ops'
import AdaptiveOperationsCharts from '../AdaptiveOperationsCharts.vue'
import CostLineChart from '../CostLineChart.vue'

function point(overrides: Partial<OpsThroughputTrendPoint> = {}): OpsThroughputTrendPoint {
  return {
    bucket_start: '2026-08-11T12:00:00.000Z',
    request_count: 1,
    success_count: 1,
    error_count: 0,
    token_consumed: 15,
    input_tokens: 10,
    output_tokens: 5,
    cache_creation_tokens: 0,
    cache_read_tokens: 0,
    switch_count: 0,
    user_billed_usd: 1,
    account_cost_usd: 0.25,
    contribution_usd: 0.75,
    duration_p95_ms: null,
    ttft_p50_ms: null,
    ttft_p95_ms: null,
    qps: 0.2,
    tps: 3,
    ...overrides,
  }
}

function mountChart(opsTrend: OpsThroughputTrendPoint[]) {
  return shallowMount(AdaptiveOperationsCharts, {
    props: {
      opsTrend,
      financialTrend: opsTrend.map((item) => ({
        timestamp: item.bucket_start,
        billedUsd: item.user_billed_usd,
        accountCostUsd: item.account_cost_usd,
        contributionUsd: item.contribution_usd,
        bucketHours: 5 / 3600,
        source: 'ops' as const,
      })),
      errorTrend: [],
      economics: null,
      cnyPerUsd: 7,
      opsBucketHours: 5 / 3600,
      procurementHourlyCny: 0,
      opsState: 'measured',
      opsReason: '',
      healthState: 'empty',
      healthReason: '没有健康样本',
    },
  })
}

describe('AdaptiveOperationsCharts truthful rates', () => {
  it('uses the known bucket width when a window contains only one point', () => {
    const economyChart = mountChart([point()]).findAllComponents(CostLineChart)[0]
    const series = economyChart.props('series') as Array<{ data: Array<number | null> }>

    expect(series[0].data[0]).toBeCloseTo(5040)
  })

  it('keeps a null fact as a chart gap instead of converting it to zero', () => {
    const economyChart = mountChart([point({ user_billed_usd: null as unknown as number })]).findAllComponents(CostLineChart)[0]
    const series = economyChart.props('series') as Array<{ data: Array<number | null> }>

    expect(series[0].data[0]).toBeNull()
  })
})
