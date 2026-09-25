import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import TimeRangeSelect from '../TimeRangeSelect.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('TimeRangeSelect', () => {
  it('keeps the one-hour value distinct from six hours', async () => {
    const wrapper = mount(TimeRangeSelect, {
      props: {
        modelValue: '1h',
        options: [
          { value: '1h', label: '最近 1 小时' },
          { value: '6h', label: '最近 6 小时' },
        ],
      },
    })

    expect(wrapper.find('.select-value').text()).toBe('最近 1 小时')
    await wrapper.find('button').trigger('click')
    const options = [...document.body.querySelectorAll('[role="option"]')]
    expect(options.map((option) => option.textContent?.trim())).toEqual(['最近 1 小时', '最近 6 小时'])
    await (options[0] as HTMLElement).click()
    expect(wrapper.emitted('update:modelValue')).toEqual([['1h']])
  })
})
