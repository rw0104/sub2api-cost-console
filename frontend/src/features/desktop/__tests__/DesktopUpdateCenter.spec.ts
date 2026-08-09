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
    localStorage.clear()
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
      if (command === 'inspect_core_identity') {
        return {
          action: 'none',
          bundled_differs: false,
          current: { version: '0.1.171', algorithm_version: '1.3.0', upstream_commit: 'f0e7a9c7', sha256: 'old' },
          bundled: { version: '0.1.171', algorithm_version: '1.3.0', upstream_commit: 'desktop123', sha256: 'new' },
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

  it('offers the same-upstream compatible core when the active core lacks required capabilities', async () => {
    mocks.check.mockResolvedValue(null)
    mocks.invoke.mockImplementation(async (command: string) => {
      if (command === 'desktop_backend_status') {
        return { core_version: '0.1.171', algorithm_version: '1.3.0', upstream_commit: 'f0e7a9c7', core_sha256: 'old' }
      }
      if (command === 'check_core_update') {
        return { available: false, current_version: '0.1.171', current_algorithm_version: '1.3.0', upstream_commit: 'f0e7a9c7', update: null, previous_version: null }
      }
      if (command === 'inspect_core_identity') {
        return {
          action: 'install_bundled',
          bundled_differs: true,
          current: { version: '0.1.171', algorithm_version: 'unknown', extension_version: '', capabilities: [], upstream_commit: 'f0e7a9c7', sha256: 'old' },
          bundled: { version: '0.1.171', algorithm_version: '1.3.0', extension_version: '1.0.0', capabilities: ['account_cost_loss_ledger.v1'], upstream_commit: 'f0e7a9c7', sha256: 'new' },
          missing_capabilities: ['account_cost_loss_ledger.v1'],
          integrity_valid: true,
        }
      }
      if (command === 'restore_bundled_core') {
        return { version: '0.1.171', algorithm_version: '1.3.0', upstream_commit: 'desktop123', restart_required: false }
      }
      return undefined
    })

    const wrapper = mount(DesktopUpdateCenter)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1_800)
    await flushPromises()
    await wrapper.get('button.desktop-update__trigger').trigger('click')

    expect(wrapper.text()).toContain('扩展兼容内核')
    const restoreButton = wrapper.findAll('button').find((button) => button.text().includes('安装扩展兼容内核'))
    expect(restoreButton).toBeDefined()
    await restoreButton!.trigger('click')
    await flushPromises()

    expect(mocks.invoke).toHaveBeenCalledWith('restore_bundled_core')
    expect(mocks.relaunch).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('内核已完成更新')
    expect(wrapper.text()).not.toContain('正在执行健康检查')
    wrapper.unmount()
  })

  it('does not prompt when a compatible active core has a different payload hash', async () => {
    mocks.check.mockResolvedValue(null)
    mocks.invoke.mockImplementation(async (command: string) => {
      if (command === 'desktop_backend_status') {
        return { core_version: '0.1.173', algorithm_version: '1.5.0', upstream_commit: '29009f0b', core_sha256: 'active' }
      }
      if (command === 'check_core_update') {
        return { available: false, current_version: '0.1.173', current_algorithm_version: '1.5.0', upstream_commit: '29009f0b', update: null, previous_version: null }
      }
      if (command === 'inspect_core_identity') {
        return {
          action: 'none',
          bundled_differs: true,
          current: { version: '0.1.173', algorithm_version: '1.5.0', extension_version: '1.0.0', capabilities: ['account_cost_loss_ledger.v1'], upstream_commit: '29009f0b', sha256: 'active' },
          bundled: { version: '0.1.173', algorithm_version: '1.5.0', extension_version: '1.0.0', capabilities: ['account_cost_loss_ledger.v1'], upstream_commit: '29009f0b', sha256: 'bundled' },
          missing_capabilities: [],
          integrity_valid: true,
        }
      }
      return undefined
    })

    const wrapper = mount(DesktopUpdateCenter)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1_800)
    await flushPromises()

    expect(wrapper.find('section.desktop-update__release--bundled').exists()).toBe(false)
    wrapper.unmount()
  })

  it('reports a newer official core while waiting for its compatible build', async () => {
    mocks.check.mockResolvedValue(null)
    mocks.invoke.mockImplementation(async (command: string) => {
      if (command === 'desktop_backend_status') {
        return { core_version: '0.1.173', algorithm_version: '1.5.0', upstream_commit: '29009f0b' }
      }
      if (command === 'check_core_update') {
        return {
          available: false,
          compatibility_pending: true,
          upstream_latest_version: '0.1.174',
          current_version: '0.1.173',
          current_algorithm_version: '1.5.0',
          upstream_commit: '29009f0b',
          update: { version: '0.1.173', extension_version: '1.0.0', capabilities: ['account_cost_loss_ledger.v1'], published_at: '', notes: '' },
          previous_version: null,
        }
      }
      if (command === 'inspect_core_identity') {
        return {
          action: 'none', bundled_differs: false, missing_capabilities: [], integrity_valid: true,
          current: { version: '0.1.173', algorithm_version: '1.5.0', extension_version: '1.0.0', capabilities: ['account_cost_loss_ledger.v1'], upstream_commit: '29009f0b', sha256: 'active' },
          bundled: { version: '0.1.173', algorithm_version: '1.5.0', extension_version: '1.0.0', capabilities: ['account_cost_loss_ledger.v1'], upstream_commit: '29009f0b', sha256: 'bundled' },
        }
      }
      return undefined
    })

    const wrapper = mount(DesktopUpdateCenter)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1_800)
    await flushPromises()
    await wrapper.get('button.desktop-update__trigger').trigger('click')

    expect(wrapper.text()).toContain('官方内核 v0.1.174 已发布，兼容内核构建中')
    expect(wrapper.text()).not.toContain('下载、校验并安全重启')
    wrapper.unmount()
  })

  it('automatically stages a compatible core update without forcing a relaunch', async () => {
    mocks.check.mockResolvedValue(null)
    mocks.invoke.mockImplementation(async (command: string) => {
      if (command === 'desktop_backend_status') {
        return { core_version: '0.1.173', algorithm_version: '1.5.0', upstream_commit: '29009f0b' }
      }
      if (command === 'check_core_update') {
        return {
          available: true, compatibility_pending: false, upstream_latest_version: '0.1.174',
          current_version: '0.1.173', current_algorithm_version: '1.5.0', upstream_commit: '29009f0b',
          update: { version: '0.1.174', extension_version: '1.0.0', capabilities: ['account_cost_loss_ledger.v1'], published_at: '', notes: '' },
          previous_version: null,
        }
      }
      if (command === 'inspect_core_identity') {
        return {
          action: 'none', bundled_differs: false, missing_capabilities: [], integrity_valid: true,
          current: { version: '0.1.173', algorithm_version: '1.5.0', extension_version: '1.0.0', capabilities: ['account_cost_loss_ledger.v1'], upstream_commit: '29009f0b', sha256: 'active' },
          bundled: { version: '0.1.173', algorithm_version: '1.5.0', extension_version: '1.0.0', capabilities: ['account_cost_loss_ledger.v1'], upstream_commit: '29009f0b', sha256: 'bundled' },
        }
      }
      if (command === 'install_core_update') {
        return { version: '0.1.174', algorithm_version: '1.5.0', extension_version: '1.0.0', restart_required: true }
      }
      return undefined
    })

    const wrapper = mount(DesktopUpdateCenter)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1_800)
    await flushPromises()

    expect(mocks.invoke).toHaveBeenCalledWith('install_core_update')
    expect(mocks.relaunch).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows a deferred pending-core recovery error without preventing the desktop UI from loading', async () => {
    mocks.check.mockResolvedValue(null)
    mocks.invoke.mockImplementation(async (command: string) => {
      if (command === 'desktop_backend_status') {
        return { core_version: '0.1.171', algorithm_version: '1.3.0', upstream_commit: 'f0e7a9c7', core_sha256: 'old' }
      }
      if (command === 'check_core_update') {
        return { available: false, current_version: '0.1.171', current_algorithm_version: '1.3.0', upstream_commit: 'f0e7a9c7', update: null, previous_version: null }
      }
      if (command === 'inspect_core_identity') {
        return {
          action: 'wait_for_compatible_update',
          bundled_differs: true,
          current: { version: '0.1.171', algorithm_version: '1.3.0', upstream_commit: 'f0e7a9c7', sha256: 'old' },
          bundled: { version: '0.1.172', algorithm_version: '1.4.0', upstream_commit: 'desktop123', sha256: 'new' },
          pending: { version: '0.1.172', algorithm_version: '1.4.0', upstream_commit: '155c4949', sha256: 'pending' },
          last_error: '内核更新暂未切换：无法替换活动内核。桌面已继续启动，请稍后重试',
          missing_capabilities: ['account_cost_loss_ledger.v1'],
          integrity_valid: true,
        }
      }
      return undefined
    })

    const wrapper = mount(DesktopUpdateCenter)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1_800)
    await flushPromises()

    expect(wrapper.text()).toContain('内核更新暂未切换')
    expect(wrapper.find('div[role="alert"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
