import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CostApiAccessPanel from '../CostApiAccessPanel.vue'

const mocks = vi.hoisted(() => ({
  listKeys: vi.fn(),
  getPublicSettings: vi.fn(),
  push: vi.fn(),
  copy: vi.fn(),
}))

vi.mock('@/api', () => ({
  keysAPI: { list: mocks.listKeys },
  authAPI: { getPublicSettings: mocks.getPublicSettings },
}))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: mocks.copy }),
}))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }) }))

describe('CostApiAccessPanel data truthfulness', () => {
  beforeEach(() => {
    mocks.listKeys.mockReset()
    mocks.getPublicSettings.mockReset().mockResolvedValue({ api_base_url: '' })
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
})
