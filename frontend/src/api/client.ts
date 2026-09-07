/**
 * Axios HTTP Client Configuration
 * Base client with interceptors for authentication, token refresh, and error handling
 */

import axios, { AxiosInstance, AxiosError, InternalAxiosRequestConfig, AxiosResponse } from 'axios'
import type { ApiResponse } from '@/types'
import { getLocale } from '@/i18n'
import {
  ADMIN_UI_REQUEST_HEADER,
  USER_UI_REQUEST_HEADER,
  shouldMarkAdminUIRequest,
  shouldMarkUserUIRequest,
} from './adminUIRequest'
import { refreshAuthTokens } from './tokenRefresh'
import { expireAuthSession, storedAuthUserId } from './authSession'
import { getAPIBaseURL, isDesktopRuntime, redirectToAppPath } from './url'
export { buildApiUrl, buildGatewayUrl } from './url'

interface AuthenticatedRequest extends InternalAxiosRequestConfig {
  _retry?: boolean
  _authUserId?: number | null
}

function requestAccessToken(config?: InternalAxiosRequestConfig): string | null {
  const header = config?.headers?.Authorization ?? config?.headers?.authorization
  return typeof header === 'string' && header.startsWith('Bearer ') ? header.slice(7) || null : null
}

const changedSessionError = () => ({
  status: 401, code: 'AUTH_SESSION_CHANGED', message: 'Authentication session changed while the request was in flight.'
})

// ==================== Axios Instance Configuration ====================

export const apiClient: AxiosInstance = axios.create({
  baseURL: getAPIBaseURL(),
  withCredentials: true,
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json'
  }
})

// ==================== Request Interceptor ====================

// Get user's timezone
const getUserTimezone = (): string => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    return 'UTC'
  }
}

apiClient.interceptors.request.use(
  (config: AuthenticatedRequest) => {
    // Attach token from localStorage
    const token = localStorage.getItem('auth_token')
    const userId = storedAuthUserId()
    if (config._retry && config._authUserId !== undefined && config._authUserId !== userId) {
      throw changedSessionError()
    }
    if (token && config.headers) {
      config.headers.Authorization = `Bearer ${token}`
    }
    config._authUserId = userId

    // Attach locale for backend translations
    if (config.headers) {
      config.headers['Accept-Language'] = getLocale()
    }

    // Attach timezone for all GET requests (backend may use it for default date ranges)
    if (config.method === 'get') {
      if (!config.params) {
        config.params = {}
      }
      config.params.timezone = getUserTimezone()
    }

    if (config.headers) {
      const requestURL = String(config.url || '')
      if (shouldMarkAdminUIRequest(requestURL)) {
        config.headers[ADMIN_UI_REQUEST_HEADER] = '1'
      }
      if (shouldMarkUserUIRequest(requestURL)) {
        config.headers[USER_UI_REQUEST_HEADER] = '1'
      }
    }

    return config
  },
  (error) => {
    return Promise.reject(error)
  }
)

// ==================== Response Interceptor ====================

