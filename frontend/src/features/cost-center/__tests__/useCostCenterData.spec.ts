import { describe, expect, it, vi } from 'vitest'

const { testAccount } = vi.hoisted(() => ({ testAccount: vi.fn() }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: { testAccount },
    dashboard: {},
    ops: {},
  },
}))

import { buildCostCenterSnapshotQuery, useCostCenterData } from '../useCostCenterData'

describe('cost center live ranges', () => {
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
