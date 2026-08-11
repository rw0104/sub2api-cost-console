import { describe, expect, it } from 'vitest'
import {
  createDataSourceStates,
  finiteOrNull,
  hasMeasuredData,
  hasUsableData,
  sourceState,
  unavailableValueLabel,
} from './dataState'

describe('cost center data states', () => {
  it('keeps a real zero distinct from missing data', () => {
    expect(finiteOrNull(0)).toBe(0)
    expect(finiteOrNull('0')).toBe(0)
    expect(finiteOrNull(null)).toBeNull()
    expect(finiteOrNull(Number.NaN)).toBeNull()
  })

  it('does not treat an unavailable source as an empty successful result', () => {
    const unavailable = sourceState('dashboard', 'unavailable', 'request failed', null)
    const empty = sourceState('dashboard', 'empty', 'window has no records')
    expect(hasUsableData(unavailable)).toBe(false)
    expect(hasMeasuredData(unavailable)).toBe(false)
    expect(unavailableValueLabel(unavailable)).toBe('无数据')
    expect(hasMeasuredData(empty)).toBe(true)
    expect(unavailableValueLabel(empty)).toBe('无记录')
  })

  it('initializes every source as loading instead of a fake zero state', () => {
    const states = createDataSourceStates()
    expect(states.economics.status).toBe('loading')
    expect(states.todayStats.status).toBe('loading')
  })
})
