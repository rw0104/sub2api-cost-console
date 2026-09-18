// Builds an isolated local installer. Does not publish a release or touch stable update metadata.
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const preview = JSON.parse(readFileSync(resolve(root, 'src-tauri/tauri.plugin-preview.conf.json'), 'utf8'))
if (preview.identifier !== 'com.sub2api.plugin-preview' || preview.mainBinaryName !== 'sub2api-plugin-preview' || preview.bundle.createUpdaterArtifacts !== false) {
  throw new Error('Preview installer must retain its isolated identity and disabled updater artifacts')
}
function run(command, args, extra = {}) {
  const env = { ...process.env, ...extra }
  // Never embed machine-local API endpoints or a stable signing configuration.
  delete env.TAURI_SIGNING_PRIVATE_KEY
  delete env.TAURI_SIGNING_PRIVATE_KEY_PASSWORD
  if (!extra.VITE_API_BASE_URL) delete env.VITE_API_BASE_URL
  if (!extra.VITE_DESKTOP_CHANNEL) delete env.VITE_DESKTOP_CHANNEL
  const result = spawnSync(command, args, { cwd: root, env, stdio: 'inherit', windowsHide: true })
  if (result.error) throw result.error
  if (result.status !== 0) process.exit(result.status ?? 1)
}
run('corepack', ['pnpm@9', 'build'])
run('node', ['scripts/prepare-desktop-sidecar.mjs'], {
  SUB2API_CORE_VERSION: '0.2.5', SUB2API_CORE_EXTENSION_VERSION: '1.2.0-plugin.1', SUB2API_PLUGIN_PREVIEW_BUILD: '1'
})
run('corepack', ['pnpm@9', 'exec', 'tauri', 'build', '--config', 'src-tauri/tauri.plugin-preview.conf.json', '--features', 'plugin-preview', '--bundles', 'nsis'], {
  VITE_API_BASE_URL: 'http://127.0.0.1:19765/api/v1', VITE_DESKTOP_CHANNEL: 'plugin-preview'
})
console.log(`Built isolated Sub2API Plugin Preview ${preview.version}; no stable release or update manifest was published.`)
