import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CostApiAccessPanel from '../CostApiAccessPanel.vue'

const mocks = vi.hoisted(() => ({
  listKeys: vi.fn(),
  getPublicSettings: vi.fn(),
  push: vi.fn(),
  copy: vi.fn(),
}))
const nativeMocks = vi.hoisted(() => ({
  isDesktopRuntime: vi.fn(),
  getWorkingDirectory: vi.fn(),
  pickWorkingDirectory: vi.fn(),
  getStoredWorkingDirectory: vi.fn((clientId: string) => localStorage.getItem(`sub2api.nativeClient.workingDirectory.${clientId}`) || localStorage.getItem('sub2api.nativeClient.workingDirectory') || ''),
  storeWorkingDirectory: vi.fn((clientId: string, directory: string) => {
    if (directory.trim() && directory.trim() !== '.') localStorage.setItem(`sub2api.nativeClient.workingDirectory.${clientId}`, directory.trim())
  }),
  preview: vi.fn(),
  launch: vi.fn(),
}))

vi.mock('@/api', () => ({
  keysAPI: { list: mocks.listKeys },
  authAPI: { getPublicSettings: mocks.getPublicSettings },
}))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: mocks.copy }),
}))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }) }))
vi.mock('@/api/url', () => ({ isDesktopRuntime: nativeMocks.isDesktopRuntime }))
vi.mock('@/api/nativeClientLauncher', () => ({
  getNativeWorkingDirectory: nativeMocks.getWorkingDirectory,
  getStoredNativeWorkingDirectory: nativeMocks.getStoredWorkingDirectory,
  pickNativeWorkingDirectory: nativeMocks.pickWorkingDirectory,
  previewNativeClientLaunch: nativeMocks.preview,
  launchNativeClient: nativeMocks.launch,
  storeNativeWorkingDirectory: nativeMocks.storeWorkingDirectory,
}))

const makeKey = (platform: 'anthropic' | 'openai' | 'gemini' | 'antigravity' | 'grok' | 'composite' = 'openai') => ({
  id: 7,
  user_id: 1,
  key: 'sk-test-key',
  name: '测试密钥',
  group_id: 9,
  status: 'active',
  ip_whitelist: [],
  ip_blacklist: [],
  last_used_at: null,
  last_used_ip: null,
  quota: 0,
  quota_used: 0,
  expires_at: null,
  created_at: '',
  updated_at: '',
  current_concurrency: 0,
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  usage_5h: 0,
  usage_1d: 0,
  usage_7d: 0,
  window_5h_start: null,
  window_1d_start: null,
  window_7d_start: null,
  reset_5h_at: null,
  reset_1d_at: null,
  reset_7d_at: null,
  group: { name: `${platform} 分组`, platform },
})

