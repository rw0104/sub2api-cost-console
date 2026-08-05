import { spawnSync } from 'node:child_process'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = dirname(fileURLToPath(import.meta.url))
const frontendDirectory = resolve(scriptDirectory, '..')
const mode = process.argv[2] === 'dev' ? 'dev' : 'build'

function run(command, args) {
  const result = spawnSync(command, args, {
    cwd: frontendDirectory,
    stdio: 'inherit',
    windowsHide: true,
  })
  if (result.error) {
    console.error(result.error.message)
    process.exit(1)
  }
  if (result.status !== 0) {
    process.exit(result.status ?? 1)
  }
}

// The Go server embeds the normal web build. The Tauri window receives a
// separate desktop build from the configured beforeBuild/beforeDev command.
run('corepack', ['pnpm@9', 'build'])
run('node', [resolve(scriptDirectory, 'prepare-desktop-sidecar.mjs')])
const tauriArgs = [mode]
run('corepack', ['pnpm@9', 'exec', 'tauri', ...tauriArgs])
