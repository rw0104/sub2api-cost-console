import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { VerifyOfficialModelsResult } from '@/api/admin/accounts'

const { verifyOfficialModels } = vi.hoisted(() => ({ verifyOfficialModels: vi.fn() }))
vi.mock('@/api/admin/accounts', () => ({ accountsAPI: { verifyOfficialModels } }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => `${key} ${JSON.stringify(values ?? {})}` })
}))

import OpenAIOfficialModelsPanel from '../OpenAIOfficialModelsPanel.vue'

function officialResult(overrides: Partial<VerifyOfficialModelsResult> = {}): VerifyOfficialModelsResult {
  return {
    models: ['gpt-6.1-sol', 'gpt-6.1-terra', 'future-official-slug'],
    metadata: { 'future-official-slug': { id: 'future-official-slug', display_name: 'Future official display name' } },
    source: 'https://chatgpt.com/backend-api/codex/models',
    fetched_at: '2026-10-26T12:00:00Z',
    checks: [
      { from: 'my-alias', to: 'gpt-6.1-sol', available: true },
      { from: 'old-alias', to: 'unlisted-model', available: false }
    ],
    missing_models: ['gpt-6-luna'],
    suggested_model_mapping: {
      'my-alias': 'gpt-6.1-sol', 'old-alias': 'unlisted-model',
      'gpt-6.1-sol': 'gpt-6.1-sol', 'gpt-6.1-terra': 'gpt-6.1-terra',
      'future-official-slug': 'future-official-slug'
    },
    ...overrides
  }
}

function mountPanel(overrides: Record<string, unknown> = {}) {
  return mount(OpenAIOfficialModelsPanel, {
    props: {
      accountId: 42,
      modelMapping: { 'my-alias': 'gpt-6.1-sol', 'old-alias': 'unlisted-model' },
      mode: 'mapping',
      ...overrides
    }
  })
}

