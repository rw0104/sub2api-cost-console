import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const styleSource = readFileSync(resolve(process.cwd(), 'src/style.css'), 'utf8')

describe('native select visual contract', () => {
  it('upgrades every single-choice native select through the modern picker API', () => {
    expect(styleSource).toContain('@supports (appearance: base-select)')
    expect(styleSource).toContain('select:not([multiple]):not([size])::picker(select)')
    expect(styleSource).toContain('appearance: base-select')
    expect(styleSource).toContain('option::checkmark')
  })

  it('defines light, dark, focus and selected picker states', () => {
    expect(styleSource).toContain('option:focus-visible')
    expect(styleSource).toContain('option:checked')
    expect(styleSource).toContain(':root.dark select:not([multiple]):not([size])::picker(select)')
    expect(styleSource).toContain('[data-aui-appearance="dark"] select:not([multiple]):not([size])::picker(select)')
    expect(styleSource).toContain('color-scheme: dark')
  })
})
