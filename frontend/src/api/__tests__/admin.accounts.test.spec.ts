import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('@/api/client', () => ({
  apiClient: { post },
}))

import { parseAccountTestSse, testAccount } from '@/api/admin/accounts'

describe('admin account connectivity probe', () => {
  beforeEach(() => post.mockReset())

  it('normalizes a successful SSE completion event', () => {
    const result = parseAccountTestSse(
      'data: {"type":"test_start"}\n\ndata: {"type":"content","text":"upstream ok"}\n\ndata: {"type":"test_complete","success":true}\n\n',
      84,
    )

    expect(result).toEqual({ success: true, message: 'upstream ok', latency_ms: 84 })
  })

  it('preserves terminal errors from the SSE stream', () => {
    const result = parseAccountTestSse(
      'data: {"type":"error","error":"API returned 401"}\n\n',
      120,
    )

    expect(result).toEqual({ success: false, message: 'API returned 401', latency_ms: 120 })
  })

  it('requests the streaming endpoint as text and returns a measured latency', async () => {
    post.mockResolvedValueOnce({
      data: 'data: {"type":"test_complete","success":true}\n\n',
    })

    await expect(testAccount(7)).resolves.toMatchObject({ success: true })
    expect(post).toHaveBeenCalledWith('/admin/accounts/7/test', undefined, expect.objectContaining({
      responseType: 'text',
      timeout: 60000,
    }))
  })
})
