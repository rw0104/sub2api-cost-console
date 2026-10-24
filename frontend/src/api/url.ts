const DEFAULT_API_BASE_URL = '/api/v1'
const DEFAULT_DESKTOP_API_BASE_URL = import.meta.env.VITE_DESKTOP_CHANNEL === 'plugin-preview'
  ? 'http://127.0.0.1:19765/api/v1'
  : 'http://127.0.0.1:18765/api/v1'

const DESKTOP_BACKEND_URL_STORAGE_KEY = 'sub2api.desktop.backendUrl'

export function isDesktopRuntime(): boolean {
  return typeof window !== 'undefined' && '__TAURI_INTERNALS__' in (window as any)
}

function getConfiguredAPIBaseURL(): unknown {
  const configured = import.meta.env.VITE_API_BASE_URL
  if (configured) {
    return configured
  }

  if (isDesktopRuntime()) {
    try {
      return localStorage.getItem(DESKTOP_BACKEND_URL_STORAGE_KEY) || DEFAULT_DESKTOP_API_BASE_URL
    } catch {
      return DEFAULT_DESKTOP_API_BASE_URL
    }
  }

  return DEFAULT_API_BASE_URL
}

const API_BASE_URL = normalizeAPIBaseURL(getConfiguredAPIBaseURL())

function normalizePath(path: string): string {
  return path.startsWith('/') ? path : `/${path}`
}

function normalizeAPIBaseURL(value: unknown): string {
  const raw = String(value || DEFAULT_API_BASE_URL).trim() || DEFAULT_API_BASE_URL
  const withoutTrailingSlash = raw.replace(/\/+$/, '')
  if (/^[a-z][a-z\d+.-]*:\/\//i.test(withoutTrailingSlash) || withoutTrailingSlash.startsWith('//')) {
    return withoutTrailingSlash
  }
  return normalizePath(withoutTrailingSlash)
}

export function getAPIBaseURL(): string {
  return API_BASE_URL
}

/**
 * Points the WebView at the origin the desktop shell reports for its managed
 * backend. The base URL is resolved once at load, so a `true` result means the
 * caller must reload the page for the new address to take effect.
 */
export function syncDesktopBackendOrigin(origin: string): boolean {
  if (import.meta.env.VITE_API_BASE_URL || !origin) return false
  const expected = normalizeAPIBaseURL(`${origin.replace(/\/+$/, '')}${DEFAULT_API_BASE_URL}`)
  if (expected === API_BASE_URL) return false
  try {
    localStorage.setItem(DESKTOP_BACKEND_URL_STORAGE_KEY, expected)
    return true
  } catch {
    return false
  }
}

/** Gateway base (`…/v1`) of the desktop-managed backend for local clients. */
export function getDesktopGatewayBase(): string {
  return buildGatewayUrl('/v1')
}

export function getCurrentAppPath(): string {
  const path = isDesktopRuntime() ? window.location.hash.slice(1) : window.location.pathname
  return path.split(/[?#]/, 1)[0] || '/'
}

/** Navigate without losing the hash router when the app is hosted in a Tauri asset window. */
export function redirectToAppPath(path: string): void {
  const normalized = path.startsWith('/') ? path : `/${path}`
  if (isDesktopRuntime()) {
    window.location.hash = `#${normalized}`
    return
  }
  window.location.href = normalized
}

export function buildApiUrl(path: string): string {
  const base = getAPIBaseURL().replace(/\/+$/, '')
  let suffix = normalizePath(path)
  if (suffix === DEFAULT_API_BASE_URL) {
    suffix = ''
  } else if (suffix.startsWith(`${DEFAULT_API_BASE_URL}/`)) {
    suffix = suffix.slice(DEFAULT_API_BASE_URL.length)
  }
  return `${base}${suffix}`
}

export function buildGatewayUrl(path: string): string {
  const suffix = normalizePath(path)
  try {
    const origin =
      typeof window === 'undefined'
        ? new URL(getAPIBaseURL()).origin
        : new URL(getAPIBaseURL(), window.location.origin).origin
    return `${origin}${suffix}`
  } catch {
    return suffix
  }
}
