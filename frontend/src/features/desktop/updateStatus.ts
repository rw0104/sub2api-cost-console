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

function isConnectionFailure(detail: string): boolean {
  return /error sending request|timed? out|connection (?:refused|reset)|dns error|tcp connect error/i.test(detail)
}

export function describeDesktopUpdateFailure(error: unknown): string {
  const detail = errorText(error)
  const isRelease404 = /404/i.test(detail)
    && /releases\/(download|latest)|latest\.json/i.test(detail)

  if (isRelease404) {
    return '桌面更新清单尚未发布或暂时无法读取；当前安装版本继续运行。'
  }

  if (isConnectionFailure(detail)) {
    return `桌面更新源连接失败；请确认系统代理或 TUN 可用。桌面端会自动跟随当前 Windows 代理。详细信息：${detail}`
  }

  return `桌面更新检查失败：${detail}`
}

export function describeCoreUpdateFailure(error: unknown): string {
  const detail = errorText(error)
  const isRelease404 = /404/i.test(detail)
    && /releases\/(download|latest)|api\.github\.com/i.test(detail)

  if (isRelease404) {
    return 'Wei-Shaw/sub2api 上游 Release 暂时无法匿名读取。桌面端没有检查本项目版本，当前内核继续运行，请稍后重试。'
  }

  if (isConnectionFailure(detail)) {
    return `上游内核更新源连接失败；请确认系统代理或 TUN 可用。桌面端会自动跟随当前 Windows 代理。详细信息：${detail}`
  }

  return `上游内核检查失败：${detail}`
}
