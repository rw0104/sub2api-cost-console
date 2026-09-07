import { getCurrentAppPath, redirectToAppPath } from './url'

const invalidationHandlers = new Set<() => void>()
let loginRedirectPending = false

export function markAuthSessionEstablished(): void {
  loginRedirectPending = false
}

/** Store scopes subscribe without introducing an API-client / Pinia import cycle. */
export function onAuthSessionInvalidated(handler: () => void): () => void {
  invalidationHandlers.add(handler)
  return () => { invalidationHandlers.delete(handler) }
}

export function storedAuthUserId(): number | null {
  try {
    const id = Number(JSON.parse(localStorage.getItem('auth_user') || 'null')?.id)
    return Number.isFinite(id) && id > 0 ? id : null
  } catch { return null }
}

/** Invalidate memory and persistence synchronously, before a hash-router guard can run. */
export function expireAuthSession(markExpired = true): void {
  // A later successful login may legitimately expire again in the same document.
  if (localStorage.getItem('auth_token')) loginRedirectPending = false
  for (const key of ['auth_token', 'refresh_token', 'auth_user', 'token_expires_at']) {
    localStorage.removeItem(key)
  }
  for (const handler of invalidationHandlers) handler()
  if (!loginRedirectPending) {
    loginRedirectPending = true
    if (markExpired) sessionStorage.setItem('auth_expired', '1')
    if (getCurrentAppPath() !== '/login') redirectToAppPath('/login')
  }
}
