import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import PluginsView from '../PluginsView.vue'
import PluginRoutingDialog from '@/components/plugins/PluginRoutingDialog.vue'
import PluginHostDialog from '@/components/plugins/PluginHostDialog.vue'
import PluginInstallDialog from '@/components/plugins/PluginInstallDialog.vue'

const {
  listPlugins,
  getPlugin,
  uploadPlugin,
  inspectPlugin,
  authorizeUpload,
  enablePlugin,
  upgradePlugin,
  rollbackPlugin,
  pluginVersions,
  saveRouting,
  hostStats,
  secretGrants,
  putSecretGrant,
  deleteSecretGrant,
  savePluginConfig,
  loadPluginConfig,
  recoverPluginConfig,
  createUISession,
  stepUpRun,
  showError,
} = vi.hoisted(() => ({
  listPlugins: vi.fn(),
  getPlugin: vi.fn(),
  uploadPlugin: vi.fn(),
  inspectPlugin: vi.fn(),
  authorizeUpload: vi.fn(),
  enablePlugin: vi.fn(),
  upgradePlugin: vi.fn(),
  rollbackPlugin: vi.fn(),
  pluginVersions: vi.fn(),
  saveRouting: vi.fn(),
  hostStats: vi.fn(),
  secretGrants: vi.fn(),
  putSecretGrant: vi.fn(),
  deleteSecretGrant: vi.fn(),
  savePluginConfig: vi.fn(),
  loadPluginConfig: vi.fn(),
  recoverPluginConfig: vi.fn(),
  createUISession: vi.fn(),
  stepUpRun: vi.fn((action: () => Promise<unknown>) => action()),
  showError: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    plugins: {
      list: listPlugins,
      get: getPlugin,
      upload: uploadPlugin,
      inspect: inspectPlugin,
      authorizeUpload,
      enable: enablePlugin,
      upgrade: upgradePlugin,
      rollback: rollbackPlugin,
      versions: pluginVersions,
      saveRouting,
      hostStats,
      secretGrants,
      putSecretGrant,
      deleteSecretGrant,
      disable: vi.fn(),
      remove: vi.fn(),
      getConfig: loadPluginConfig,
      saveConfig: savePluginConfig,
      recoverConfig: recoverPluginConfig,
      test: vi.fn().mockResolvedValue({ success: true, message: 'ok', latency_ms: 1 }),
      createUISession,
    },
  },
}))

vi.mock('@/api/url', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/url')>()),
  buildApiUrl: (path: string) => `http://127.0.0.1:19765${path}`,
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
    showSuccess: vi.fn(),
    showInfo: vi.fn(),
  }),
}))

vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: stepUpRun }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => '',
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, te: () => false }),
}))

const plugin = {
  id: 7,
  plugin_key: 'local.test.transport',
  name: 'Test Transport',
  version: '1.0.0',
  description: '',
  author: 'test',
  manifest: {
    schema_version: 1,
    id: 'local.test.transport',
    name: 'Test Transport',
    version: '1.0.0',
    requires: {
      sub2api: '>=0.1.0',
      plugin_protocol: 1,
      transport_api: 1,
      ui_bridge: 1,
    },
    capabilities: [],
    ui: { entrypoint: 'ui/index.html' },
  },
  binary_sha256: 'a'.repeat(64),
  signature_status: 'trusted' as const,
  state: 'disabled' as const,
  last_error: '',
  installed_at: '2026-08-22T00:00:00Z',
  updated_at: '2026-08-22T00:00:00Z',
  bindings: [
    {
      id: 1,
      plugin_id: 7,
      capability: 'openai.oauth.outbound_transport.v1',
      platform: 'openai',
      account_type: 'oauth',
      enabled: false,
      rollout_percent: 100,
    },
  ],
  compatibility: {
    compatible: true,
    tested: true,
    status: 'compatible' as const,
    message: '',
    current_sub2api_version: '0.1.0',
    required_sub2api_version: '>=0.1.0',
    recommended_sub2api_version: '0.1.0',
    plugin_protocol: 1,
    transport_api: 1,
    ui_bridge: 1,
  },
  runtime_healthy: false,
  runtime_message: '',
}

