import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, disposePinia, setActivePinia, type Pinia } from 'pinia'
import type { AxiosInstance, InternalAxiosRequestConfig } from 'axios'
import type { User } from '@/types'

describe('desktop session invalidation', () => {
  let pinia: Pinia
  let client: AxiosInstance
  let auth: ReturnType<typeof import('@/stores/auth')['useAuthStore']>
  let urls: typeof import('@/api/url')
  const user = (id = 1) => ({ id, role: 'admin', username: `fixture-${id}` }) as User

  function seed(token = 'expired-access', id = 1) {
    auth.token = token
    auth.user = user(id)
    localStorage.setItem('auth_token', token)
    localStorage.setItem('auth_user', JSON.stringify(user(id)))
  }
  function unauthorized(token = 'expired-access', retried = false, url = '/admin/dashboard/snapshot-v2') {
    return {
      config: { url, _retry: retried, headers: { Authorization: `Bearer ${token}` } },
      response: { status: 401, data: { code: 'TOKEN_EXPIRED', message: 'fixture expired' } },
    }
  }
  function rejectResponse(error: unknown) {
    const interceptors = client.interceptors.response as unknown as { handlers: { rejected: (error: unknown) => Promise<unknown> }[] }
    return interceptors.handlers[0].rejected(error)
  }
  beforeEach(async () => {
    vi.resetModules()
    localStorage.clear()
    sessionStorage.clear()
    window.history.replaceState({}, '', '/index.html#/admin/dashboard')
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true })
    pinia = createPinia()
    setActivePinia(pinia)
    auth = (await import('@/stores/auth')).useAuthStore()
    client = (await import('@/api/client')).apiClient
    urls = await import('@/api/url')
    vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
  })
  afterEach(() => {
    disposePinia(pinia)
    Reflect.deleteProperty(window, '__TAURI_INTERNALS__')
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('clears real Pinia state before the real route guard processes the login redirect', async () => {
    seed()
    const navigate = urls.redirectToAppPath
    vi.spyOn(urls, 'redirectToAppPath').mockImplementation(path => {
      expect(auth.isAuthenticated).toBe(false)
      navigate(path)
    })
    await rejectResponse(unauthorized()).catch(() => undefined)
    expect(auth.isAuthenticated).toBe(false)
    expect(auth.user).toBeNull()
    expect(localStorage.getItem('auth_token')).toBeNull()
    expect(window.location.hash).toBe('#/login')
    await (await import('@/i18n')).initI18n()
    const router = (await import('@/router')).default
    // Keep the production guards; page rendering is outside this session test.
    for (const route of router.getRoutes()) route.components = { default: { render: () => null } }
    await router.push('/login')
    expect(router.currentRoute.value.path).toBe('/login')
    router.options.history.destroy()
  })

  it('coalesces concurrent 401 responses into one login navigation', async () => {
    seed()
    const redirect = vi.spyOn(urls, 'redirectToAppPath')
    await Promise.allSettled(Array.from({ length: 20 }, () => rejectResponse(unauthorized())))
    expect(auth.isAuthenticated).toBe(false)
    expect(redirect).toHaveBeenCalledTimes(1)
    expect(sessionStorage.getItem('auth_expired')).toBe('1')
    // The login page consumes this notice; late responses must not recreate it.
    sessionStorage.removeItem('auth_expired')
    await rejectResponse(unauthorized()).catch(() => undefined)
    expect(sessionStorage.getItem('auth_expired')).toBeNull()
  })

  it('ends the session if the replay with a refreshed token also receives 401', async () => {
    seed('refreshed-access')
    localStorage.setItem('refresh_token', 'rotated-refresh')
    await rejectResponse(unauthorized('refreshed-access', true)).catch(() => undefined)
    expect(auth.isAuthenticated).toBe(false)
    expect(localStorage.getItem('refresh_token')).toBeNull()
  })

  it('does not erase a newly signed-in user when an old request fails', async () => {
    seed('new-user-access', 2)
    await rejectResponse(unauthorized('old-user-access')).catch(() => undefined)
    expect(auth.isAuthenticated).toBe(true)
    expect(auth.user?.id).toBe(2)
    expect(localStorage.getItem('auth_token')).toBe('new-user-access')
    expect(window.location.hash).toBe('#/admin/dashboard')
  })

  it('does not navigate again when already on the desktop login route with a query', async () => {
    seed()
    window.history.replaceState({}, '', '/index.html#/login?redirect=%2Fadmin%2Fdashboard')
    const redirect = vi.spyOn(urls, 'redirectToAppPath')
    await rejectResponse(unauthorized()).catch(() => undefined)
    expect(redirect).not.toHaveBeenCalled()
    expect(auth.isAuthenticated).toBe(false)
  })

  it('does not let a late user-info response resurrect an invalidated session', async () => {
    seed()
    let complete!: (value: unknown) => void
    let requestConfig!: InternalAxiosRequestConfig
    client.defaults.adapter = config => { requestConfig = config; return new Promise(resolve => { complete = resolve }) }
    const pendingUser = auth.refreshUser()
    const outcome = expect(pendingUser).rejects.toMatchObject({ code: 'AUTH_SESSION_CHANGED' })
    await vi.waitFor(() => expect(complete).toBeDefined())
    await rejectResponse(unauthorized()).catch(() => undefined)
    complete({ status: 200, data: { code: 0, data: user() }, config: requestConfig, headers: {}, statusText: 'OK' })
    await outcome
    expect(auth.user).toBeNull()
    expect(localStorage.getItem('auth_user')).toBeNull()
  })

  it('keeps failed login credentials on the login flow rather than reporting an expired session', async () => {
    window.history.replaceState({}, '', '/index.html#/login')
    const redirect = vi.spyOn(urls, 'redirectToAppPath')
    await rejectResponse(unauthorized('', false, '/auth/login')).catch(() => undefined)
    expect(redirect).not.toHaveBeenCalled()
    expect(sessionStorage.getItem('auth_expired')).toBeNull()
  })

  it('refreshes only once and clears memory if the replay is rejected', async () => {
    seed()
    localStorage.setItem('refresh_token', 'refresh-before')
    localStorage.setItem('token_expires_at', String(Date.now() - 1))
    const axios = (await import('axios')).default
    const refresh = vi.spyOn(axios, 'post').mockResolvedValue({ data: { code: 0, data: {
      access_token: 'refreshed-access', refresh_token: 'refresh-after', expires_in: 3600, token_type: 'Bearer',
    } } })
    const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
      throw { config, response: { status: 401, data: { code: 'INVALID_TOKEN' } } }
    })
    client.defaults.adapter = adapter
    await expect(client.get('/admin/dashboard/snapshot-v2')).rejects.toMatchObject({ status: 401 })
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(adapter).toHaveBeenCalledTimes(2)
    expect(auth.isAuthenticated).toBe(false)
  })

  it('does not replay an old-user request using a new users refresh credentials', async () => {
    seed('old-user-access', 1)
    let reject!: (error: unknown) => void
    let config!: InternalAxiosRequestConfig
    client.defaults.adapter = request => { config = request; return new Promise((_resolve, fail) => { reject = fail }) }
    const pending = client.get('/admin/dashboard/snapshot-v2')
    const outcome = expect(pending).rejects.toMatchObject({ code: 'AUTH_SESSION_CHANGED' })
    await vi.waitFor(() => expect(reject).toBeDefined())
    seed('new-user-access', 2)
    localStorage.setItem('refresh_token', 'new-user-refresh')
    const refresh = vi.spyOn((await import('axios')).default, 'post')
    reject({ config, response: { status: 401, data: { code: 'TOKEN_EXPIRED' } } })
    await outcome
    expect(refresh).not.toHaveBeenCalled()
    expect(auth.user?.id).toBe(2)
    expect(localStorage.getItem('refresh_token')).toBe('new-user-refresh')
  })

  it('permits a later session to expire after a successful login in the same document', async () => {
    seed()
    const redirect = vi.spyOn(urls, 'redirectToAppPath')
    await rejectResponse(unauthorized()).catch(() => undefined)
    seed('second-session-access')
    window.history.replaceState({}, '', '/index.html#/admin/dashboard')
    await rejectResponse(unauthorized('second-session-access')).catch(() => undefined)
    expect(redirect).toHaveBeenCalledTimes(2)
    expect(auth.isAuthenticated).toBe(false)
  })

  it('does not replay if another login wins immediately after a refresh succeeds', async () => {
    seed('old-user-access', 1)
    localStorage.setItem('refresh_token', 'old-refresh')
    const refresh = await import('@/api/tokenRefresh')
    vi.spyOn(refresh, 'refreshAuthTokens').mockImplementation(async () => {
      seed('new-user-access', 2)
      localStorage.setItem('refresh_token', 'new-user-refresh')
      return { access_token: 'refreshed-old-user', refresh_token: 'rotated-old-refresh', expires_in: 3600, token_type: 'Bearer' }
    })
    const adapter = vi.fn()
    client.defaults.adapter = adapter
    await expect(rejectResponse(unauthorized('old-user-access'))).rejects.toMatchObject({ code: 'AUTH_SESSION_CHANGED' })
    expect(adapter).not.toHaveBeenCalled()
    expect(auth.user?.id).toBe(2)
    expect(localStorage.getItem('auth_token')).toBe('new-user-access')
  })

  it.each([undefined, 429, 503])('preserves the session when refreshing is temporarily unavailable (%s)', async status => {
    seed()
    localStorage.setItem('refresh_token', 'still-valid-refresh')
    const refresh = await import('@/api/tokenRefresh')
    vi.spyOn(refresh, 'refreshAuthTokens').mockRejectedValue({ response: { status } })
    await expect(rejectResponse(unauthorized())).rejects.toMatchObject({ code: 'TOKEN_REFRESH_UNAVAILABLE' })
    expect(auth.isAuthenticated).toBe(true)
    expect(localStorage.getItem('refresh_token')).toBe('still-valid-refresh')
    expect(window.location.hash).toBe('#/admin/dashboard')
  })

  it('also clears the session and its timers when proactive refresh receives 401', async () => {
    vi.useFakeTimers()
    const { authAPI } = await import('@/api')
    vi.spyOn(authAPI, 'login').mockResolvedValue({
      access_token: 'expired-access', refresh_token: 'refresh', expires_in: 121,
      token_type: 'Bearer', user: user(),
    })
    const refresh = vi.spyOn(authAPI, 'refreshToken').mockRejectedValue({ response: { status: 401 } })
    const userRefresh = vi.spyOn(authAPI, 'getCurrentUser')
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    await auth.login({ email: 'fixture@example.invalid', password: 'fixture-only' })
    await vi.advanceTimersByTimeAsync(1000)
    expect(auth.isAuthenticated).toBe(false)
    expect(window.location.hash).toBe('#/login')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(userRefresh).not.toHaveBeenCalled()
  })

  it('can redirect a new session even if its tokens were removed by another document', async () => {
    seed()
    await rejectResponse(unauthorized()).catch(() => undefined)
    const { authAPI } = await import('@/api')
    vi.spyOn(authAPI, 'login').mockResolvedValue({
      access_token: 'next-access', refresh_token: 'next-refresh', expires_in: 3600,
      token_type: 'Bearer', user: user(2),
    })
    await auth.login({ email: 'fixture@example.invalid', password: 'fixture-only' })
    window.history.replaceState({}, '', '/index.html#/admin/dashboard')
    localStorage.clear()
    await rejectResponse(unauthorized('')).catch(() => undefined)
    expect(auth.isAuthenticated).toBe(false)
    expect(window.location.hash).toBe('#/login')
  })
})
