import { beforeEach, describe, expect, it, vi } from 'vitest'
import { upload, upgrade } from '../plugins'

const { adapter } = vi.hoisted(() => ({ adapter: vi.fn() }))
vi.mock('@/api/client', async () => {
  const { default: axios } = await import('axios')
  return { apiClient: axios.create({ headers: { 'Content-Type': 'application/json' }, adapter }) }
})

describe('plugin package multipart encoding', () => {
  beforeEach(() => {
    adapter.mockReset()
    adapter.mockImplementation(async config => ({ data: {}, status: 200, statusText: 'OK', headers: {}, config }))
  })

  for (const operation of ['install', 'upgrade'] as const) {
    it(`${operation} preserves the file through the real Axios transform`, async () => {
      const file = new File(['signed-package'], 'example.s2plugin', { type: 'application/zip' })
      if (operation === 'install') await upload(file)
      else await upgrade(7, file, true)
      const request = adapter.mock.calls[0][0]
      expect(request.data).toBeInstanceOf(FormData)
      expect(request.data.get('plugin')).toBe(file)
      expect(request.headers.getContentType()).toContain('multipart/form-data')
      if (operation === 'upgrade') expect(request.data.get('accept_untested')).toBe('true')
    })
  }
})
