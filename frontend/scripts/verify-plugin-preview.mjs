// Verifies build outputs without installing the application or accessing user databases.
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync, copyFileSync, rmSync, statSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { basename, dirname, join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const frontend = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const repo = resolve(frontend, '..')
const workingTree = process.argv[2] === '--working-tree'
const head = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8', windowsHide: true })
const commit = workingTree ? head.stdout.trim() : process.argv[2]
assert.match(commit || '', /^[0-9a-f]{40}$/, 'Pass the full source commit used for this build')
const changed = spawnSync('git', ['diff', '--name-only', commit, '--', 'backend', 'frontend/src', 'frontend/src-tauri', 'frontend/scripts/build-plugin-preview.mjs', 'frontend/scripts/prepare-desktop-sidecar.mjs'], { cwd: repo, encoding: 'utf8', windowsHide: true })
assert.equal(changed.status, 0, changed.stderr)
if (!workingTree) assert.equal(changed.stdout.trim(), '', 'Runtime source must match the stated build commit')
const sourceList = spawnSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '--', 'backend', 'frontend/src', 'frontend/src-tauri', 'frontend/scripts', 'frontend/PLUGIN_PREVIEW_NOTES.md', 'frontend/CORE_VERSION', 'frontend/CORE_CAPABILITIES', 'frontend/CORE_EXTENSION_VERSION'], { cwd: repo, encoding: 'utf8', windowsHide: true })
assert.equal(sourceList.status, 0, sourceList.stderr)
const sourceHash = createHash('sha256')
for (const name of [...new Set(sourceList.stdout.trim().split(/\r?\n/))].sort()) {
  const path = join(repo, name)
  if (existsSync(path) && statSync(path).isFile()) sourceHash.update(name + '\0').update(readFileSync(path))
}
const profile = JSON.parse(readFileSync(join(frontend, 'src-tauri/tauri.plugin-preview.conf.json'), 'utf8'))
const name = `${profile.productName}_${profile.version}_x64-setup.exe`
const installer = join(frontend, 'src-tauri/target/release/bundle/nsis', name)
const sidecar = join(frontend, 'src-tauri/binaries/sub2api-backend-x86_64-pc-windows-msvc.exe')
const script = readFileSync(join(frontend, 'src-tauri/target/release/nsis/x64/installer.nsi'), 'utf8')
for (const [key, value] of Object.entries({ PRODUCTNAME: profile.productName, VERSION: profile.version, BUNDLEID: profile.identifier, MAINBINARYNAME: profile.mainBinaryName, STARTMENUFOLDER: profile.productName })) {
  assert.ok(script.includes(`!define ${key} "${value}"`), `NSIS ${key} must match preview identity`)
}
assert.ok(!script.includes('com.sub2api.cost-console'), 'NSIS must not use the stable app ID')
assert.equal(profile.bundle.createUpdaterArtifacts, false)
assert.ok(existsSync(installer) && statSync(installer).size > 1_000_000)

const version = spawnSync(sidecar, ['--version'], { encoding: 'utf8', windowsHide: true, timeout: 15000 })
assert.equal(version.status, 0, version.stderr)
assert.match(version.stdout, /Sub2API 0\.2\.5/)
assert.match(version.stdout, /1\.2\.0-plugin\.1/)

const scratch = mkdtempSync(join(tmpdir(), 'sub2api-plugin-preview-verify-'))
let isolationOutput
try {
  // Reserved .invalid hostname is intentional: even an incorrect build cannot
  // reach the user's production database. Correct builds reject before dialing.
  const configPath = join(scratch, 'config.yaml')
  writeFileSync(configPath, JSON.stringify({
    database: { host: 'preview-safety.invalid', port: 5432, dbname: 'production' },
    jwt: { secret: 'isolated-preview-verification-only-32-characters' }
  }))
  const safeEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) => /^(PATH|PATHEXT|SYSTEMROOT|WINDIR|TEMP|TMP|APPDATA|LOCALAPPDATA|USERPROFILE|COMSPEC)$/i.test(key)))
  const result = spawnSync(sidecar, [], { cwd: scratch, encoding: 'utf8', windowsHide: true, timeout: 15000,
    env: { ...safeEnv, CONFIG_FILE: configPath, DATA_DIR: scratch, SKIP_SETUP: 'true' } })
  isolationOutput = (result.stdout || '') + (result.stderr || '')
  assert.notEqual(result.status, 0)
  assert.match(isolationOutput, /不能连接正式库/, 'compiled preview guard must reject before connecting (without a preview env flag)')
} finally {
  assert.ok(resolve(scratch).startsWith(resolve(tmpdir()) + sep) && basename(scratch).startsWith('sub2api-plugin-preview-verify-'))
  rmSync(scratch, { recursive: true, force: true })
}

const output = join(frontend, 'release-assets', `plugin-preview-${profile.version}`)
mkdirSync(output, { recursive: true })
copyFileSync(installer, join(output, name))
const sha256 = path => createHash('sha256').update(readFileSync(path)).digest('hex')
const receipt = {
  desktop_version: profile.version, core_version: '0.2.5', extension_version: '1.2.0-plugin.1', source_commit: commit,
  source_mode: workingTree ? 'working-tree' : 'commit', source_files_sha256: sourceHash.digest('hex'),
  product_name: profile.productName, app_identifier: profile.identifier, executable: profile.mainBinaryName + '.exe',
  backend_port: 19765, postgres_port: 25432, redis_port: 26379, database: 'sub2api_plugin_preview',
  stable_update_channels_enabled: false, installed_on_user_machine: false,
  checks: { nsis_identity: true, sidecar_version: true, compiled_database_isolation: true },
  installer: { file: name, bytes: statSync(installer).size, sha256: sha256(installer) },
  sidecar: { sha256: sha256(sidecar), version_output: version.stdout.trim() },
  verified_at: new Date().toISOString()
}
writeFileSync(join(output, 'verification.json'), JSON.stringify(receipt, null, 2) + '\n')
const files = readdirSync(output).filter(file => file !== 'SHA256SUMS.txt' && !file.endsWith('.zip')).sort()
assert.ok(files.every(file => !file.endsWith('.private')), 'Never ship publisher private keys')
writeFileSync(join(output, 'SHA256SUMS.txt'), files.map(file => `${sha256(join(output,file))}  ${file}`).join('\n') + '\n')
console.log(JSON.stringify(receipt, null, 2))