apiClient.interceptors.response.use(
  (response: AxiosResponse) => {
    // Unwrap standard API response format { code, message, data }
    const apiResponse = response.data as ApiResponse<unknown>
    if (apiResponse && typeof apiResponse === 'object' && 'code' in apiResponse) {
      if (apiResponse.code === 0) {
        // Success - return the data portion
        response.data = apiResponse.data
      } else {
        // API error
        const resp = apiResponse as unknown as Record<string, unknown>
        return Promise.reject({
          status: response.status,
          code: apiResponse.code,
          message: apiResponse.message || 'Unknown error',
          reason: resp.reason,
          metadata: resp.metadata,
        })
      }
    }
    return response
  },
  async (error: AxiosError<ApiResponse<unknown>>) => {
    if (error.code === 'AUTH_SESSION_CHANGED') return Promise.reject(error)
    // Request cancellation: keep the original axios cancellation error so callers can ignore it.
    // Otherwise we'd misclassify it as a generic "network error".
    if (error.code === 'ERR_CANCELED' || axios.isCancel(error)) {
      return Promise.reject(error)
    }

    const originalRequest = error.config as AuthenticatedRequest | undefined

    // Handle common errors
    if (error.response) {
      const { status, data } = error.response
      const url = String(error.config?.url || '')

      // Validate `data` shape to avoid HTML error pages breaking our error handling.
      const apiData = (typeof data === 'object' && data !== null ? data : {}) as Record<string, any>

      // Ops monitoring disabled: treat as feature-flagged 404, and proactively redirect away
      // from ops pages to avoid broken UI states.
      if (status === 404 && apiData.message === 'Ops monitoring is disabled') {
        try {
          localStorage.setItem('ops_monitoring_enabled_cached', 'false')
        } catch {
          // ignore localStorage failures
        }
        try {
          window.dispatchEvent(new CustomEvent('ops-monitoring-disabled'))
        } catch {
          // ignore event failures
        }

        if (window.location.pathname.startsWith('/admin/ops')) {
          redirectToAppPath('/admin/settings')
        }

        return Promise.reject({
          status,
          code: 'OPS_DISABLED',
          message: apiData.message || error.message,
          url
        })
      }

      if (status === 423 && apiData.code === 'ADMIN_COMPLIANCE_ACK_REQUIRED') {
        try {
          window.dispatchEvent(new CustomEvent('admin-compliance-required', {
            detail: apiData.metadata || {}
          }))
        } catch {
          // ignore event failures
        }

        return Promise.reject({
          status,
          code: apiData.code,
          message: apiData.message || error.message,
          metadata: apiData.metadata,
        })
      }

      if (status === 401) {
        const isAuthEndpoint =
          url.includes('/auth/login') || url.includes('/auth/register') || url.includes('/auth/refresh')
        if (!isAuthEndpoint) {
          const accessToken = localStorage.getItem('auth_token')
          const refreshToken = localStorage.getItem('refresh_token')
          const failedAccessToken = requestAccessToken(originalRequest)
          const currentUserId = storedAuthUserId()
          const differentToken = accessToken && accessToken !== failedAccessToken
          const sameKnownUser = originalRequest?._authUserId != null && originalRequest._authUserId === currentUserId

          // Late responses from a logged-out/replaced session must not revoke or replay as a new user.
          if (differentToken && (!sameKnownUser || !refreshToken || originalRequest?._retry)) {
            return Promise.reject(changedSessionError())
          }

          if (refreshToken && originalRequest && !originalRequest._retry) {
            const refreshSessionToken = accessToken
            const refreshSessionUser = currentUserId
            originalRequest._retry = true
            try {
              const tokens = await refreshAuthTokens({ failedAccessToken })
              if (storedAuthUserId() !== refreshSessionUser || localStorage.getItem('auth_token') !== tokens.access_token) {
                return Promise.reject(changedSessionError())
              }
              originalRequest.headers.Authorization = 'Bearer ' + tokens.access_token
              originalRequest._authUserId = refreshSessionUser
              return apiClient(originalRequest)
            } catch (refreshError) {
              if (localStorage.getItem('refresh_token') !== refreshToken ||
                  localStorage.getItem('auth_token') !== refreshSessionToken ||
                  storedAuthUserId() !== refreshSessionUser) {
                return Promise.reject(changedSessionError())
              }
              const failure = refreshError as { status?: number; response?: { status?: number } }
              const refreshStatus = failure.response?.status ?? failure.status
              if (refreshStatus === undefined || refreshStatus === 408 || refreshStatus === 429 || refreshStatus >= 500) {
                return Promise.reject({ status: refreshStatus ?? 0, code: 'TOKEN_REFRESH_UNAVAILABLE', message: 'Unable to refresh the session right now. Please try again.' })
              }
              expireAuthSession()
              return Promise.reject({ status: 401, code: 'TOKEN_REFRESH_FAILED', message: 'Session expired. Please log in again.' })
            }
          }
          // Covers both sessions without refresh tokens and a refreshed request rejected again.
          expireAuthSession(Boolean(accessToken || failedAccessToken))
        }
      }

      // Return structured error
      return Promise.reject({
        status,
        code: apiData.code,
        reason: apiData.reason,
        error: apiData.error,
        message: apiData.message || apiData.detail || error.message,
        metadata: apiData.metadata,
      })
    }

    // Network error. Desktop builds use a local Go backend, so surface the
    // actual endpoint and the CORS prerequisite instead of a generic browser hint.
    const networkMessage = isDesktopRuntime()
      ? `无法连接 Sub2API 后端（${getAPIBaseURL()}）。请先启动后端，并将 http://tauri.localhost 加入 CORS 白名单。`
      : 'Network error. Please check your connection.'
    return Promise.reject({
      status: 0,
      message: networkMessage
    })
  }
)

export default apiClient