describe('OpenAIOfficialModelsPanel', () => {
  beforeEach(() => { verifyOfficialModels.mockReset() })

  it('fetches real catalog IDs and compares the full draft without changing it', async () => {
    verifyOfficialModels.mockResolvedValue(officialResult())
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="verify-official-models"]').trigger('click')
    await flushPromises()

    expect(verifyOfficialModels).toHaveBeenCalledWith(42, {
      model_mapping: { 'my-alias': 'gpt-6.1-sol', 'old-alias': 'unlisted-model' }
    })
    expect(wrapper.text()).toContain('future-official-slug')
    expect(wrapper.text()).toContain('Future official display name')
    expect(wrapper.text()).toContain('old-alias → unlisted-model')
    expect(wrapper.text()).toContain('gpt-6-luna')
    expect(wrapper.emitted('add-models')).toBeUndefined()

    await wrapper.get('[data-testid="add-official-models"]').trigger('click')
    expect(wrapper.emitted('add-models')).toEqual([[['gpt-6.1-sol', 'gpt-6.1-terra', 'future-official-slug']]])
    expect(wrapper.find('[data-testid="add-official-models"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('preserves explicit remaps and wildcard routing when adding confirmed models', async () => {
    verifyOfficialModels.mockResolvedValue(officialResult({
      suggested_model_mapping: { 'gpt-6.1-sol': 'gpt-6.1-sol', 'gpt-6.1-terra': 'gpt-6.1-terra' }
    }))
    const wrapper = mountPanel({ modelMapping: { 'gpt-6.1-sol': 'my-upstream', 'gpt-6.1-*': 'other-upstream' } })
    await wrapper.get('[data-testid="verify-official-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="add-official-models"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="add-official-models"]').trigger('click')
    expect(wrapper.emitted('add-models')).toBeUndefined()
    wrapper.unmount()
  })

  it('offers future official IDs and excludes suggestions absent from the live catalog', async () => {
    verifyOfficialModels.mockResolvedValue(officialResult({
      models: ['gpt-6.1-sol', 'future-official-slug'],
      suggested_model_mapping: { 'gpt-6.1-terra': 'gpt-6.1-terra', 'future-official-slug': 'future-official-slug' }
    }))
    const wrapper = mountPanel({ modelMapping: {} })
    await wrapper.get('[data-testid="verify-official-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="add-official-models"]').text()).toContain('"count":1')
    await wrapper.get('[data-testid="add-official-models"]').trigger('click')
    expect(wrapper.emitted('add-models')).toEqual([[['future-official-slug']]])
    wrapper.unmount()
  })

  it('deduplicates all official IDs and preserves existing future-model routes', async () => {
    verifyOfficialModels.mockResolvedValue(officialResult({
      models: ['future-remapped', 'future-covered', 'audio-future', 'audio-future', 'already-listed'],
      suggested_model_mapping: {
        'future-remapped': 'future-remapped', 'future-covered': 'future-covered',
        'audio-future': 'audio-future', 'already-listed': 'already-listed'
      }
    }))
    const wrapper = mountPanel({
      modelMapping: {
        'future-remapped': 'custom-target', 'future-*': 'wildcard-target',
        'audio-alias': 'audio-future', 'already-listed': 'already-listed'
      }
    })
    await wrapper.get('[data-testid="verify-official-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="add-official-models"]').text()).toContain('"count":1')
    await wrapper.get('[data-testid="add-official-models"]').trigger('click')
    expect(wrapper.emitted('add-models')).toEqual([[['audio-future']]])
    wrapper.unmount()
  })

  it('does not supplement models already covered by a catch-all mapping', async () => {
    verifyOfficialModels.mockResolvedValue(officialResult())
    const wrapper = mountPanel({ modelMapping: { '*': 'custom-target' } })
    await wrapper.get('[data-testid="verify-official-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="add-official-models"]').attributes('disabled')).toBeDefined()
    expect(wrapper.emitted('add-models')).toBeUndefined()
    wrapper.unmount()
  })

  it('reports official fetch failure without adding models or retaining an old result', async () => {
    verifyOfficialModels.mockResolvedValueOnce(officialResult()).mockRejectedValueOnce({ message: 'Official catalog unavailable' })
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="verify-official-models"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="verify-official-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Official catalog unavailable')
    expect(wrapper.find('[data-testid="add-official-models"]').exists()).toBe(false)
    expect(wrapper.emitted('add-models')).toBeUndefined()
    wrapper.unmount()
  })

  it.each([
    { accountId: 99 },
    { mode: 'whitelist' as const },
    { modelMapping: { 'new-alias': 'new-target' } }
  ])('ignores an in-flight response after the account, mode, or draft changes: %j', async (newProps) => {
    let resolve!: (value: VerifyOfficialModelsResult) => void
    verifyOfficialModels.mockReturnValue(new Promise<VerifyOfficialModelsResult>(finish => { resolve = finish }))
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="verify-official-models"]').trigger('click')
    await wrapper.setProps(newProps)
    resolve(officialResult())
    await flushPromises()
    expect(wrapper.find('[data-testid="add-official-models"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('future-official-slug')
    expect(wrapper.emitted('add-models')).toBeUndefined()
    wrapper.unmount()
  })

  it('invalidates a completed comparison when the draft changes', async () => {
    verifyOfficialModels.mockResolvedValue(officialResult())
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="verify-official-models"]').trigger('click')
    await flushPromises()
    await wrapper.setProps({ modelMapping: { 'gpt-6.1-terra': 'custom-target' } })
    expect(wrapper.find('[data-testid="add-official-models"]').exists()).toBe(false)
    expect(wrapper.emitted('add-models')).toBeUndefined()
    wrapper.unmount()
  })

  it('does not fetch without a saved account and prevents duplicate requests', async () => {
    const unsaved = mountPanel({ accountId: 0 })
    await unsaved.get('[data-testid="verify-official-models"]').trigger('click')
    expect(verifyOfficialModels).not.toHaveBeenCalled()
    unsaved.unmount()

    verifyOfficialModels.mockReturnValue(new Promise(() => {}))
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="verify-official-models"]').trigger('click')
    await wrapper.get('[data-testid="verify-official-models"]').trigger('click')
    expect(verifyOfficialModels).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
