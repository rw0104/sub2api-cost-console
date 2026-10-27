import { beforeEach, describe, expect, it, vi } from 'vitest'
const { post } = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { post } }))
import { accountsAPI, verifyOfficialModels } from '../admin/accounts'

describe('official model comparison API', () => {
  beforeEach(() => { post.mockReset() })

  it('sends the complete draft to the saved account verification endpoint', async () => {
    const result = { models: ['gpt-6.1-terra'], checks: [], source: 'https://api.openai.com/v1/models' }
    post.mockResolvedValue({ data: result })
    const draft = { model_mapping: { 'my-alias': 'gpt-6.1-sol', 'gpt-6.1-terra': 'gpt-6.1-terra' } }
    expect(await accountsAPI.verifyOfficialModels(42, draft)).toEqual(result)
    expect(post).toHaveBeenCalledWith('/admin/accounts/42/models/verify-official', draft)
  })

  it('can request comparison of the saved mapping and preserves a failure', async () => {
    const failure = { status: 502, message: 'Official catalog failed' }
    post.mockRejectedValue(failure)
    await expect(verifyOfficialModels(42)).rejects.toBe(failure)
    expect(post).toHaveBeenCalledWith('/admin/accounts/42/models/verify-official', undefined)
  })
})
