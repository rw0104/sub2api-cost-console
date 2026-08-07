import { describe, expect, it } from 'vitest'
import { calculateDesktopScale } from '../desktopScale'

describe('cost center desktop scaling', () => {
  it('keeps a 100% DPI desktop at native scale', () => {
    expect(calculateDesktopScale({ cssWidth: 1440, cssHeight: 900, devicePixelRatio: 1 })).toEqual({
      scale: 1,
      effectiveWidth: 1440,
      physicalWidth: 1440,
      useWideToolbar: false,
    })
  })

  it('lets WebView2 preserve the Windows 150% accessibility scale', () => {
    const result = calculateDesktopScale({ cssWidth: 1280, cssHeight: 720, devicePixelRatio: 1.5 })

    expect(result.scale).toBe(1)
    expect(result.effectiveWidth).toBe(1280)
    expect(result.physicalWidth).toBe(1920)
    expect(result.useWideToolbar).toBe(false)
  })

  it('does not apply a second browser zoom at 200% DPI', () => {
    const result = calculateDesktopScale({ cssWidth: 2048, cssHeight: 1125, devicePixelRatio: 2 })

    expect(result.scale).toBe(1)
    expect(result.effectiveWidth).toBe(2048)
    expect(result.physicalWidth).toBe(4096)
    expect(result.useWideToolbar).toBe(true)
  })
})
