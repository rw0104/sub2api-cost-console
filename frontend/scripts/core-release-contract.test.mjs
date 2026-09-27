import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { test } from 'node:test'
import { requireCoreCapabilities, verifyCoreRelease } from './core-release-contract.mjs'

const expected = {
  repository: 'rw0104/sub2api-cost-console', version: '0.2.8', extension_version: '1.3.4',
  algorithm_version: '1.6.1', upstream_commit: 'a'.repeat(40),
  capabilities: ['plugin_extensions.v2', 'openai.oauth.request_header_probe.v1'],
}
const archive = Buffer.from('fixture-core-package')
const manifest = () => ({
  ...expected, schema: 2, channel: 'stable',
  platforms: { 'windows-x86_64': {
    archive_name: 'sub2api-core_0.2.8_1.3.4_windows_x86_64.zip',
    url: 'https://github.com/rw0104/sub2api-cost-console/releases/download/core-stable/sub2api-core_0.2.8_1.3.4_windows_x86_64.zip',
    sha256: createHash('sha256').update(archive).digest('hex'), size: archive.length,
  } },
})

test('same-version stable core missing a newly required capability must be refreshed', () => {
  const stale = manifest()
  stale.capabilities = ['plugin_extensions.v2']
  assert.throws(() => requireCoreCapabilities(stale, expected.capabilities), /request_header_probe/)
  assert.throws(() => verifyCoreRelease(stale, expected, archive), /request_header_probe/)
  assert.doesNotThrow(() => verifyCoreRelease(manifest(), expected, archive))
})

test('release contract rejects stale identities and non-stable channels', () => {
  for (const field of ['version', 'extension_version', 'algorithm_version', 'upstream_commit']) {
    assert.throws(() => verifyCoreRelease({ ...manifest(), [field]: 'stale' }, expected, archive), /does not match/)
  }
  assert.throws(() => requireCoreCapabilities({ ...manifest(), channel: 'candidate' }, expected.capabilities), /stable/)
  assert.throws(() => requireCoreCapabilities({ ...manifest(), schema: 1 }, expected.capabilities), /schema/)
})

test('publishing cannot advertise a different or corrupted core archive', () => {
  assert.throws(() => verifyCoreRelease(manifest(), expected, Buffer.from('altered-core-package')), /size|SHA-256/)
  const sameSizeCorruption = Buffer.from(archive)
  sameSizeCorruption[0] ^= 1
  assert.throws(() => verifyCoreRelease(manifest(), expected, sameSizeCorruption), /SHA-256/)
  const wrongOrigin = manifest()
  wrongOrigin.platforms['windows-x86_64'].url = 'https://example.invalid/core.zip'
  assert.throws(() => verifyCoreRelease(wrongOrigin, expected, archive), /download URL/)
})
