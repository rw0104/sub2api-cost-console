import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(
  resolve(process.cwd(), 'src/views/admin/CostCenterView.vue'),
  'utf8',
)

function cssHex(variable: string): string {
  const match = source.match(new RegExp(`--${variable}:\\s*(#[0-9a-f]{6})`, 'i'))
  if (!match) throw new Error(`Missing CSS variable --${variable}`)
  return match[1]
}

function relativeLuminance(hex: string): number {
  const channels = hex.match(/[0-9a-f]{2}/gi)?.map((value) => Number.parseInt(value, 16) / 255)
  if (!channels || channels.length !== 3) throw new Error(`Invalid color ${hex}`)
  const [red, green, blue] = channels.map((value) => (
    value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
  ))
  return 0.2126 * red + 0.7152 * green + 0.0722 * blue
}

function contrast(foreground: string, background: string): number {
  const lighter = Math.max(relativeLuminance(foreground), relativeLuminance(background))
  const darker = Math.min(relativeLuminance(foreground), relativeLuminance(background))
  return (lighter + 0.05) / (darker + 0.05)
}

describe('cost center UI shell contract', () => {
  it('keeps every cost workspace inside an explicit dark semantic boundary', () => {
    expect(source).toContain('data-aui-appearance="dark"')
    expect(source).toContain('--aui-label-primary: var(--cost-text)')
    expect(source).toContain('--aui-label-tertiary: var(--cost-muted)')
    expect(source).toContain('--aui-canvas: var(--cost-bg)')
    expect(source).toContain('--aui-content-plane: var(--cost-bg)')
    expect(source).toContain('--aui-content-plane-secondary: var(--cost-panel)')
    expect(source).toContain('> main[data-aui-layer="content"]')
  })

  it('maintains readable primary, secondary and accent contrast', () => {
    expect(contrast(cssHex('cost-text'), cssHex('cost-bg'))).toBeGreaterThanOrEqual(7)
    expect(contrast(cssHex('cost-muted'), cssHex('cost-panel'))).toBeGreaterThanOrEqual(4.5)
    expect(contrast(cssHex('cost-lime'), cssHex('cost-bg'))).toBeGreaterThanOrEqual(7)
    expect(contrast('#10140f', cssHex('cost-lime'))).toBeGreaterThanOrEqual(7)
  })

  it('keeps governance out of primary navigation and exposes it as a toolbar utility', () => {
    const workspaceItems = source.match(/const workspaceItems = \[([\s\S]*?)\n\]/)?.[1] ?? ''
    expect(workspaceItems).not.toContain("key: 'governance'")
    expect(workspaceItems).toContain("key: 'api'")
    expect(source).toContain('class="cost-icon-button cost-governance-button"')
    expect(source).toContain('aria-label="数据治理"')
    expect(source).toContain("@click=\"activePanel = 'governance'\"")
  })

  it('renders one compact, non-wrapping segmented workspace control', () => {
    expect(source).toContain('data-aui-component="segmented"')
    expect(source).not.toContain('data-aui-component="sidebar"')
    expect(source).toContain(':aria-pressed="activePanel === item.key"')
    expect(source).toMatch(/\.cost-workspaces button\s*\{[\s\S]*?white-space:\s*nowrap;/)
    expect(source).toMatch(/\.cost-workspaces button\s*\{[\s\S]*?width:\s*142px;[\s\S]*?flex:\s*0 0 142px;/)
    expect(source).toMatch(/\.cost-toolbar\s*\{[\s\S]*?min-height:\s*74px;/)
  })
})
