const DEFAULT_API_BASE_URL = '/api/v1'
const DEFAULT_DESKTOP_API_BASE_URL = 'http://127.0.0.1:18765/api/v1'

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
      return localStorage.getItem('sub2api.desktop.backendUrl') || DEFAULT_DESKTOP_API_BASE_URL
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
