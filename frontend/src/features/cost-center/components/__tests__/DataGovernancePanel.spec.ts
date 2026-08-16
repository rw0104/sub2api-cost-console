import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import DataGovernancePanel from '../DataGovernancePanel.vue'
import { createDataSourceStates, sourceState } from '../../dataState'

const push = vi.fn()
vi.mock('vue-router', () => ({ useRouter: () => ({ push }) }))

describe('DataGovernancePanel', () => {
  it('summarizes every source state and explains retention semantics', async () => {
    const states = createDataSourceStates('measured')
    states.dashboard = sourceState('dashboard', 'unavailable', 'usage_logs 读取失败')
    states.economics = sourceState('economics', 'partial', '样本不足')
    states.accountUsage = sourceState('accountUsage', 'empty', '窗口内无记录')

    const wrapper = mount(DataGovernancePanel, {
      props: { states, lastUpdated: '2026-08-11 10:00:00' },
    })

    expect(wrapper.findAll('.source-card')).toHaveLength(12)
    expect(wrapper.text()).toContain('usage_logs 读取失败')
    expect(wrapper.text()).toContain('当天统计每天归零')
    expect(wrapper.text()).not.toContain('Asia/Shanghai')
    expect(wrapper.text()).not.toContain('北京时间自然日')
    expect(wrapper.text()).toContain('自动刷新是 5/10/15/30 秒间隔')
    expect(wrapper.text()).toContain('成功查询的空金额/次数桶显示 0')
    expect(wrapper.text()).toContain('接口失败显示“无数据”')
    expect(wrapper.text()).toContain('90 天')
    expect(wrapper.text()).toContain('封禁损失账本不可静默清零')

    await wrapper.get('.governance__actions button').trigger('click')
    expect(push).toHaveBeenCalledWith('/admin/usage')
  })
})
