import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import DesktopBackendGate from '../DesktopBackendGate.vue'

const mocks = vi.hoisted(() => ({ invoke: vi.fn(), listen: vi.fn() }))
vi.mock('@tauri-apps/api/core', () => ({ invoke: mocks.invoke }))
vi.mock('@tauri-apps/api/event', () => ({ listen: mocks.listen }))

const status = (overrides = {}) => ({
  phase: 'waiting_for_dependencies', managed: true, pid: null, port: 18765,
  data_dir: 'fixture', core_version: '0.2.2', algorithm_version: '1.6.0',
  upstream_commit: 'fixture', message: '请启动 Docker Desktop，应用会自动继续启动。',
  last_log: 'dial tcp 127.0.0.1:15432: connection refused',
  problem: { code: 'docker_unavailable', title: 'Docker 尚未启动',
    message: '请启动 Docker Desktop，应用会自动继续启动。', retryable: true },
  ...overrides,
})

describe('DesktopBackendGate recovery', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mocks.invoke.mockReset().mockResolvedValue(status())
    mocks.listen.mockReset().mockResolvedValue(vi.fn())
  })
  afterEach(() => vi.useRealTimers())

  it('explains Docker recovery and keeps raw logs inside optional details', async () => {
    const wrapper = mount(DesktopBackendGate)
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('Docker 尚未启动')
    expect(wrapper.text()).toContain('请启动 Docker Desktop')
    expect(wrapper.get('details').attributes('open')).toBeUndefined()
    expect(wrapper.get('details').text()).toContain('connection refused')
    expect(wrapper.get('button').text()).toContain('重新检测并启动')
    wrapper.unmount()
  })

  it('retries inside the same window and emits ready when dependencies recover', async () => {
    const wrapper = mount(DesktopBackendGate)
    await flushPromises()
    mocks.invoke.mockResolvedValue(status({ phase: 'starting', problem: null }))
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(mocks.invoke).toHaveBeenCalledWith('desktop_backend_start')
    mocks.invoke.mockResolvedValue(status({ phase: 'ready', problem: null }))
    await vi.advanceTimersByTimeAsync(750)
    await flushPromises()
    expect(wrapper.emitted('ready')).toHaveLength(1)
    wrapper.unmount()
  })

  it('ignores a slow status request from before the user retried', async () => {
    const wrapper = mount(DesktopBackendGate)
    await flushPromises()
    let completeOldPoll!: (value: ReturnType<typeof status>) => void
    mocks.invoke.mockReturnValueOnce(new Promise(resolve => { completeOldPoll = resolve }))
    await vi.advanceTimersByTimeAsync(750)
    mocks.invoke.mockResolvedValue(status({ phase: 'starting', problem: null }))
    await wrapper.get('button').trigger('click')
    await flushPromises()
    completeOldPoll(status({ phase: 'error' }))
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('正在启动成本运维内核')
    wrapper.unmount()
  })

  it('keeps polling if the native event subscription is unavailable', async () => {
    mocks.listen.mockRejectedValue(new Error('event channel unavailable'))
    const wrapper = mount(DesktopBackendGate)
    await flushPromises()
    mocks.invoke.mockResolvedValue(status({ phase: 'ready', problem: null }))
    await vi.advanceTimersByTimeAsync(750)
    await flushPromises()
    expect(wrapper.emitted('ready')).toHaveLength(1)
    wrapper.unmount()
  })
})
