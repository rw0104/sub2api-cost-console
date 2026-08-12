import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const appSource = readFileSync(resolve(process.cwd(), 'src/App.vue'), 'utf8')
const titlebarSource = readFileSync(
  resolve(process.cwd(), 'src/features/desktop/DesktopTitleBar.vue'),
  'utf8',
)
const toastSource = readFileSync(
  resolve(process.cwd(), 'src/components/common/Toast.vue'),
  'utf8',
)

describe('desktop chrome safe-area contract', () => {
  it('shares the custom titlebar height with fixed Sub2API shell elements', () => {
    expect(titlebarSource).toContain('flex: 0 0 36px')
    expect(appSource).toContain('--desktop-titlebar-height: 36px')
    expect(appSource).toContain('.app-window--desktop .app-window__content .sidebar')
    expect(appSource).toContain('top: var(--desktop-titlebar-height)')
    expect(appSource).toContain('min-height: calc(100vh - var(--desktop-titlebar-height))')
    expect(appSource).toContain(':root.desktop-runtime body :is(.fixed.inset-0, .modal-overlay)')
  })

  it('keeps desktop notifications away from titlebar and toolbar controls', () => {
    expect(toastSource).toContain("desktop ? 'toast-stack--desktop' : 'top-4'")
    expect(toastSource).toMatch(/\.toast-stack--desktop\s*\{[\s\S]*?top:\s*auto;[\s\S]*?bottom:\s*72px;/)
  })
})
