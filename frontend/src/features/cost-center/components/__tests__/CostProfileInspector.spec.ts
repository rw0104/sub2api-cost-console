import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { Account } from '@/types'
import CostProfileInspector from '../CostProfileInspector.vue'

const JOINED_AT = '2026-08-01T00:00:00.000Z'

function makeAccount(overrides: Partial<Account> = {}): Account {
  return {
    id: 1,
    name: 'upstream',
    platform: 'openai',
    type: 'apikey',
    credentials: {},
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    error_message: null,
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: false,
    created_at: JOINED_AT,
    updated_at: JOINED_AT,
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
    ...overrides,
  } as Account
}

function mountInspector(account: Account) {
  return mount(CostProfileInspector, {
    props: {
      account,
      saving: false,
      now: new Date('2026-08-06T12:00:00.000Z'),
    },
  })
}

describe('CostProfileInspector billing modes', () => {
  it('shows official DeepSeek API keys as automatic metered billing', () => {
    const wrapper = mountInspector(makeAccount({
      name: 'deepseek',
      credentials: { base_url: 'https://api.deepseek.com/v1' },
    }))

    expect(wrapper.text()).toContain('API 按量成本')
    expect(wrapper.text()).toContain('DeepSeek 官方')
    expect(wrapper.text()).toContain('无需填写采购金额')
    expect(wrapper.text()).toContain('输入 Token + 缓存命中 + 输出 Token')
    expect(wrapper.find('#cost-amount').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('应用美国官方默认价')
    expect(wrapper.text()).not.toContain('保存成本档案')
  })

  it('keeps subscription procurement fields for OAuth accounts', () => {
    const wrapper = mountInspector(makeAccount({
      name: 'codex',
      type: 'oauth',
      extra: { plan_type: 'plus' },
    }))

    expect(wrapper.text()).toContain('号码成本配置')
    expect(wrapper.text()).toContain('应用美国官方默认价')
    expect(wrapper.find('#cost-amount').exists()).toBe(true)
    expect(wrapper.text()).toContain('保存成本档案')
  })

  it('does not present a one-time purchase as a zero hourly rate', () => {
    const wrapper = mountInspector(makeAccount({
      type: 'oauth',
      extra: {
        cost_profile: {
          amount: 12,
          currency: 'CNY',
          billing_cycle: 'one_time',
          started_at: JOINED_AT,
        },
      },
    }))

    expect(wrapper.text()).toContain('一次性费用')
    expect(wrapper.text()).toContain('非周期费用')
    expect(wrapper.text()).not.toContain('¥0.00000/h')
  })

  it('labels relays as channel-priced and only reveals optional fixed overhead on demand', async () => {
    const wrapper = mountInspector(makeAccount({
      name: 'relay',
      type: 'upstream',
      credentials: { base_url: 'https://relay.example.com/v1' },
    }))

    expect(wrapper.text()).toContain('API 中转 · relay.example.com')
    expect(wrapper.text()).toContain('渠道自定义价格')
    expect(wrapper.find('#cost-amount').exists()).toBe(false)

    await wrapper.get('[data-testid="enable-fixed-overhead"]').trigger('click')
    expect(wrapper.find('#cost-amount').exists()).toBe(true)
    expect(wrapper.text()).toContain('保存固定附加成本')
  })
})
