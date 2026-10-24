import { describe, expect, it } from 'vitest'
import {
  hasExistingLocalServices,
  quickSetupBlocker,
  recommendedSetupMode,
  type SetupEnvironment,
} from '../setupEnvironment'

function environment(overrides: Partial<SetupEnvironment> = {}): SetupEnvironment {
  return {
    desktop: true,
    docker: { installed: true, running: true, version: 'Docker 28', message: 'ready' },
    postgres: { host: '127.0.0.1', port: 5432, reachable: false },
    redis: { host: '127.0.0.1', port: 6379, reachable: false },
    managed_postgres: { host: '127.0.0.1', port: 15432, reachable: false },
    managed_redis: { host: '127.0.0.1', port: 16379, reachable: false },
    ...overrides,
  }
}

describe('desktop setup environment decisions', () => {
  it('recommends quick setup only when Docker is ready and managed ports are free', () => {
    const detected = environment()
    expect(quickSetupBlocker(detected)).toBeNull()
    expect(recommendedSetupMode(detected)).toBe('quick')
  })

  it('requires manual advanced configuration when Docker is unavailable', () => {
    const detected = environment({
      docker: { installed: false, running: false, version: '', message: 'missing' },
    })
    expect(quickSetupBlocker(detected)).toBe('docker_missing')
    expect(recommendedSetupMode(detected)).toBe('advanced')
  })

  it('does not create managed containers over existing local services', () => {
    const detected = environment({
      postgres: { host: '127.0.0.1', port: 5432, reachable: true },
    })
    expect(hasExistingLocalServices(detected)).toBe(true)
    expect(recommendedSetupMode(detected)).toBe('advanced')
  })

  it('blocks quick setup when reserved managed ports are already occupied', () => {
    const detected = environment({
      managed_redis: { host: '127.0.0.1', port: 16379, reachable: true },
    })
    expect(quickSetupBlocker(detected)).toBe('managed_port_conflict')
  })
})
