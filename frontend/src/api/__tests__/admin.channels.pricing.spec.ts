import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, post },
}))

import { getPricingStatus, refreshPricing } from '@/api/admin/channels'

const status = {
  model_count: 12,
  last_updated: '2026-09-24T08:00:00Z',
  local_hash: '01234567',
  catalog_source: 'https://example.test/model_pricing.json',
  configured_source: 'https://example.test/model_pricing.json',
  fallback_available: true,
  update_interval_hours: 24,
}

describe('admin pricing catalog API contract', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
  })

  it('reads the dedicated status endpoint', async () => {
    get.mockResolvedValueOnce({ data: status })

    await expect(getPricingStatus()).resolves.toEqual(status)
    expect(get).toHaveBeenCalledWith('/admin/channels/pricing/status')
  })

  it('refreshes through the dedicated POST endpoint and returns the snapshot', async () => {
    post.mockResolvedValueOnce({ data: status })

    await expect(refreshPricing()).resolves.toEqual(status)
    expect(post).toHaveBeenCalledWith('/admin/channels/pricing/refresh')
  })
})

