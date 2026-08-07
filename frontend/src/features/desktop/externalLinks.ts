import { openUrl } from '@tauri-apps/plugin-opener'

export const PROJECT_REPOSITORY_URL = 'https://github.com/rw0104/sub2api-cost-console'
export const ACCOUNT_PURCHASE_URL = 'https://pay.ldxp.cn/shop/13QL6FLR'

const allowedExternalUrls = new Set([
  PROJECT_REPOSITORY_URL,
  ACCOUNT_PURCHASE_URL,
])

export async function openProjectExternalUrl(url: string): Promise<void> {
  if (!allowedExternalUrls.has(url)) {
    throw new Error('拒绝打开未授权的外部链接')
  }
  if ('__TAURI_INTERNALS__' in window) {
    await openUrl(url)
    return
  }
  window.open(url, '_blank', 'noopener,noreferrer')
}
