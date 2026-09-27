import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'
import enAccounts from '@/i18n/locales/en/admin/accounts'
import zhAccounts from '@/i18n/locales/zh/admin/accounts'

const { listAccounts, listPlugins, pluginStatus, translate } = vi.hoisted(() => ({
  listAccounts: vi.fn(), listPlugins: vi.fn(), pluginStatus: vi.fn(), translate: vi.fn()
}))

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: translate })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      getBatchTodayStats: vi.fn().mockResolvedValue({ stats: {} }),
      getUpstreamBillingProbeSettings: vi.fn().mockResolvedValue({ enabled: true })
    },
    plugins: { list: listPlugins, status: pluginStatus },
    proxies: { getAll: vi.fn().mockResolvedValue([]) },
    groups: { getAll: vi.fn().mockResolvedValue([]) }
  }
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showWarning: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() })
}))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token', isSimpleMode: false })
}))

const HEADER_PROBE = 'openai.oauth.request_header_probe.v1'
const LEGACY_TRANSPORT = 'openai.oauth.protection_transport.v1'
const installation = (capability: string, id = 'header-probe') => ({
  id, state: 'enabled', manifest: { capabilities: [{ id: capability }] }
})
const observedSession = {
  account_id: 42,
  header_present: true,
  parsed: true,
  observed_length: 780,
  observed_blocks: 33,
  phase: 'observed',
  last_probe: '2026-09-27T08:00:00Z',
  requests: 4
}
let wrapper: VueWrapper | undefined

async function renderSession(session: Record<string, unknown> = observedSession, locale = 'zh', envelope = 'payload') {
  // Use the actual locale strings; the test runner aliases vue-i18n to its
  // compiler-free runtime, so interpolate these simple named placeholders here.
  const messages: Record<string, string> = (locale === 'zh' ? zhAccounts : enAccounts).accounts.protectionTransport
  translate.mockImplementation((key: string, params: Record<string, unknown> = {}) => {
    const message = messages[key.replace('admin.accounts.protectionTransport.', '')] ?? key
    return message.replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? `{${name}}`))
  })
  const coreReport = { schema: 1, mode: 'observe', sessions: [session] }
  const status = envelope === 'payload' ? { payload: { core_report: coreReport } }
    : envelope === 'core_report' ? { core_report: coreReport } : coreReport
  pluginStatus.mockResolvedValue({ status_json: JSON.stringify(status) })
  wrapper = mount(AccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="table" /></div>' },
        DataTable: {
          props: ['data'],
          template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-status" :row="row" /></div></div>'
        },
        AccountTableActions: true, AccountTableFilters: true, AccountBulkActionsBar: true,
        Pagination: true, ConfirmDialog: true, AccountActionMenu: true, ImportDataModal: true,
        ReAuthAccountModal: true, AccountTestModal: true, AccountStatsModal: true,
        ScheduledTestsPanel: true, SyncFromCrsModal: true, TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true, TLSFingerprintProfilesModal: true,
        CreateAccountModal: true, EditAccountModal: true, BulkEditAccountModal: true,
        AccountStatusIndicator: true, HelpTooltip: true, Icon: true, Teleport: true
      }
    }
  })
  await flushPromises()
  return wrapper.get('[data-testid="account-protection-transport"]')
}

