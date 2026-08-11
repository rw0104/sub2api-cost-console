import { shallowMount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import CostLineChart from '../CostLineChart.vue'

describe('CostLineChart truthful states', () => {
  it('does not cover a measured chart with an empty-state layer when there are no events', () => {
    const wrapper = shallowMount(CostLineChart, {
      props: {
        labels: ['12:00'],
        series: [{ label: '收入', color: '#fff', data: [1] }],
        state: 'measured',
        events: [],
      },
    })

    expect(wrapper.find('.cost-chart__state').exists()).toBe(false)
  })

  it('shows no-data instead of an empty plotting surface when every value is unavailable', () => {
    const wrapper = shallowMount(CostLineChart, {
      props: {
        labels: ['12:00'],
        series: [{ label: 'TTFT P95', color: '#fff', data: [null] }],
        state: 'measured',
      },
    })

    expect(wrapper.find('.cost-chart__state').text()).toContain('无记录')
  })
})