const mountedViews: ReturnType<typeof mount>[] = []
function mountView() {
  const wrapper = mount(PluginsView, {
    attachTo: document.body,
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        BaseDialog: { name: 'BaseDialog', template: '<div><slot /></div>' },
        Icon: true,
        TotpStepUpDialog: true,
      },
    },
  })
  mountedViews.push(wrapper)
  return wrapper
}

describe('管理员插件页二次验证', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    stepUpRun.mockImplementation((action: () => Promise<unknown>) => action())
    listPlugins.mockResolvedValue([plugin])
    getPlugin.mockImplementation(async () => (await listPlugins())[0])
    uploadPlugin.mockResolvedValue(plugin)
    inspectPlugin.mockResolvedValue({ manifest: plugin.manifest, compatibility: plugin.compatibility, package_sha256: 'c'.repeat(64), signature_status: 'trusted', publisher: { key_id: 'known-publisher', fingerprint: `sha256:${'b'.repeat(64)}` } })
    authorizeUpload.mockResolvedValue(undefined)
    enablePlugin.mockResolvedValue(plugin)
    upgradePlugin.mockResolvedValue(plugin)
    rollbackPlugin.mockResolvedValue(plugin)
    saveRouting.mockResolvedValue(plugin)
    hostStats.mockResolvedValue({ logs: 1, metrics: { requests: 2 }, events_accepted: 1, events_dropped: 0, recent_events: [] })
    secretGrants.mockResolvedValue([{
      plugin_id: 7, capability: 'request.preprocess.v1', alias: 'policy_key',
      expires_at: '2026-09-17T23:00:00Z', updated_at: '2026-09-17T22:00:00Z',
    }])
    putSecretGrant.mockResolvedValue(undefined)
    deleteSecretGrant.mockResolvedValue(undefined)
    pluginVersions.mockResolvedValue([{
      id: 11, plugin_id: plugin.id, version: '0.9.0',
      binary_sha256: 'b'.repeat(64), saved_at: '2026-09-17T12:00:00Z',
      expires_at: '2026-09-18T12:00:00Z', manifest: plugin.manifest, compatibility: plugin.compatibility,
    }])
    savePluginConfig.mockResolvedValue({ enabled: true })
    loadPluginConfig.mockResolvedValue({})
    recoverPluginConfig.mockResolvedValue({ enabled: true })
    createUISession.mockResolvedValue({
      url: '/api/v1/plugin-ui/token/index.html#bridge_token=bridge',
      bridge_token: 'bridge',
      ui_bridge_version: 1,
      expires_at: '2026-08-22T01:00:00Z',
    })
  })

  afterEach(() => {
    mountedViews.splice(0).forEach(wrapper => wrapper.unmount())
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('首次发布者必须确认才安装，并把确认绑定到该包和公钥指纹', async () => {
    const inspection = { manifest: plugin.manifest, compatibility: plugin.compatibility, package_sha256: 'c'.repeat(64), signature_status: 'untrusted', publisher: { key_id: 'new-publisher', fingerprint: `sha256:${'b'.repeat(64)}` } }
    inspectPlugin.mockResolvedValueOnce(inspection)
    const wrapper = mountView()
    await flushPromises()
    const file = new File(['compiled-package'], 'share.s2plugin')
    const input = wrapper.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
    await input.trigger('change')
    await flushPromises()
    expect(inspectPlugin).toHaveBeenCalledWith(file)
    expect(uploadPlugin).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('new-publisher')
    expect(wrapper.get('[data-testid="confirm-publisher-install"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="publisher-consent"]').setValue(true)
    await wrapper.get('[data-testid="confirm-publisher-install"]').trigger('click')
    await flushPromises()
    expect(uploadPlugin).toHaveBeenCalledWith(file, { package_sha256: inspection.package_sha256, publisher_fingerprint: inspection.publisher.fingerprint })
    expect(authorizeUpload).toHaveBeenCalledTimes(2)
    expect(wrapper.getComponent(PluginInstallDialog).props('inspection')).toBeNull()
  })

  it('取消首次发布者确认不会上传或建立信任', async () => {
    inspectPlugin.mockResolvedValueOnce({ manifest: plugin.manifest, compatibility: plugin.compatibility, package_sha256: 'c'.repeat(64), signature_status: 'untrusted', publisher: { key_id: 'new-publisher', fingerprint: `sha256:${'b'.repeat(64)}` } })
    const wrapper = mountView()
    await flushPromises()
    const input = wrapper.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [new File(['package'], 'share.s2plugin')], configurable: true })
    await input.trigger('change')
    await flushPromises()
    wrapper.getComponent(PluginInstallDialog).vm.$emit('close')
    await flushPromises()
    expect(uploadPlugin).not.toHaveBeenCalled()
    expect(wrapper.getComponent(PluginInstallDialog).props('inspection')).toBeNull()
  })

  it('页面卸载后丢弃迟到的包检查，不继续安装', async () => {
    let resolveInspection!: (value: unknown) => void
    inspectPlugin.mockImplementationOnce(() => new Promise(resolve => { resolveInspection = resolve }))
    const wrapper = mountView()
    await flushPromises()
    const input = wrapper.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [new File(['package'], 'share.s2plugin')] })
    await input.trigger('change')
    await flushPromises()
    wrapper.unmount()
    resolveInspection({ manifest: plugin.manifest, compatibility: plugin.compatibility, package_sha256: 'c'.repeat(64), signature_status: 'trusted' })
    await flushPromises()
    expect(uploadPlugin).not.toHaveBeenCalled()
  })

  it('桌面配置 iframe 使用后端地址而不是桌面资源域名', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.plugins.configure')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('iframe').attributes('src')).toBe('http://127.0.0.1:19765/api/v1/plugin-ui/token/index.html#bridge_token=bridge')
    expect(wrapper.get('iframe').attributes('sandbox')).toBe('allow-scripts')
    wrapper.unmount()
  })

  it('空白 iframe 的 load 事件不会被当作插件 ready', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.plugins.configure')!.trigger('click')
    await flushPromises()
    await wrapper.get('iframe').trigger('load')
    expect(wrapper.text()).toContain('admin.plugins.loadingUI')
    wrapper.unmount()
  })

  it('校验 ready 消息来源和 token，加载超时后可重试', async () => {
    const wrapper = mountView()
    await flushPromises()
    vi.useFakeTimers()
    await wrapper.findAll('button').find(b => b.text() === 'admin.plugins.configure')!.trigger('click')
    await flushPromises()
    const frame = wrapper.get('iframe').element as HTMLIFrameElement
    const data = { source: 'sub2api-plugin-ui', type: 'sub2api.plugin.ready', bridge_token: 'bridge' }
    window.dispatchEvent(new MessageEvent('message', { origin: 'null', source: window, data }))
    window.dispatchEvent(new MessageEvent('message', { origin: 'null', source: frame.contentWindow, data: { ...data, bridge_token: 'wrong' } }))
    await flushPromises()
    expect(wrapper.text()).toContain('admin.plugins.loadingUI')
    await vi.advanceTimersByTimeAsync(15_001)
    expect(wrapper.get('[role="alert"]').text()).toContain('admin.plugins.uiReadyTimeout')
    await wrapper.findAll('button').find(b => b.text() === 'admin.plugins.retryUI')!.trigger('click')
    await flushPromises()
    expect(createUISession).toHaveBeenCalledTimes(2)
    const retry = wrapper.get('iframe').element as HTMLIFrameElement
    window.dispatchEvent(new MessageEvent('message', { origin: 'null', source: retry.contentWindow, data }))
    await flushPromises()
    expect(wrapper.text()).not.toContain('admin.plugins.loadingUI')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(16_000)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('解释 v2 字段不被当前内核识别并保留插件包校验', async () => {
    inspectPlugin.mockRejectedValueOnce(new Error('解析插件清单: json: unknown field "failure_mode"'))
    const wrapper = mountView()
    await flushPromises()
    const input = wrapper.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [new File(['package'], 'example.s2plugin')] })
    await input.trigger('change')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('admin.plugins.v2HostRequired')
    expect(uploadPlugin).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('旧分享包通过宿主确认重新配置，读取失败不自动保存或恢复', async () => {
    loadPluginConfig.mockRejectedValueOnce({reason:'PLUGIN_CONFIG_UNREADABLE',message:'原配置已保留',metadata:{config_digest:'a'.repeat(64)}})
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.plugins.configure')!.trigger('click')
    await flushPromises()
    const frame = wrapper.get('iframe').element as HTMLIFrameElement
    const send = async (type: string, id: string, config?: object) => {
      window.dispatchEvent(new MessageEvent('message', {origin:'null',source:frame.contentWindow,
        data:{source:'sub2api-plugin-ui',bridge_token:'bridge',type,request_id:id,config}}))
      await flushPromises()
    }
    await send('config.load', 'load-original')
    expect(wrapper.text()).toContain('admin.plugins.configUnreadable')
    expect(savePluginConfig).not.toHaveBeenCalled()
    expect(recoverPluginConfig).not.toHaveBeenCalled()
    await send('config.save', 'unconfirmed', {enabled:true})
    expect(recoverPluginConfig).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="confirm-config-recovery"]').setValue(true)
    await send('config.save', 'confirmed', {enabled:true})
    expect(recoverPluginConfig).toHaveBeenCalledWith(7, {enabled:true}, 'a'.repeat(64))
    expect(savePluginConfig).not.toHaveBeenCalled()
    expect(stepUpRun).toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('admin.plugins.configUnreadable')
  })

  it('切换插件时丢弃迟到的配置解密失败和确认', async () => {
    let rejectLoad!: (error: unknown) => void
    loadPluginConfig.mockImplementationOnce(() => new Promise((_,reject) => {rejectLoad=reject}))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.plugins.configure')!.trigger('click')
    await flushPromises()
    const frame=wrapper.get('iframe').element as HTMLIFrameElement
    window.dispatchEvent(new MessageEvent('message',{origin:'null',source:frame.contentWindow,
      data:{source:'sub2api-plugin-ui',bridge_token:'bridge',type:'config.load',request_id:'late-load'}}))
    await flushPromises()
    wrapper.getComponent({name:'BaseDialog'}).vm.$emit('close')
    rejectLoad({reason:'PLUGIN_CONFIG_UNREADABLE',metadata:{config_digest:'a'.repeat(64)}})
    await flushPromises()
    expect(wrapper.text()).not.toContain('admin.plugins.configUnreadable')
    expect(recoverPluginConfig).not.toHaveBeenCalled()
  })

  it('关闭对话框后丢弃迟到的会话，避免重新创建 iframe', async () => {
    let resolveSession!: (value: unknown) => void
    createUISession.mockImplementationOnce(() => new Promise(resolve => { resolveSession = resolve }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.plugins.configure')!.trigger('click')
    wrapper.getComponent({ name: 'BaseDialog' }).vm.$emit('close')
    resolveSession({url:'/api/v1/plugin-ui/late/index.html#bridge_token=late',bridge_token:'late'})
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(false)
  })

  it('多个插件默认折叠详情，可搜索和按状态筛选', async () => {
    listPlugins.mockResolvedValue(Array.from({ length: 12 }, (_, index) => ({
      ...plugin, id: index + 1, name: `Plugin ${index + 1}`, plugin_key: `example.plugin-${index + 1}`,
      state: index % 2 ? 'enabled' : 'disabled',
    })))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(12)
    expect(wrapper.get('#plugin-details-1').isVisible()).toBe(false)
    await wrapper.findAll('button').find(b => b.text() === 'admin.plugins.showDetails')!.trigger('click')
    expect(wrapper.get('#plugin-details-1').isVisible()).toBe(true)
    expect(wrapper.get('#plugin-details-2').isVisible()).toBe(false)
    await wrapper.get('input[type="search"]').setValue('example.plugin-12')
    expect(wrapper.findAll('article')).toHaveLength(1)
    await wrapper.get('select[aria-label="admin.plugins.filterState"]').setValue('disabled')
    expect(wrapper.findAll('article')).toHaveLength(0)
    expect(wrapper.text()).toContain('admin.plugins.noMatches')
  })

  it('秘密授权经二次验证提交，清空输入且撤销只发送别名', async () => {
    listPlugins.mockResolvedValue([{
      ...plugin, manifest: { ...plugin.manifest, schema_version: 2, capabilities: [{
        id: 'request.preprocess.v1', permissions: ['request.metadata.read', 'secrets.broker'],
        platform: 'openai', account_type: 'oauth', kind: 'hook', timeout_ms: 1000, failure_mode: 'fail_closed',
      }] },
    }])
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const wrapper = mountView()
    await flushPromises()
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.plugins.hostServices')
    await button!.trigger('click')
    await flushPromises()
    expect(hostStats).toHaveBeenCalledWith(7)
    expect(secretGrants).toHaveBeenCalledWith(7)
    const dialog = wrapper.getComponent(PluginHostDialog)
    await dialog.get('input[type="text"]').setValue('policy_key')
    await dialog.get('input[type="password"]').setValue('private-secret-marker')
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(putSecretGrant).toHaveBeenCalledWith(7, 'request.preprocess.v1', 'policy_key', 'private-secret-marker', 900)
    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect((dialog.get('input[type="password"]').element as HTMLInputElement).value).toBe('')
    expect(dialog.text()).not.toContain('private-secret-marker')
    const revoke = dialog.findAll('button').find(item => item.text() === 'admin.plugins.revokeSecret')
    await revoke!.trigger('click')
    await flushPromises()
    expect(deleteSecretGrant).toHaveBeenCalledWith(7, 'request.preprocess.v1', 'policy_key')
    expect(stepUpRun).toHaveBeenCalledTimes(2)
  })

  it('隔离模式和运行说明同时展示', async () => {
    listPlugins.mockResolvedValue([{ ...plugin, runtime_healthy: true, runtime_isolation: 'container', runtime_message: 'Sandbox active' }])
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.plugins.isolation_container')
    expect(wrapper.text()).toContain('Sandbox active')
  })

  it('路由编辑校验 ID 并带版本戳和二次验证保存', async () => {
    listPlugins.mockResolvedValue([{
      ...plugin,
      manifest: { ...plugin.manifest, schema_version: 2, capabilities: [{
        id: 'request.preprocess.v1', kind: 'hook', platform: 'openai', account_type: 'oauth',
        timeout_ms: 200, permissions: ['request.metadata.read'], failure_mode: 'fail_closed',
      }] },
      bindings: [{ ...plugin.bindings[0], capability: 'request.preprocess.v1' }],
    }])
    const wrapper = mountView()
    await flushPromises()
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.plugins.routing')
    await button!.trigger('click')
    await flushPromises()
    const dialog = wrapper.getComponent(PluginRoutingDialog)
    const numbers = dialog.findAll('input[type="number"]')
    await numbers[0].setValue('75')
    await numbers[2].setValue('2')
    await numbers[3].setValue('10')
    const scopes = dialog.findAll('input[type="text"]')
    await scopes[0].setValue('11, 11')
    await dialog.get('form').trigger('submit')
    expect(saveRouting).not.toHaveBeenCalled()
    expect(dialog.text()).toContain('admin.plugins.invalidIDs')
    await scopes[0].setValue('11, 10')
    await scopes[1].setValue('7')
    await scopes[2].setValue('9')
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(saveRouting).toHaveBeenCalledWith(7, [{
      capability: 'request.preprocess.v1', priority: 75, rollout_percent: 100, max_concurrency: 2, timeout_ms: 10,
      account_ids: [10, 11], user_ids: [7], group_ids: [9],
    }], plugin.updated_at)
  })

  it('启用 v2 时保留零灰度，不会默认为全量', async () => {
    listPlugins.mockResolvedValue([{
      ...plugin, manifest: { ...plugin.manifest, schema_version: 2 },
      bindings: [{ ...plugin.bindings[0], capability: 'request.preprocess.v1', rollout_percent: 0 }],
    }])
    const wrapper = mountView()
    await flushPromises()
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.plugins.enable')
    await button!.trigger('click')
    await flushPromises()
    expect(enablePlugin).toHaveBeenCalledWith(7, 0, false)
  })

  it('升级指定插件需要确认并通过 step-up 执行', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const wrapper = mountView()
    await flushPromises()
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.plugins.upgrade')
    await button!.trigger('click')
    const input = wrapper.findAll('input[type="file"]')[1]
    const file = new File(['new version'], 'policy.s2plugin')
    Object.defineProperty(input.element, 'files', { configurable: true, value: [file] })
    await input.trigger('change')
    await flushPromises()
    expect(window.confirm).toHaveBeenCalledWith('admin.plugins.confirmUpgrade')
    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(upgradePlugin).toHaveBeenCalledWith(plugin.id, file, true)
  })

  it('版本记录按选定快照执行回滚并保持二次验证', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const wrapper = mountView()
    await flushPromises()
    const historyButton = wrapper.findAll('button').find(item => item.text() === 'admin.plugins.versions')
    await historyButton!.trigger('click')
    await flushPromises()
    expect(pluginVersions).toHaveBeenCalledWith(plugin.id)
    expect(wrapper.text()).toContain('v0.9.0')
    const rollbackButton = wrapper.findAll('button').find(item => item.text() === 'admin.plugins.rollback')
    await rollbackButton!.trigger('click')
    await flushPromises()
    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(rollbackPlugin).toHaveBeenCalledWith(plugin.id, 11, false)
  })

  it('启用插件通过 step-up 控制器执行', async () => {
    const wrapper = mountView()
    await flushPromises()

    const button = wrapper.findAll('button').find((item) => item.text().includes('admin.plugins.enable'))
    expect(button).toBeDefined()
    await button!.trigger('click')
    await flushPromises()

    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(enablePlugin).toHaveBeenCalledWith(7, 100, false)
  })

  it('上传插件通过 step-up 控制器执行', async () => {
    const wrapper = mountView()
    await flushPromises()
    const input = wrapper.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', {
      configurable: true,
      value: [new File(['plugin'], 'transport.s2plugin', { type: 'application/zip' })],
    })

    await input.trigger('change')
    await flushPromises()

    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(uploadPlugin).toHaveBeenCalledTimes(1)
    expect(authorizeUpload).toHaveBeenCalledTimes(1)
    expect(authorizeUpload.mock.invocationCallOrder[0]).toBeLessThan(uploadPlugin.mock.invocationCallOrder[0])
  })

  it('上传前验证未完成时不会传输插件包', async () => {
    authorizeUpload.mockRejectedValue({ code: 'STEP_UP_REQUIRED', status: 403 })
    const wrapper = mountView()
    await flushPromises()
    const input = wrapper.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', {
      configurable: true,
      value: [new File(['plugin'], 'transport.s2plugin', { type: 'application/zip' })],
    })
    await input.trigger('change')
    await flushPromises()
    expect(authorizeUpload).toHaveBeenCalledTimes(1)
    expect(uploadPlugin).not.toHaveBeenCalled()
  })

  it('展示 v2 能力权限和失败策略，并使用通用绑定的灰度值', async () => {
    listPlugins.mockResolvedValue([{
      ...plugin,
      manifest: {
        ...plugin.manifest,
        schema_version: 2,
        capabilities: [{
          id: 'request.preprocess.v1', platform: 'openai', account_type: 'oauth', kind: 'hook',
          permissions: ['request.metadata.read', 'request.body.read', 'request.mutate'],
          timeout_ms: 200, failure_mode: 'fail_closed', synchronous: true,
        }],
      },
      bindings: [{ ...plugin.bindings[0], capability: 'request.preprocess.v1', rollout_percent: 25 }],
      capability_runtime: [{
        capability: 'request.preprocess.v1', healthy: false, calls: 3, errors: 3, denied: 0,
        in_flight: 0, concurrency_limit: 32, circuit_open: true, message: '',
      }],
    }])
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('request.preprocess.v1')
    expect(wrapper.text()).toContain('request.body.read')
    expect(wrapper.text()).toContain('200 ms')
    expect(wrapper.text()).toContain('admin.plugins.fail_closed')
    expect(wrapper.text()).toContain('admin.plugins.circuitOpen')
    expect((wrapper.get('input[type="range"]').element as HTMLInputElement).value).toBe('25')
    const button = wrapper.findAll('button').find(item => item.text().includes('admin.plugins.enable'))
    await button!.trigger('click')
    await flushPromises()
    expect(enablePlugin).toHaveBeenCalledWith(7, 25, false)
  })
})
