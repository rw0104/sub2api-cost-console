import { beforeEach, describe, expect, it, vi } from 'vitest'

describe('desktop backend origin sync', () => {
  beforeEach(() => {
    vi.resetModules()
    localStorage.clear()
    ;(window as any).__TAURI_INTERNALS__ = {}
  })

  it('keeps the default origin without requesting a reload', async () => {
    const url = await import('../url')
    expect(url.syncDesktopBackendOrigin('http://127.0.0.1:18765')).toBe(false)
    expect(localStorage.getItem('sub2api.desktop.backendUrl')).toBeNull()
    expect(url.getDesktopGatewayBase()).toBe('http://127.0.0.1:18765/v1')
  })

  it('stores a changed origin once and uses it after the reload', async () => {
    const before = await import('../url')
    expect(before.syncDesktopBackendOrigin('http://127.0.0.1:28000/')).toBe(true)
    expect(localStorage.getItem('sub2api.desktop.backendUrl')).toBe('http://127.0.0.1:28000/api/v1')

    vi.resetModules()
    const after = await import('../url')
    expect(after.getAPIBaseURL()).toBe('http://127.0.0.1:28000/api/v1')
    expect(after.getDesktopGatewayBase()).toBe('http://127.0.0.1:28000/v1')
    expect(after.syncDesktopBackendOrigin('http://127.0.0.1:28000')).toBe(false)
  })
})
