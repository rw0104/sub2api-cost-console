import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import CostLineChart from '../CostLineChart.vue'

vi.mock('vue-chartjs', () => ({
  Line: {
    name: 'Line',
    props: ['data', 'options'],
    template: '<div data-test="line-chart" />',
  },
}))

const zeroSeries = [{ label: '实际产出', data: [0, 0], color: '#b9e55a' }]

describe('CostLineChart', () => {
  it('renders a genuine zero line when the source was measured', () => {
    const wrapper = mount(CostLineChart, {
      props: { labels: ['10:00', '10:05'], series: zeroSeries, state: 'measured' },
    })

    expect(wrapper.find('[data-test="line-chart"]').exists()).toBe(true)
    expect(wrapper.find('.cost-chart__state').exists()).toBe(false)
  })

  it('replaces the chart with an explicit failure state when the source is unavailable', () => {
    const wrapper = mount(CostLineChart, {
      props: {
        labels: [],
        series: [],
        state: 'unavailable',
        stateReason: 'usage_logs 接口超时',
      },
    })

    expect(wrapper.find('[data-test="line-chart"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('不可用')
    expect(wrapper.text()).toContain('usage_logs 接口超时')
  })

  it('distinguishes a successful empty query from a failed query', () => {
    const wrapper = mount(CostLineChart, {
      props: {
        labels: [],
        series: [],
        state: 'empty',
        stateReason: '时间窗口内确实没有记录',
      },
    })

    expect(wrapper.text()).toContain('无记录')
    expect(wrapper.text()).not.toContain('不可用')
  })
})
