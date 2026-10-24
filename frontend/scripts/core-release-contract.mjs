import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export function requireCoreCapabilities(manifest, capabilities) {
  assert.equal(manifest?.schema, 2, 'Compatible core schema must be 2')
  assert.equal(manifest.channel, 'stable', 'Expected stable core channel')
  assert.ok(Array.isArray(manifest.capabilities), 'Core capabilities must be an array')
  const missing = capabilities.filter(capability => !manifest.capabilities.includes(capability))
  assert.equal(missing.length, 0, `Compatible core is missing required capabilities: ${missing.join(', ')}`)
}

export function verifyCoreRelease(manifest, expected, archive) {
  requireCoreCapabilities(manifest, expected.capabilities)
  for (const field of ['version', 'extension_version', 'algorithm_version', 'upstream_commit']) {
    assert.equal(manifest[field], expected[field], `Core ${field} does not match the desktop build`)
  }
  const platform = manifest.platforms?.['windows-x86_64']
  assert.ok(platform, 'Windows core platform is missing')
  const name = `sub2api-core_${expected.version}_${expected.extension_version}_windows_x86_64.zip`
  assert.equal(platform.archive_name, name, 'Unexpected core archive name')
  assert.equal(platform.url, `https://github.com/${expected.repository}/releases/download/core-stable/${name}`, 'Unexpected core download URL')
  assert.equal(platform.size, archive.length, 'Core archive size mismatch')
  assert.equal(platform.sha256, createHash('sha256').update(archive).digest('hex'), 'Core archive SHA-256 mismatch')
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const frontend = resolve(dirname(fileURLToPath(import.meta.url)), '..')
  const value = name => readFileSync(resolve(frontend, name), 'utf8').trim()
  const capabilities = value('CORE_CAPABILITIES').split(/\s+/).filter(Boolean)
  const [mode, manifestPath, archivePath] = process.argv.slice(2)
  try {
    assert.ok(manifestPath, 'Usage: core-release-contract.mjs capabilities|verify MANIFEST [ARCHIVE]')
    const manifest = JSON.parse(readFileSync(resolve(manifestPath), 'utf8'))
    if (mode === 'capabilities') {
      requireCoreCapabilities(manifest, capabilities)
    } else {
      assert.equal(mode, 'verify', 'Unknown verification mode')
      assert.ok(archivePath, 'Core archive path is required')
      verifyCoreRelease(manifest, {
        repository: process.env.GITHUB_REPOSITORY || 'rw0104/sub2api-cost-console',
        version: value('CORE_VERSION'),
        extension_version: value('CORE_EXTENSION_VERSION'),
        algorithm_version: value('ALGORITHM_VERSION'),
        upstream_commit: value('UPSTREAM_SUB2API_COMMIT'),
        capabilities,
      }, readFileSync(resolve(archivePath)))
    }
    console.log('Compatible core release contract verified')
  } catch (error) {
    console.error(error.message)
    process.exitCode = 1
  }
}
