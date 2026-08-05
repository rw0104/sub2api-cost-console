import { describe, expect, it } from 'vitest'
import { describeCoreUpdateFailure, resolveUpdateCheckState } from '../updateStatus'

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
})
