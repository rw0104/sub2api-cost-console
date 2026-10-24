/**
 * Setup API endpoints
 */
import axios from 'axios'
import { invoke } from '@tauri-apps/api/core'
import type { SetupEnvironment } from '@/features/desktop/setupEnvironment'
import { buildGatewayUrl } from './url'
import { isDesktopRuntime } from './url'

// Create a separate client for setup endpoints (not under /api/v1)
const setupClient = axios.create({
  baseURL: buildGatewayUrl('/').replace(/\/+$/, ''),
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json'
  }
})

export interface SetupStatus {
  needs_setup: boolean
  step: string
}

export interface DatabaseConfig {
  host: string
  port: number
  user: string
  password: string
  dbname: string
  sslmode: string
}

export interface RedisConfig {
  host: string
  port: number
  username: string
  password: string
  db: number
  enable_tls: boolean
}

export interface AdminConfig {
  email: string
  password: string
}

export interface ServerConfig {
  host: string
  port: number
  mode: string
}

export interface InstallRequest {
  database: DatabaseConfig
  redis: RedisConfig
  admin: AdminConfig
  server: ServerConfig
}

export interface InstallResponse {
  message: string
  restart: boolean
}

export interface ManagedSetupConfig {
  database: DatabaseConfig
  redis: RedisConfig
  postgres_image: string
  valkey_image: string
}

export interface SetupProvisionProgress {
  stage: string
  message: string
  percent: number
}

export async function detectSetupEnvironment(): Promise<SetupEnvironment> {
  if (!isDesktopRuntime()) {
    const unavailable = (port: number) => ({
      host: '127.0.0.1',
      port,
      reachable: false,
    })
    return {
      desktop: false,
      docker: {
        installed: false,
        running: false,
        version: '',
        message: 'Automatic environment detection is available in the desktop application only.',
      },
      postgres: unavailable(5432),
      redis: unavailable(6379),
      managed_postgres: unavailable(15432),
      managed_redis: unavailable(16379),
    }
  }
  return invoke<SetupEnvironment>('detect_setup_environment')
}

export async function provisionQuickSetup(): Promise<ManagedSetupConfig> {
  if (!isDesktopRuntime()) {
    throw new Error('快速安装仅在 Windows 桌面应用中可用。')
  }
  return invoke<ManagedSetupConfig>('provision_quick_setup')
}

/**
 * Get setup status
 */
export async function getSetupStatus(): Promise<SetupStatus> {
  const response = await setupClient.get('/setup/status')
  return response.data.data
}

/**
 * Test database connection
 */
export async function testDatabase(config: DatabaseConfig): Promise<void> {
  await setupClient.post('/setup/test-db', config)
}

/**
 * Test Redis connection
 */
export async function testRedis(config: RedisConfig): Promise<void> {
  await setupClient.post('/setup/test-redis', config)
}

/**
 * Perform installation
 */
export async function install(config: InstallRequest): Promise<InstallResponse> {
  const response = await setupClient.post('/setup/install', config)
  return response.data.data
}
