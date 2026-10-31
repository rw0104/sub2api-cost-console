import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { GroupPlatform } from '@/types'
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
vi.mock('@/api/url', () => ({
  isDesktopRuntime: nativeMocks.isDesktopRuntime,
  getDesktopGatewayBase: () => 'http://127.0.0.1:18765/v1',
}))
vi.mock('@/api/nativeClientLauncher', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/nativeClientLauncher')>(),
  getNativeWorkingDirectory: nativeMocks.getWorkingDirectory,
  getStoredNativeWorkingDirectory: nativeMocks.getStoredWorkingDirectory,
  pickNativeWorkingDirectory: nativeMocks.pickWorkingDirectory,
  previewNativeClientLaunch: nativeMocks.preview,
  launchNativeClient: nativeMocks.launch,
  storeNativeWorkingDirectory: nativeMocks.storeWorkingDirectory,
}))

const makeKey = (platform: GroupPlatform = 'openai') => ({
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
    expect(wrapper.text()).toContain('model_catalog_url = "http://127.0.0.1:18765/v1/models"')
    expect(wrapper.text()).toContain('[features]\napi_key_model_discovery = true\ngoals = true')
    expect(wrapper.find('input.cost-api-input').exists()).toBe(true)
  })

  // Codex 0.149 起，requires_openai_auth = false 的自定义 Provider 不再读取 auth.json，
  // 只认 env_key 指向的环境变量。示例配置缺少 env_key 会让 Codex 不带密钥访问网关并收到 401。
  it('puts the Codex API key on env_key instead of an ignored auth.json', async () => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey()], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('requires_openai_auth = false')
    expect(text).toContain('env_key = "SUB2API_API_KEY"')
    expect(text).toContain('$env:SUB2API_API_KEY = "<从 API Key 管理页复制>"')
    expect(text).not.toContain('%USERPROFILE%\\.codex\\auth.json')
    expect(text).not.toContain('{ "OPENAI_API_KEY"')
  })

  // Codex 0.166（openai/codex#57840）起，会话只能用创建时记录的 model_provider ID 恢复。
  // 复制的配置必须与桌面启动器（client_launcher.rs 的 model_provider="sub2api"）使用同一 ID，
  // 否则两种入口创建的会话无法互相 resume。
  it('uses the same Codex provider ID as the desktop launcher', async () => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey()], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('model_provider = "sub2api"')
    expect(text).toContain('[model_providers.sub2api]')
    expect(text).not.toContain('Sub2APILocal')
  })

  it('copies the key command for the variable the Codex config reads, and keeps OPENAI_API_KEY for SDK presets', async () => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey()], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()

    const copyKeyButton = () => wrapper.findAll('button').find((button) => button.text().includes('复制密钥命令'))!
    await copyKeyButton().trigger('click')
    expect(mocks.copy).toHaveBeenLastCalledWith('$env:SUB2API_API_KEY = "sk-test-key"', 'PowerShell 密钥命令已复制')

    const pythonPreset = wrapper.findAll('.cost-api-presets button').find((button) => button.text().includes('Python'))!
    await pythonPreset.trigger('click')
    await copyKeyButton().trigger('click')
    expect(mocks.copy).toHaveBeenLastCalledWith('$env:OPENAI_API_KEY = "sk-test-key"', 'PowerShell 密钥命令已复制')
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
    ['anthropic', 'anthropic', '/v1'],
    ['openai', 'openai', '/v1'],
    ['gemini', 'gemini', '/v1beta'],
    ['antigravity', 'antigravity', '/antigravity/v1'],
    ['grok', 'grok', '/v1'],
    ['kimi', 'openai', '/v1'],
    ['zhipu', 'openai', '/v1'],
    ['deepseek', 'openai', '/v1'],
    ['minimax', 'openai', '/v1'],
    ['opencode_go', 'openai', '/v1'],
    ['typesafe', 'anthropic', '/v1'],
    ['command_code', 'openai', '/v1'],
    ['cline', 'openai', '/v1'],
    ['composite', 'composite', '/v1'],
  ] as const)('normalizes the %s group for both OpenCode preview and launch', async (platform, profile, path) => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey(platform)], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()
    await wrapper.get('[role="tablist"] button:nth-child(4)').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="native-preview"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="native-launch"]').trigger('click')
    await flushPromises()

    const request = expect.objectContaining({
      client_id: 'opencode',
      gateway_profile: profile,
      base_url: `http://127.0.0.1:18765${path}`,
      api_key: 'sk-test-key',
    })
    expect(nativeMocks.preview).toHaveBeenCalledWith(request)
    expect(nativeMocks.launch).toHaveBeenCalledWith(request)
    wrapper.unmount()
  })

  it.each([
    ['command_code', 'codex', 2, '/v1'],
    ['cline', 'codex', 2, '/v1'],
    ['command_code', 'claude-code', 3, ''],
    ['cline', 'claude-code', 3, ''],
    ['command_code', 'cursor', 5, '/v1'],
    ['cline', 'cursor', 5, '/v1'],
    ['command_code', 'grok', 6, '/v1'],
    ['cline', 'grok', 6, '/v1'],
  ] as const)('normalizes %s credentials when using %s', async (platform, clientId, tabIndex, path) => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey(platform)], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()
    await wrapper.get(`[role="tablist"] button:nth-child(${tabIndex})`).trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="native-launch"]').trigger('click')
    await flushPromises()

    expect(nativeMocks.launch).toHaveBeenCalledWith(expect.objectContaining({
      client_id: clientId,
      gateway_profile: 'openai',
      base_url: `http://127.0.0.1:18765${path}`,
    }))
    wrapper.unmount()
  })

  it('blocks unknown group protocols before IPC while keeping ChatGPT Desktop independent', async () => {
    mocks.listKeys.mockResolvedValue({ items: [makeKey('future-provider')], total: 1, page: 1, page_size: 100 })
    const wrapper = mount(CostApiAccessPanel, { props: { desktopMode: true } })
    await flushPromises()

    expect(wrapper.text()).toContain('当前分组平台 future-provider 尚不支持原生启动，请更新桌面应用后重试。')
    expect((wrapper.get('[data-test="native-preview"]').element as HTMLButtonElement).disabled).toBe(true)
    expect((wrapper.get('[data-test="native-launch"]').element as HTMLButtonElement).disabled).toBe(true)
    await wrapper.get('[data-test="native-preview"]').trigger('click')
    await wrapper.get('[data-test="native-launch"]').trigger('click')
    expect(nativeMocks.preview).not.toHaveBeenCalled()
    expect(nativeMocks.launch).not.toHaveBeenCalled()

    await wrapper.get('[role="tablist"] button:nth-child(1)').trigger('click')
    await flushPromises()
    expect((wrapper.get('[data-test="native-launch"]').element as HTMLButtonElement).disabled).toBe(false)
    await wrapper.get('[data-test="native-launch"]').trigger('click')
    await flushPromises()
    expect(nativeMocks.launch).toHaveBeenCalledWith(expect.objectContaining({
      client_id: 'chatgpt',
      gateway_profile: 'composite',
    }))
    wrapper.unmount()
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
