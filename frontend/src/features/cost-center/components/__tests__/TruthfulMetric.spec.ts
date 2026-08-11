import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TruthfulMetric from '../TruthfulMetric.vue'

describe('TruthfulMetric', () => {
  it('keeps a measured zero as a real numeric value', () => {
    const wrapper = mount(TruthfulMetric, {
      props: {
        label: '当前产出',
        value: '$0.00',
        note: '已成功读取 usage_logs',
        state: 'measured',
      },
    })

    expect(wrapper.text()).toContain('$0.00')
    expect(wrapper.attributes('data-data-state')).toBe('measured')
    expect(wrapper.find('.cost-metric-cell__state').exists()).toBe(false)
  })

  it('labels an unavailable source instead of rendering a fake zero', () => {
    const wrapper = mount(TruthfulMetric, {
      props: {
        label: '当前产出',
        value: '无数据',
        note: '请求超时',
        state: 'unavailable',
      },
    })

    expect(wrapper.text()).toContain('无数据')
    expect(wrapper.text()).toContain('不可用')
    expect(wrapper.text()).not.toContain('$0.00')
  })

  it('exposes estimated values as estimates', () => {
    const wrapper = mount(TruthfulMetric, {
      props: {
        label: '采购费率',
        value: '¥0.3425/h',
        note: '套餐默认值',
        state: 'estimated',
      },
    })

    expect(wrapper.text()).toContain('估算')
    expect(wrapper.attributes('data-data-state')).toBe('estimated')
  })
})
