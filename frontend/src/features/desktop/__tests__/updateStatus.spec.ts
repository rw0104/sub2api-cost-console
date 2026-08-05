import { describe, expect, it } from 'vitest'
import { describeUpdateFailure, resolveUpdateCheckState } from '../updateStatus'

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

  it('turns GitHub release 404 responses into an actionable desktop message', () => {
    expect(describeUpdateFailure(
      'desktop',
      'Could not fetch a valid release JSON from the remote',
    )).toContain('latest.json')

    expect(describeUpdateFailure(
      'core',
      'HTTP status client error (404 Not Found) for url (https://github.com/example/core-update.json)',
    )).toContain('GitHub 网络可达')
  })
})
