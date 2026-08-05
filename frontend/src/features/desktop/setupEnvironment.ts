export type SetupMode = 'quick' | 'advanced'
export type QuickSetupBlocker =
  | 'desktop_only'
  | 'docker_missing'
  | 'docker_stopped'
  | 'managed_port_conflict'

export interface PortProbe {
  host: string
  port: number
  reachable: boolean
}

export interface DockerProbe {
  installed: boolean
  running: boolean
  version: string
  message: string
}

export interface SetupEnvironment {
  desktop: boolean
  docker: DockerProbe
  postgres: PortProbe
  redis: PortProbe
  managed_postgres: PortProbe
  managed_redis: PortProbe
}

export function quickSetupBlocker(environment: SetupEnvironment | null): QuickSetupBlocker | null {
  if (!environment?.desktop) return 'desktop_only'
  if (!environment.docker.installed) return 'docker_missing'
  if (!environment.docker.running) return 'docker_stopped'
  if (environment.managed_postgres.reachable || environment.managed_redis.reachable) {
    return 'managed_port_conflict'
  }
  return null
}

export function recommendedSetupMode(environment: SetupEnvironment | null): SetupMode {
  if (!environment) return 'advanced'
  // Existing standard database endpoints usually belong to the user. Reusing
  // them through the explicit advanced flow avoids creating duplicate stores.
  if (environment.postgres.reachable || environment.redis.reachable) return 'advanced'
  return quickSetupBlocker(environment) === null ? 'quick' : 'advanced'
}

export function hasExistingLocalServices(environment: SetupEnvironment | null): boolean {
  return Boolean(environment?.postgres.reachable || environment?.redis.reachable)
}
