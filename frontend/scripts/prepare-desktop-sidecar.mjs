import { spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = dirname(fileURLToPath(import.meta.url))
const frontendDirectory = resolve(scriptDirectory, '..')
const repositoryDirectory = resolve(frontendDirectory, '..')
const backendDirectory = resolve(repositoryDirectory, 'backend')
const tauriDirectory = resolve(frontendDirectory, 'src-tauri')
const embeddedIndex = resolve(backendDirectory, 'internal', 'web', 'dist', 'index.html')
const coreVersionFile = resolve(frontendDirectory, 'CORE_VERSION')
const output = resolve(
  tauriDirectory,
  'binaries',
  'sub2api-backend-x86_64-pc-windows-msvc.exe',
)

if (!existsSync(embeddedIndex)) {
  console.error('Missing embedded backend UI. Run `corepack pnpm@9 build` before preparing the sidecar.')
  process.exit(1)
}

const version = (process.env.SUB2API_CORE_VERSION || readFileSync(coreVersionFile, 'utf8')).trim()
if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) {
  console.error(`CORE_VERSION must be SemVer, received ${JSON.stringify(version)}`)
  process.exit(1)
}

function capture(command, args, fallback) {
  const result = spawnSync(command, args, {
    cwd: repositoryDirectory,
    encoding: 'utf8',
    windowsHide: true,
  })
  return result.status === 0 ? result.stdout.trim() || fallback : fallback
}

const commit = capture('git', ['rev-parse', '--short=12', 'HEAD'], 'unknown')
const date = new Date().toISOString()
const ldflags = [
  '-s',
  '-w',
  `-X main.Version=${version}`,
  `-X main.Commit=${commit}`,
  `-X main.Date=${date}`,
  '-X main.BuildType=release',
].join(' ')

mkdirSync(dirname(output), { recursive: true })
console.log(`Building managed Sub2API core ${version} -> ${output}`)
const build = spawnSync(
  'go',
  ['build', '-trimpath', '-ldflags', ldflags, '-o', output, './cmd/server'],
  {
    cwd: backendDirectory,
    stdio: 'inherit',
    windowsHide: true,
    env: {
      ...process.env,
      CGO_ENABLED: '0',
      GOOS: 'windows',
      GOARCH: 'amd64',
    },
  },
)

if (build.error) {
  console.error(build.error.message)
  process.exit(1)
}
process.exit(build.status ?? 1)