describe('CostApiAccessPanel data truthfulness', () => {
  beforeEach(() => {
    localStorage.clear()
    mocks.listKeys.mockReset()
    mocks.getPublicSettings.mockReset().mockResolvedValue({ api_base_url: '' })
    nativeMocks.isDesktopRuntime.mockReset().mockReturnValue(true)
    nativeMocks.getWorkingDirectory.mockReset().mockResolvedValue('C:\\Users\\reki')
    nativeMocks.pickWorkingDirectory.mockReset().mockResolvedValue(null)
    nativeMocks.getStoredWorkingDirectory.mockClear()
    nativeMocks.storeWorkingDirectory.mockClear()
    nativeMocks.preview.mockReset().mockResolvedValue({
      available: true,
      message: '客户端已就绪',
      executable: 'C:\\tools\\client.cmd',
      working_directory: 'C:\\Users\\reki',
    })
    nativeMocks.launch.mockReset().mockResolvedValue({ message: '客户端已启动' })
  })

  it('does not describe an API failure as an empty key list', async () => {
    mocks.listKeys.mockRejectedValue(new Error('401 Unauthorized'))
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('API Key 无数据：401 Unauthorized')
    expect(wrapper.text()).toContain('这不是“没有密钥”')
    expect(wrapper.text()).not.toContain('当前确实没有密钥')
  })

  it('shows an empty state only after a successful empty response', async () => {
    mocks.listKeys.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()

    expect(wrapper.text()).toContain('API Key 清单读取成功，但当前确实没有密钥')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('shows the native launcher in the desktop API access panel', async () => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey()], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()

    expect(wrapper.text()).toContain('启动本机客户端')
    expect(wrapper.text()).toContain('启动 Codex CLI')
    expect(wrapper.find('input.cost-api-input').exists()).toBe(true)
  })

  it('shows the detected directory and applies a directory selected by the user', async () => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey()], total: 1, page: 1, page_size: 100 })
    nativeMocks.pickWorkingDirectory.mockResolvedValue('D:\\Projects\\demo')
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()

    const input = wrapper.get('input.cost-api-input').element as HTMLInputElement
    expect(input.value).toBe('C:\\Users\\reki')
    expect(wrapper.text()).toContain('检测到的当前目录：C:\\Users\\reki')

    await wrapper.get('.cost-api-directory-button').trigger('click')
    await flushPromises()
    expect((wrapper.get('input.cost-api-input').element as HTMLInputElement).value).toBe('D:\\Projects\\demo')
    expect(localStorage.getItem('sub2api.nativeClient.workingDirectory.codex')).toBe('D:\\Projects\\demo')

    await wrapper.get('.cost-api-presets button:nth-child(4)').trigger('click')
    await flushPromises()
    expect((wrapper.get('input.cost-api-input').element as HTMLInputElement).value).toBe('C:\\Users\\reki')

    await wrapper.get('.cost-api-presets button:nth-child(2)').trigger('click')
    await flushPromises()
    expect((wrapper.get('input.cost-api-input').element as HTMLInputElement).value).toBe('D:\\Projects\\demo')

    wrapper.unmount()
    const reopened = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()
    expect((reopened.get('input.cost-api-input').element as HTMLInputElement).value).toBe('D:\\Projects\\demo')
  })

  it('keeps ChatGPT Desktop separate from the Codex CLI and does not require an API key', async () => {
    mocks.listKeys.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()
    await wrapper.get('[role="tablist"] button:nth-child(1)').trigger('click')

    expect(wrapper.text()).toContain('启动 ChatGPT Desktop')
    expect(wrapper.text()).not.toContain('启动 Codex CLI')
    const launchButton = wrapper.get('.cost-api-native-launcher button.cost-api-button--primary')
    expect((launchButton.element as HTMLButtonElement).disabled).toBe(false)
    await launchButton.trigger('click')

    expect(nativeMocks.launch).toHaveBeenCalledWith(expect.objectContaining({
      client_id: 'chatgpt',
      api_key: '',
    }))
  })

  it('persists a directory typed directly into the field', async () => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey()], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()

    const input = wrapper.get('input.cost-api-input')
    await input.setValue('E:\\Workspaces\\sub2api')
    expect(localStorage.getItem('sub2api.nativeClient.workingDirectory.codex')).toBe('E:\\Workspaces\\sub2api')

    wrapper.unmount()
    const reopened = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()
    expect((reopened.get('input.cost-api-input').element as HTMLInputElement).value).toBe('E:\\Workspaces\\sub2api')
  })

  it('passes the selected Grok route and normalized v1 endpoint to the native launcher', async () => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey('grok')], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()
    await wrapper.get('[role="tablist"] button:nth-child(6)').trigger('click')
    await wrapper.get('[data-test="native-preview"]').trigger('click')

    expect(nativeMocks.preview).toHaveBeenCalledWith(expect.objectContaining({
      client_id: 'grok',
      gateway_profile: 'grok',
      base_url: 'http://127.0.0.1:18765/v1',
      api_key: 'sk-test-key',
    }))
  })

  it('passes the selected Gemini profile and v1beta endpoint to OpenCode', async () => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey('gemini')], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()
    await wrapper.get('[role="tablist"] button:nth-child(4)').trigger('click')
    await wrapper.get('.cost-api-native-launcher button.cost-api-button--primary').trigger('click')

    expect(nativeMocks.launch).toHaveBeenCalledWith(expect.objectContaining({
      client_id: 'opencode',
      gateway_profile: 'gemini',
      base_url: 'http://127.0.0.1:18765/v1beta',
      api_key: 'sk-test-key',
    }))
  })

  it.each([
    ['claude-code', 3, 'anthropic', 'http://127.0.0.1:18765'],
    ['cursor', 5, 'openai', 'http://127.0.0.1:18765/v1'],
  ] as const)('maps the %s client to its process-scoped gateway request', async (clientId, tabIndex, platform, baseUrl) => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey(platform)], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()
    await wrapper.get(`[role="tablist"] button:nth-child(${tabIndex})`).trigger('click')
    await wrapper.get('.cost-api-native-launcher button.cost-api-button--primary').trigger('click')

    expect(nativeMocks.launch).toHaveBeenCalledWith(expect.objectContaining({
      client_id: clientId,
      gateway_profile: platform,
      base_url: baseUrl,
      api_key: 'sk-test-key',
    }))
  })

  it('does not expose native launch controls outside the desktop runtime', async () => {
    nativeMocks.isDesktopRuntime.mockReturnValue(false)
    mocks.listKeys.mockResolvedValue({ items: [makeKey()], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: false } })
    await flushPromises()

    expect(wrapper.text()).not.toContain('启动本机客户端')
    expect(wrapper.find('input.cost-api-input').exists()).toBe(false)
  })
})
