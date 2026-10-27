import { describe, expect, it } from 'vitest'
import { OPENAI_OFFICIAL_MODEL_PRESETS, getModelsByPlatform, getPresetMappingsByPlatform } from '../useModelWhitelist'

const pickerIds = [
  'gpt-6.1-sol', 'gpt-6.1-terra', 'gpt-6-sol', 'gpt-6-luna',
  'gpt-5.6-terra', 'gpt-5.6-luna', 'gpt-6-astra', 'gpt-5.6-sol'
]

describe('official OpenAI model presets', () => {
  it('uses the requested picker order for model choices and exact self mappings', () => {
    expect(OPENAI_OFFICIAL_MODEL_PRESETS.map(model => model.id)).toEqual(pickerIds)
    expect(getModelsByPlatform('openai').slice(0, 8)).toEqual(pickerIds)
    expect(getPresetMappingsByPlatform('openai').slice(0, 8).map(preset => [preset.from, preset.to]))
      .toEqual(pickerIds.map(id => [id, id]))
  })

  it('retains legacy aliases and does not duplicate the official models', () => {
    const models = getModelsByPlatform('openai')
    expect(models).toEqual(expect.arrayContaining(['gpt-6', 'gpt-5.6', 'gpt-5.4', 'gpt-image-2.5-sunburst']))
    for (const model of pickerIds) expect(models.filter(candidate => candidate === model)).toHaveLength(1)
  })
})
