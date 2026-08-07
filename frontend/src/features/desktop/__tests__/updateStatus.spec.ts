import { describe, expect, it } from 'vitest'
import {
  describeCoreUpdateFailure,
  describeDesktopUpdateFailure,
  resolveUpdateCheckState,
} from '../updateStatus'

describe('desktop update status', () => {
  it('does not report current when every remote update check failed', () => {
    expect(resolveUpdateCheckState({
      checking: false,
      hasUpdate: false,
      hasFailures: true,
      hasChecked: true,
    })).toBe('unavailable')
  })

  it('reports current only after a successful check without updates', () => {
    expect(resolveUpdateCheckState({
      checking: false,
      hasUpdate: false,
      hasFailures: false,
      hasChecked: true,
    })).toBe('current')
  })

  it('turns an upstream release 404 into an actionable core-only message', () => {
    expect(describeCoreUpdateFailure(
      'HTTP status client error (404 Not Found) for url (https://api.github.com/repos/Wei-Shaw/sub2api/releases/latest)',
    )).toContain('Wei-Shaw/sub2api')
  })

  it('treats a missing initial desktop release as a non-fatal publishing state', () => {
    expect(describeDesktopUpdateFailure(new Error('404 latest.json'))).toContain('尚未发布')
  })

  it('keeps diagnostic details for other desktop updater failures', () => {
    expect(describeDesktopUpdateFailure(new Error('signature rejected')))
      .toBe('桌面更新检查失败：signature rejected')
  })

  it('turns transport failures into an actionable system proxy message', () => {
    expect(describeDesktopUpdateFailure(new Error('error sending request for url')))
      .toContain('自动跟随当前 Windows 代理')
    expect(describeCoreUpdateFailure(new Error('tcp connect error')))
      .toContain('系统代理或 TUN')
  })
})
