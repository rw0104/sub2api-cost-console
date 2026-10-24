import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DesktopListenSettings from '../DesktopListenSettings.vue'

const mocks = vi.hoisted(() => ({ invoke: vi.fn(), relaunch: vi.fn() }))
vi.mock('@tauri-apps/api/core', () => ({ invoke: mocks.invoke }))
vi.mock('@tauri-apps/plugin-process', () => ({ relaunch: mocks.relaunch }))

const current = {
  host: '127.0.0.1',
  port: 18765,
  active_host: '127.0.0.1',
  active_port: 18765,
  default_port: 18765,
  editable: true,
  restart_required: false,
}

describe('DesktopListenSettings', () => {
  beforeEach(() => {
    mocks.invoke.mockReset()
    mocks.relaunch.mockReset()
  })

  it('saves a new address, warns about LAN exposure and restarts on request', async () => {
    mocks.invoke.mockImplementation(async (command: string, args?: any) => {
      if (command === 'desktop_listen_settings') return current
      if (command === 'desktop_listen_settings_save') return { ...current, ...args, restart_required: true }
      return undefined
    })
    const wrapper = mount(DesktopListenSettings)
    await flushPromises()
    expect(wrapper.text()).toContain('127.0.0.1:18765')
    expect(wrapper.find('.desktop-listen__warning').exists()).toBe(false)

    await wrapper.find('select').setValue('0.0.0.0')
    await wrapper.find('input').setValue('28000')
    expect(wrapper.find('.desktop-listen__warning').exists()).toBe(true)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.invoke).toHaveBeenCalledWith('desktop_listen_settings_save', { host: '0.0.0.0', port: 28000 })
    expect(wrapper.text()).toContain('已保存 0.0.0.0:28000')

    await wrapper.find('.desktop-listen__restart button').trigger('click')
    await flushPromises()
    expect(mocks.invoke).toHaveBeenCalledWith('desktop_backend_prepare_relaunch')
    expect(mocks.relaunch).toHaveBeenCalled()
  })

  it('shows the shell validation error and stays hidden when unsupported', async () => {
    mocks.invoke.mockImplementation(async (command: string) => {
      if (command === 'desktop_listen_settings') return current
      throw new Error('端口 28000 已被其他程序占用，请换一个端口')
    })
    const wrapper = mount(DesktopListenSettings)
    await flushPromises()
    await wrapper.find('input').setValue('28000')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toContain('已被其他程序占用')

    await wrapper.find('select').setValue('custom')
    const [customHost, port] = wrapper.findAll('input')
    await customHost.setValue('192.168.1.10')
    await port.setValue('abc')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toContain('端口必须是')
    expect(mocks.invoke).not.toHaveBeenCalledWith('desktop_listen_settings_save', expect.objectContaining({ host: '192.168.1.10' }))

    mocks.invoke.mockRejectedValue(new Error('unknown command'))
    const hidden = mount(DesktopListenSettings)
    await flushPromises()
    expect(hidden.find('.desktop-listen').exists()).toBe(false)
  })
})
