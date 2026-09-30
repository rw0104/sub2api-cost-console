import { beforeEach, describe, expect, it, vi } from 'vitest'
import { inspect, saveConfig, upload, upgrade } from '../plugins'

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

  it('inspection does not send a trust grant', async () => {
    const file = new File(['signed-package'], 'example.s2plugin')
    await inspect(file)
    const request = adapter.mock.calls[0][0]
    expect(request.url).toBe('/admin/plugins/inspect')
    expect(request.data.get('plugin')).toBe(file)
    expect(request.data.has('trust_publisher')).toBe(false)
  })

  it('explicit publisher approval stays bound to the reviewed package', async () => {
    const file = new File(['signed-package'], 'example.s2plugin')
    const approval = { package_sha256: 'a'.repeat(64), publisher_fingerprint: `sha256:${'b'.repeat(64)}` }
    await upload(file, approval)
    const form = adapter.mock.calls[0][0].data as FormData
    expect(form.get('trust_publisher')).toBe('true')
    expect(form.get('package_sha256')).toBe(approval.package_sha256)
    expect(form.get('publisher_fingerprint')).toBe(approval.publisher_fingerprint)
  })
})

describe('plugin config save revision headers', () => {
  beforeEach(() => {
    adapter.mockReset()
    adapter.mockImplementation(async config => ({ data: {}, status: 200, statusText: 'OK', headers: {}, config }))
  })
  it('以 X-Plugin-Revision 为主通道并保留 If-Match 回退，同时解析写入响应头', async () => {
    adapter.mockImplementationOnce(async config => ({
      data: { enabled: true }, status: 200, statusText: 'OK',
      headers: { 'x-plugin-revision': '5', etag: 'plugin-7-5', 'x-plugin-operation-id': 'op-1' },
      config,
    }))
    const result = await saveConfig(7, { enabled: true }, 4)
    const request = adapter.mock.calls[0][0]
    expect(request.url).toBe('/admin/plugins/7/config')
    expect(request.headers.get('X-Plugin-Revision')).toBe('4')
    expect(request.headers.get('If-Match')).toBe('4')
    expect(result).toEqual({ config: { enabled: true }, revision: 5, etag: 'plugin-7-5', operationId: 'op-1' })
  })
  it('未提供 expectedRevision 时不带 revision 相关请求头', async () => {
    await saveConfig(7, { enabled: false })
    const request = adapter.mock.calls[0][0]
    expect(request.headers.get('X-Plugin-Revision')).toBeFalsy()
    expect(request.headers.get('If-Match')).toBeFalsy()
  })
  it('非正数或非法的 revision 响应头按缺失处理', async () => {
    adapter.mockImplementationOnce(async config => ({ data: {}, status: 200, statusText: 'OK', headers: { 'x-plugin-revision': '0' }, config }))
    expect((await saveConfig(7, {}, 1)).revision).toBeUndefined()
    adapter.mockImplementationOnce(async config => ({ data: {}, status: 200, statusText: 'OK', headers: { 'x-plugin-revision': 'not-a-number' }, config }))
    expect((await saveConfig(7, {}, 1)).revision).toBeUndefined()
  })
})
