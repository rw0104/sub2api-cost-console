import { invoke } from '@tauri-apps/api/core'
import { isDesktopRuntime } from './url'

export type NativeClientId = 'chatgpt' | 'codex' | 'claude-code' | 'cursor' | 'opencode' | 'grok'
export type NativeGatewayProfile = 'anthropic' | 'openai' | 'gemini' | 'antigravity' | 'grok' | 'composite'

export interface NativeClientLaunchRequest {
  client_id: NativeClientId
  gateway_profile: NativeGatewayProfile
  base_url: string
  api_key: string
  working_directory: string
}

export interface NativeClientAvailability {
  client_id: NativeClientId
  label: string
  executable: string | null
  available: boolean
  message: string
}

export interface NativeClientLaunchPreview {
  client_id: NativeClientId
  label: string
  executable: string | null
  working_directory: string
  display_command: string
  environment_keys: string[]
  available: boolean
  message: string
}

export interface NativeClientLaunchReceipt {
  client_id: NativeClientId
  pid: number
  executable: string
  message: string
}

const NATIVE_WORKING_DIRECTORY_KEY_PREFIX = 'sub2api.nativeClient.workingDirectory.'
const LEGACY_NATIVE_WORKING_DIRECTORY_KEY = 'sub2api.nativeClient.workingDirectory'

function nativeWorkingDirectoryStorageKey(clientId: NativeClientId): string {
  return `${NATIVE_WORKING_DIRECTORY_KEY_PREFIX}${clientId}`
}

/** Read the last directory used for a client, migrating the original shared preference once. */
export function getStoredNativeWorkingDirectory(clientId: NativeClientId): string {
  try {
    const stored = localStorage.getItem(nativeWorkingDirectoryStorageKey(clientId))?.trim()
    if (stored) return stored

    const legacy = localStorage.getItem(LEGACY_NATIVE_WORKING_DIRECTORY_KEY)?.trim()
    if (legacy) {
      localStorage.setItem(nativeWorkingDirectoryStorageKey(clientId), legacy)
      return legacy
    }
  } catch {
    // Local preference storage is optional; the native runtime still supplies a fallback.
  }
  return ''
}

export function storeNativeWorkingDirectory(clientId: NativeClientId, directory: string): void {
  const value = directory.trim()
  if (!value || value === '.') return
  try {
    localStorage.setItem(nativeWorkingDirectoryStorageKey(clientId), value)
  } catch {
    // Local preference storage is optional; launching remains independent of it.
  }
}

function assertDesktop(): void {
  if (!isDesktopRuntime()) {
    throw new Error('原生客户端启动仅在 Windows 桌面应用中可用。')
  }
}

export async function listNativeClients(): Promise<NativeClientAvailability[]> {
  assertDesktop()
  return invoke<NativeClientAvailability[]>('list_native_clients')
}

export async function getNativeWorkingDirectory(): Promise<string> {
  assertDesktop()
  return invoke<string>('native_working_directory')
}

export async function pickNativeWorkingDirectory(): Promise<string | null> {
  assertDesktop()
  return invoke<string | null>('pick_native_working_directory')
}

export async function previewNativeClientLaunch(
  request: NativeClientLaunchRequest
): Promise<NativeClientLaunchPreview> {
  assertDesktop()
  return invoke<NativeClientLaunchPreview>('preview_native_client_launch', { request })
}

export async function launchNativeClient(
  request: NativeClientLaunchRequest
): Promise<NativeClientLaunchReceipt> {
  assertDesktop()
  return invoke<NativeClientLaunchReceipt>('launch_native_client', { request })
}
