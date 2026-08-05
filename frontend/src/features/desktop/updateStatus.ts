export type UpdateCheckState = 'idle' | 'checking' | 'available' | 'current' | 'unavailable'

export interface UpdateCheckStateInput {
  checking: boolean
  hasUpdate: boolean
  hasFailures: boolean
  hasChecked: boolean
}

export function resolveUpdateCheckState(input: UpdateCheckStateInput): UpdateCheckState {
  if (input.checking) return 'checking'
  if (input.hasFailures) return 'unavailable'
  if (input.hasUpdate) return 'available'
  return input.hasChecked ? 'current' : 'idle'
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function describeUpdateFailure(kind: 'desktop' | 'core', error: unknown): string {
  const detail = errorText(error)
  const isRelease404 = /404|valid release JSON|releases\/(download|latest)/i.test(detail)

  if (kind === 'desktop' && isRelease404) {
    return 'GitHub 网络可达，但当前发布仓库的 latest.json 不允许匿名访问（404）。这不是全局断网；当前桌面版本不受影响，请恢复该 Release 的公网访问或配置公开更新镜像。'
  }

  if (kind === 'core' && isRelease404) {
    return 'GitHub 网络可达，但当前发布仓库的 core-update.json 不允许匿名访问（404）。当前内核继续运行；上游公开仓库可匿名下载，不代表本仓库的受限 Release 也可访问。'
  }

  return `${kind === 'desktop' ? '桌面更新' : '内核更新'}检查失败：${detail}`
}
