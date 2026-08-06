import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import UpstreamProbeCell from '../UpstreamProbeCell.vue'

describe('UpstreamProbeCell', () => {
  it('makes the full latency cell a real probe button', async () => {
    const wrapper = mount(UpstreamProbeCell, {
      props: { accountName: 'probe@example.com' },
    })

    const trigger = wrapper.get('button')
    expect(trigger.text()).toContain('点击开始真实检测')
    await trigger.trigger('click')
    expect(wrapper.emitted('probe')).toHaveLength(1)
  })

  it('shows explicit loading, success and failure feedback', async () => {
    const wrapper = mount(UpstreamProbeCell, {
      props: {
        accountName: 'probe@example.com',
        state: { loading: true, message: '正在请求真实上游…' },
      },
    })

    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('检测中')

    await wrapper.setProps({ state: { loading: false, success: true, latency_ms: 86, message: '连接成功' } })
    expect(wrapper.text()).toContain('86 ms')
    expect(wrapper.text()).toContain('成功 · 连接测试总耗时')

    await wrapper.setProps({ state: { loading: false, success: false, latency_ms: 120, message: '上游返回 401' } })
    expect(wrapper.text()).toContain('120 ms')
    expect(wrapper.text()).toContain('失败 · 上游返回 401')
  })
})