describe('AccountsView request header probe badge', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset().mockResolvedValue({
      items: [{ id: 42, name: 'OAuth account', platform: 'openai', type: 'oauth', status: 'active',
        schedulable: true, concurrency: 2, priority: 1, group_ids: [], extra: {}, credentials: {} }],
      total: 1, page: 1, page_size: 20, pages: 1
    })
    listPlugins.mockReset().mockResolvedValue([installation(HEADER_PROBE)])
    pluginStatus.mockReset()
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
  })

  it.each([['zh', '780 B · 33 块'], ['en', '780 B · 33 blocks']])(
    'renders observation counts with a success badge in %s', async (locale, label) => {
      const badge = await renderSession(observedSession, locale)
      expect(badge.text()).toBe(label)
      expect(badge.classes()).toContain('text-emerald-700')
      expect(badge.attributes('title')).toContain(label)
      expect(pluginStatus).toHaveBeenCalledWith('header-probe')
    }
  )

  it.each(['payload', 'core_report', 'bare'])('reads the %s status envelope', async envelope => {
    expect((await renderSession(observedSession, 'zh', envelope)).text()).toBe('780 B · 33 块')
  })

  it.each([
    { phase: 'unobserved', header_present: false, observed_length: 0, observed_blocks: 0 },
    { phase: 'unobserved', header_present: false, observed_length: 780, observed_blocks: 33 },
    { phase: 'observed', header_present: false, observed_length: 780, observed_blocks: 33 }
  ])('honors explicit absent-header status over numeric counters: %j', async session => {
    const badge = await renderSession({ ...observedSession, ...session })
    expect(badge.text()).toBe('尚未观测到请求头')
    expect(badge.classes()).toContain('text-gray-500')
    expect(badge.classes()).not.toContain('text-emerald-700')
  })

  it.each([
    [{ observed_length: undefined }, '33 块'],
    [{ observed_blocks: undefined }, '780 B']
  ])('does not add placeholders for an omitted observed field', async (missing, label) => {
    const badge = await renderSession({ ...observedSession, ...missing })
    expect(badge.text()).toBe(label)
    expect(badge.classes()).toContain('text-emerald-700')
  })

  it('keeps malformed headers as a warning with their diagnostic', async () => {
    const badge = await renderSession({ ...observedSession, phase: 'invalid', parsed: false, diagnostic: 'invalid encoding' })
    expect(badge.text()).toBe('780 B · 33 块')
    expect(badge.classes()).toContain('text-amber-700')
    expect(badge.attributes('title')).toContain('invalid encoding')
  })

  it('does not mark contradictory presence and diagnostic data as successful', async () => {
    const badge = await renderSession({ ...observedSession, header_present: false, diagnostic: 'inconsistent probe data' })
    expect(badge.classes()).toContain('text-amber-700')
    expect(badge.attributes('title')).toContain('inconsistent probe data')
  })

  it.each(['auth_blocked', 'access_paused', 'rate_limited', 'error', 'failed'])(
    'keeps %s visibly failed even if header_present is false', async phase => {
      const badge = await renderSession({ ...observedSession, header_present: false, phase, diagnostic: 'probe unavailable' })
      expect(badge.classes()).toContain('text-rose-700')
      expect(badge.attributes('title')).toContain('probe unavailable')
      expect(badge.text()).not.toBe('尚未观测到请求头')
    }
  )

  it('ignores an enabled plugin without an observer capability', async () => {
    listPlugins.mockResolvedValue([installation('unrelated.capability')])
    const badge = await renderSession()
    expect(badge.text()).toBe('尚未观测到请求头')
    expect(badge.classes()).toContain('text-gray-500')
    expect(pluginStatus).not.toHaveBeenCalled()
  })

  it('prefers the dedicated probe when legacy transport is also enabled', async () => {
    listPlugins.mockResolvedValue([installation(LEGACY_TRANSPORT, 'legacy'), installation(HEADER_PROBE)])
    await renderSession()
    expect(pluginStatus).toHaveBeenCalledWith('header-probe')
  })

  it.each([LEGACY_TRANSPORT, HEADER_PROBE])('preserves expected target comparisons for %s', async capability => {
    listPlugins.mockResolvedValue([installation(capability)])
    const badge = await renderSession({ ...observedSession, expected_length: 800, expected_blocks: 34 })
    expect(badge.text()).toBe('780/800 B · 33/34 块')
    expect(badge.classes()).toContain('text-amber-700')
  })

  it('preserves legacy placeholder semantics without assuming a probe from missing targets', async () => {
    listPlugins.mockResolvedValue([installation(LEGACY_TRANSPORT)])
    const badge = await renderSession(observedSession)
    expect(badge.text()).toBe('780/— B · 33/— 块')
    expect(badge.classes()).toContain('text-amber-700')
  })
})
