import { createHash } from 'node:crypto'
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs'
import { spawnSync } from 'node:child_process'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = dirname(fileURLToPath(import.meta.url))
const frontendDirectory = resolve(scriptDirectory, '..')
const releaseDirectory = resolve(frontendDirectory, 'release-assets')
const stagingDirectory = resolve(releaseDirectory, 'core-package')
const sourceBinary = resolve(
  frontendDirectory,
  'src-tauri',
  'binaries',
  'sub2api-backend-x86_64-pc-windows-msvc.exe',
)
const stagedBinary = resolve(stagingDirectory, 'sub2api.exe')
const repository = (process.env.GITHUB_REPOSITORY || 'rw0104/sub2api-cost-console').trim()
const coreVersion = readFileSync(resolve(frontendDirectory, 'CORE_VERSION'), 'utf8').trim()
const algorithmVersion = readFileSync(resolve(frontendDirectory, 'ALGORITHM_VERSION'), 'utf8').trim()
const extensionVersion = readFileSync(resolve(frontendDirectory, 'CORE_EXTENSION_VERSION'), 'utf8').trim()
const upstreamCommit = readFileSync(resolve(frontendDirectory, 'UPSTREAM_SUB2API_COMMIT'), 'utf8').trim()
const desktopReleaseNotes = readFileSync(
  resolve(frontendDirectory, 'DESKTOP_RELEASE_NOTES.md'),
  'utf8',
).trim()
const capabilities = readFileSync(resolve(frontendDirectory, 'CORE_CAPABILITIES'), 'utf8')
  .trim()
  .split(/\s+/)
  .filter(Boolean)

if (!existsSync(sourceBinary)) throw new Error(`Managed core does not exist: ${sourceBinary}`)
if (!/^\d+\.\d+\.\d+$/.test(coreVersion)) throw new Error(`Invalid CORE_VERSION ${JSON.stringify(coreVersion)}`)
if (!/^\d+\.\d+\.\d+$/.test(extensionVersion)) throw new Error(`Invalid CORE_EXTENSION_VERSION ${JSON.stringify(extensionVersion)}`)
if (!/^[0-9a-f]{40}$/i.test(upstreamCommit)) throw new Error('UPSTREAM_SUB2API_COMMIT must be a full Git commit')
if (!capabilities.length) throw new Error('CORE_CAPABILITIES must not be empty')

const releaseNotes = desktopReleaseNotes.includes(`\`v${coreVersion}\``)
  && desktopReleaseNotes.includes(upstreamCommit)
  ? desktopReleaseNotes
  : [
      `# 兼容内核 v${coreVersion}+costconsole.${extensionVersion}`,
      '',
      '## 内核基线',
      '',
      `- Sub2API 上游基线：v${coreVersion}`,
      `- 上游提交：${upstreamCommit}`,
      `- 成本扩展版本：${extensionVersion}`,
      `- 成本算法版本：${algorithmVersion}`,
      `- 必需能力：${capabilities.join(', ')}`,
      '',
      '## 技术变更',
      '',
      '- 自动合并新的上游内核基线，并重新编译成本扩展兼容内核。',
      '- 保留账号成本损失账本、事件去重、退款与恢复冲销能力。',
      '- 发布前已完成后端、桌面兼容协议和产物身份验证。',
      '',
      '## 升级行为',
      '',
      '- 桌面端检测到该兼容内核后会自动下载并暂存，在下次安全启动时切换。',
      '- 不会安装缺少成本账本能力的官方原版二进制，也不会自动降级。',
    ].join('\n')

const versionCheck = spawnSync(sourceBinary, ['--version'], { encoding: 'utf8', windowsHide: true })
const versionOutput = `${versionCheck.stdout || ''}\n${versionCheck.stderr || ''}`
if (versionCheck.status !== 0) throw new Error(`Managed core --version failed: ${versionCheck.status}`)
for (const marker of [
  `Sub2API ${coreVersion} `,
  `commit: ${upstreamCommit}`,
  `extension: ${extensionVersion}`,
  `capabilities: ${capabilities.join('|')}`,
]) {
  if (!versionOutput.includes(marker)) throw new Error(`Managed core identity is missing ${JSON.stringify(marker)}`)
}

mkdirSync(stagingDirectory, { recursive: true })
copyFileSync(sourceBinary, stagedBinary)
const archiveName = `sub2api-core_${coreVersion}_${extensionVersion}_windows_x86_64.zip`
const archivePath = resolve(releaseDirectory, archiveName)
rmSync(archivePath, { force: true })
const archive = spawnSync(
  'tar',
  ['-a', '-c', '-f', archivePath, '-C', stagingDirectory, 'sub2api.exe'],
  { encoding: 'utf8', windowsHide: true },
)
if (archive.error) throw archive.error
if (archive.status !== 0) throw new Error(`Unable to create compatible core archive: ${archive.stderr}`)

const sha256 = createHash('sha256').update(readFileSync(archivePath)).digest('hex')
const releaseTag = (process.env.CORE_RELEASE_TAG || 'core-stable').trim()
if (!/^[A-Za-z0-9._-]+$/.test(releaseTag)) throw new Error(`Invalid CORE_RELEASE_TAG ${JSON.stringify(releaseTag)}`)
const releaseChannel = (process.env.CORE_RELEASE_CHANNEL || 'stable').trim()
if (!['candidate', 'stable'].includes(releaseChannel)) throw new Error(`Invalid CORE_RELEASE_CHANNEL ${JSON.stringify(releaseChannel)}`)
const assetUrl = `https://github.com/${repository}/releases/download/${releaseTag}/${archiveName}`
const manifest = {
  schema: 2,
  channel: releaseChannel,
  version: coreVersion,
  algorithm_version: algorithmVersion,
  extension_version: extensionVersion,
  capabilities,
  upstream_commit: upstreamCommit,
  published_at: new Date().toISOString(),
  notes: releaseNotes,
  release_url: `https://github.com/${repository}/releases/tag/${releaseTag}`,
  platforms: {
    'windows-x86_64': {
      url: assetUrl,
      archive_name: archiveName,
      sha256,
      size: statSync(archivePath).size,
    },
  },
}
const manifestPath = resolve(releaseDirectory, 'core-latest.json')
writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, 'utf8')
const checksumsPath = resolve(releaseDirectory, 'CORE_SHA256SUMS.txt')
writeFileSync(
  checksumsPath,
  `${sha256}  ${archiveName}\n${createHash('sha256').update(readFileSync(manifestPath)).digest('hex')}  core-latest.json\n`,
  'utf8',
)
rmSync(stagingDirectory, { recursive: true, force: true })

console.log(JSON.stringify({ archiveName, archivePath, manifestPath, checksumsPath, manifest }, null, 2))
