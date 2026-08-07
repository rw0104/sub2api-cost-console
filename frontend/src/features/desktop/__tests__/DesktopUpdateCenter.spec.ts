import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import DesktopUpdateCenter from '../DesktopUpdateCenter.vue'

const mocks = vi.hoisted(() => ({
  check: vi.fn(),
  getVersion: vi.fn(),
  invoke: vi.fn(),
  listen: vi.fn(),
  relaunch: vi.fn(),
}))

vi.mock('@/api/url', () => ({ isDesktopRuntime: () => true }))
vi.mock('@tauri-apps/api/app', () => ({ getVersion: mocks.getVersion }))
vi.mock('@tauri-apps/api/core', () => ({ invoke: mocks.invoke }))
vi.mock('@tauri-apps/api/event', () => ({ listen: mocks.listen }))
vi.mock('@tauri-apps/plugin-process', () => ({ relaunch: mocks.relaunch }))
vi.mock('@tauri-apps/plugin-updater', () => ({ check: mocks.check }))

class PrivateFieldUpdate {
  version = '0.2.7'
  body = 'signed desktop update'
  #downloads = 0

  async downloadAndInstall(onEvent: (event: { event: string; data: Record<string, never> }) => void) {
    this.#downloads += 1
    onEvent({ event: 'Finished', data: {} })
  }

  get downloads() {
    return this.#downloads
  }
}

describe('DesktopUpdateCenter', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mocks.getVersion.mockResolvedValue('0.2.6')
    mocks.listen.mockResolvedValue(vi.fn())
    mocks.relaunch.mockResolvedValue(undefined)
    mocks.invoke.mockImplementation(async (command: string) => {
      if (command === 'desktop_backend_status') {
        return {
          core_version: '0.1.171',
          algorithm_version: '1.3.0',
          upstream_commit: 'f0e7a9c7',
        }
      }
      if (command === 'check_core_update') {
        return {
          available: false,
          current_version: '0.1.171',
          current_algorithm_version: '1.3.0',
          upstream_commit: 'f0e7a9c7',
          update: null,
          previous_version: null,
        }
      }
      return undefined
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('keeps the Tauri Update instance unproxied while installing', async () => {
    const update = new PrivateFieldUpdate()
    mocks.check.mockResolvedValue(update)

    const wrapper = mount(DesktopUpdateCenter)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1_800)
    await flushPromises()
    await wrapper.get('button.desktop-update__trigger').trigger('click')

    const installButton = wrapper.findAll('button')
      .find((button) => button.text().includes('安装桌面更新'))
    expect(installButton).toBeDefined()

    await installButton!.trigger('click')
    await flushPromises()

    expect(update.downloads).toBe(1)
    expect(mocks.invoke).toHaveBeenCalledWith('desktop_backend_prepare_relaunch')
    expect(mocks.relaunch).toHaveBeenCalledOnce()
    expect(wrapper.text()).not.toContain('Cannot read private member')

    wrapper.unmount()
  })
})
