import { afterEach, describe, expect, it, vi } from 'vitest'
import { openUrl } from '@tauri-apps/plugin-opener'
import {
  ACCOUNT_PURCHASE_URL,
  openProjectExternalUrl,
  PROJECT_REPOSITORY_URL,
} from '../externalLinks'

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn().mockResolvedValue(undefined),
}))

describe('desktop external links', () => {
  afterEach(() => {
    vi.clearAllMocks()
    delete (window as typeof window & { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__
  })

  it('opens the project repository and purchase page through the system browser', async () => {
    ;(window as typeof window & { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = {}

    await openProjectExternalUrl(PROJECT_REPOSITORY_URL)
    await openProjectExternalUrl(ACCOUNT_PURCHASE_URL)

    expect(openUrl).toHaveBeenNthCalledWith(1, PROJECT_REPOSITORY_URL)
    expect(openUrl).toHaveBeenNthCalledWith(2, ACCOUNT_PURCHASE_URL)
  })

  it('rejects external destinations outside the fixed allowlist', async () => {
    await expect(openProjectExternalUrl('https://example.com')).rejects.toThrow('未授权')
    expect(openUrl).not.toHaveBeenCalled()
  })
})
