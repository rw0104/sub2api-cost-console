import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = dirname(fileURLToPath(import.meta.url))
const frontendDirectory = resolve(scriptDirectory, '..')
const tauriDirectory = resolve(frontendDirectory, 'src-tauri')
const releaseDirectory = resolve(frontendDirectory, 'release-assets')
const sourceCore = resolve(
  tauriDirectory,
  'binaries',
  'sub2api-backend-x86_64-pc-windows-msvc.exe',
)
const coreVersion = readFileSync(resolve(frontendDirectory, 'CORE_VERSION'), 'utf8').trim()
const algorithmVersion = readFileSync(resolve(frontendDirectory, 'ALGORITHM_VERSION'), 'utf8').trim()
const repository = (process.env.GITHUB_REPOSITORY || 'renqw2023/sub2api-cost-console').trim()
const channel = (process.env.SUB2API_CORE_CHANNEL || 'core-channel').trim()
const sourceCommit = (process.env.GITHUB_SHA || 'local').trim()
const notes = (process.env.SUB2API_CORE_RELEASE_NOTES || '').trim()
  || `受管 Sub2API 内核 ${coreVersion}；成本规则版本 ${algorithmVersion}。`

for (const [name, value] of [
  ['CORE_VERSION', coreVersion],
  ['ALGORITHM_VERSION', algorithmVersion],
]) {
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(value)) {
    throw new Error(`${name} must be SemVer, received ${JSON.stringify(value)}`)
  }
}
if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository)) {
  throw new Error(`Invalid GITHUB_REPOSITORY ${JSON.stringify(repository)}`)
}
if (!/^[A-Za-z0-9_.-]+$/.test(channel)) {
  throw new Error(`Invalid core release channel ${JSON.stringify(channel)}`)
}
if (!existsSync(sourceCore)) {
  throw new Error(`Managed core was not built: ${sourceCore}`)
}

function run(command, args) {
  const result = spawnSync(command, args, {
    cwd: frontendDirectory,
    stdio: 'inherit',
    windowsHide: true,
    env: process.env,
  })
  if (result.error) throw result.error
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} exited with ${result.status}`)
  }
}

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex')
}

rmSync(releaseDirectory, { recursive: true, force: true })
mkdirSync(releaseDirectory, { recursive: true })

const assetName = `sub2api-core-${coreVersion}-x86_64-pc-windows-msvc.exe`
const corePath = resolve(releaseDirectory, assetName)
copyFileSync(sourceCore, corePath)
run('corepack', ['pnpm@9', 'exec', 'tauri', 'signer', 'sign', corePath])

const releaseBase = `https://github.com/${repository}/releases/download/${channel}`
const manifest = {
  schema: 1,
  version: coreVersion,
  algorithm_version: algorithmVersion,
  published_at: new Date().toISOString(),
  source_commit: sourceCommit,
  notes,
  platforms: {
    'windows-x86_64': {
      url: `${releaseBase}/${assetName}`,
      signature_url: `${releaseBase}/${assetName}.sig`,
      sha256: sha256(corePath),
      size: statSync(corePath).size,
    },
  },
}
const manifestPath = resolve(releaseDirectory, 'core-update.json')
writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, 'utf8')
run('corepack', ['pnpm@9', 'exec', 'tauri', 'signer', 'sign', manifestPath])

const checksumPath = resolve(releaseDirectory, 'CORE_SHA256SUMS.txt')
const checksumLines = readdirSync(releaseDirectory, { withFileTypes: true })
  .filter(entry => entry.isFile() && entry.name !== 'CORE_SHA256SUMS.txt')
  .map(entry => `${sha256(resolve(releaseDirectory, entry.name))}  ${entry.name}`)
  .sort()
writeFileSync(checksumPath, `${checksumLines.join('\n')}\n`, 'utf8')

console.log(JSON.stringify({
  coreVersion,
  algorithmVersion,
  channel,
  assetName,
  releaseDirectory,
}, null, 2))
