import { createHash } from 'node:crypto'
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  statSync,
  writeFileSync,
} from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = dirname(fileURLToPath(import.meta.url))
const frontendDirectory = resolve(scriptDirectory, '..')
const tauriDirectory = resolve(frontendDirectory, 'src-tauri')
const releaseDirectory = resolve(frontendDirectory, 'release-assets')
const nsisDirectory = resolve(tauriDirectory, 'target', 'release', 'bundle', 'nsis')
const config = JSON.parse(readFileSync(resolve(tauriDirectory, 'tauri.conf.json'), 'utf8'))
const version = String(config.version)
const repository = (process.env.GITHUB_REPOSITORY || 'renqw2023/sub2api-cost-console').trim()

if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) {
  throw new Error(`Desktop version must be SemVer, received ${JSON.stringify(version)}`)
}
if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository)) {
  throw new Error(`Invalid GITHUB_REPOSITORY ${JSON.stringify(repository)}`)
}
if (!existsSync(nsisDirectory)) {
  throw new Error(`NSIS output directory does not exist: ${nsisDirectory}`)
}

const installerName = readdirSync(nsisDirectory)
  .find(name => name.endsWith(`_${version}_x64-setup.exe`))
if (!installerName) throw new Error('No x64 NSIS installer was generated')
const installerPath = resolve(nsisDirectory, installerName)
const signaturePath = `${installerPath}.sig`
if (!existsSync(signaturePath)) {
  throw new Error(`Updater signature is missing: ${signaturePath}`)
}

const signature = readFileSync(signaturePath, 'utf8').trim()
// GitHub normalizes spaces in uploaded release asset names to periods.
// Build the update URL and checksum labels from the resulting remote name.
const releaseAssetName = installerName.replace(/\s+/g, '.')
const encodedAssetName = encodeURIComponent(releaseAssetName)
const downloadUrl = `https://github.com/${repository}/releases/download/v${version}/${encodedAssetName}`
const platform = { signature, url: downloadUrl }
const latest = {
  version,
  notes: `Sub2API Cost Console ${version}: managed core, signed updates and cost algorithm traceability.`,
  pub_date: new Date().toISOString(),
  platforms: {
    'windows-x86_64': platform,
    'windows-x86_64-nsis': platform,
  },
}

mkdirSync(releaseDirectory, { recursive: true })
const latestPath = resolve(releaseDirectory, 'latest.json')
writeFileSync(latestPath, `${JSON.stringify(latest, null, 2)}\n`, 'utf8')

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex')
}

const checksums = [
  [installerPath, releaseAssetName],
  [signaturePath, `${releaseAssetName}.sig`],
  [latestPath, 'latest.json'],
]
  .map(([path, name]) => `${sha256(path)}  ${name}`)
  .sort()
const checksumPath = resolve(releaseDirectory, 'INSTALLER_SHA256SUMS.txt')
writeFileSync(checksumPath, `${checksums.join('\n')}\n`, 'utf8')

console.log(JSON.stringify({
  version,
  installerName,
  releaseAssetName,
  installerPath,
  signaturePath,
  latestPath,
  checksumPath,
  installerSize: statSync(installerPath).size,
}, null, 2))
