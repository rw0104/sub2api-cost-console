import { afterEach, describe, expect, it, vi } from 'vitest'

const { testAccount } = vi.hoisted(() => ({ testAccount: vi.fn() }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: { testAccount },
    dashboard: {},
    ops: {},
  },
}))

import {
  buildCostCenterSnapshotQuery,
  buildCostCenterDataQueries,
  DEFAULT_COST_CENTER_RANGE,
  DEFAULT_MODEL_COST_RANGE,
  filterModelAuditLogs,
  selectExactWindowModelStats,
  snapshotMatchesRequestedWindow,
  useCostCenterData,
} from '../useCostCenterData'

describe('cost center live ranges', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('defaults live observation and recent model economics to one hour', () => {
    expect(DEFAULT_COST_CENTER_RANGE).toBe('1h')
    expect(DEFAULT_MODEL_COST_RANGE).toBe('1h')
    expect(useCostCenterData().modelCostRange.value).toBe('1h')
  })

  it('requests the current local calendar day with exact bounds', () => {
    const now = new Date(2026, 7, 6, 13, 45, 30)
    vi.useFakeTimers()
    vi.setSystemTime(now)

    const start = new Date(now)
    start.setHours(0, 0, 0, 0)
    const end = new Date(start)
    end.setDate(end.getDate() + 1)

    expect(buildCostCenterSnapshotQuery('today')).toEqual({
      start_time: start.toISOString(),
      end_time: end.toISOString(),
      granularity: 'hour',
    })
  })

  it('uses minute buckets for the one minute window', () => {
    expect(buildCostCenterSnapshotQuery('1m')).toEqual({
      time_range: '1m',
      granularity: 'minute',
    })
  })

  it('uses minute buckets for the default one hour chart', () => {
    expect(buildCostCenterSnapshotQuery('1h')).toEqual({
      time_range: '1h',
      granularity: 'minute',
    })
  })

  it('keeps observation and model queries on their independent windows', () => {
    const queries = buildCostCenterDataQueries('1h', 'today')
    expect(queries.observation).toMatchObject({ time_range: '1h', granularity: 'minute' })
    expect(queries.model).toMatchObject({ granularity: 'hour' })
    expect(queries.model).not.toHaveProperty('time_range')
  })

  it('requests an exact 30 minute snapshot with minute buckets', () => {
    expect(buildCostCenterSnapshotQuery('30m')).toEqual({
      time_range: '30m',
      granularity: 'minute',
    })
  })

  it('uses day buckets for the seven day window', () => {
    expect(buildCostCenterSnapshotQuery('7d')).toEqual({
      time_range: '7d',
      granularity: 'day',
    })
  })

  it('uses day buckets for the one month window', () => {
    expect(buildCostCenterSnapshotQuery('30d')).toEqual({
      time_range: '30d',
      granularity: 'day',
    })
  })

  it('rejects a legacy snapshot that omitted the requested exact time boundary', () => {
    const start = new Date('2026-08-07T07:00:00.000Z')
    const end = new Date('2026-08-07T08:00:00.000Z')
    expect(snapshotMatchesRequestedWindow({ start_date: '2026-08-07', end_date: '2026-08-07' }, start, end)).toBe(false)
    expect(snapshotMatchesRequestedWindow({
      start_time: start.toISOString(),
      end_time: end.toISOString(),
    }, start, end)).toBe(true)
  })

  it('does not present a truncated compatibility sample as complete model cost', () => {
    const start = new Date('2026-08-07T07:00:00.000Z')
    const end = new Date('2026-08-07T08:00:00.000Z')
    const result = selectExactWindowModelStats(
      { models: [{ model: 'gpt-old', requests: 269 }] as any },
      { logs: [{ model: 'deepseek-v4-flash', created_at: '2026-08-07T07:30:00.000Z' }] as any, truncated: true },
      start,
      end,
      'upstream',
    )

    expect(result.models).toEqual([])
    expect(result.compatibilityTruncated).toBe(true)
    expect(result.usedCompatibilityAggregation).toBe(false)
  })

  it('keeps only confirmed upstream model mismatches when the audit filter is enabled', () => {
    const logs = [
      { id: 1, upstream_model_mismatch: true },
      { id: 2, upstream_model_mismatch: false },
      { id: 3, upstream_model_mismatch: null },
    ] as any

    expect(filterModelAuditLogs(logs, true).map((log) => log.id)).toEqual([1])
    expect(filterModelAuditLogs(logs, false)).toBe(logs)
  })
})

describe('cost center account probe feedback', () => {
  it('publishes visible progress before the upstream request completes', async () => {
    let resolveProbe!: (value: { success: boolean; message: string; latency_ms: number }) => void
    testAccount.mockReturnValueOnce(new Promise((resolve) => { resolveProbe = resolve }))
    const data = useCostCenterData()
    const account = { id: 9, name: 'probe@example.com' } as any

    const pending = data.probeAccount(account)

    expect(data.probes.value['9']).toMatchObject({
      loading: true,
      message: '正在请求真实上游…',
    })

    resolveProbe({ success: true, message: '连接成功', latency_ms: 86 })
    await pending
    expect(data.probes.value['9']).toMatchObject({
      loading: false,
      success: true,
      latency_ms: 86,
    })
  })
})
