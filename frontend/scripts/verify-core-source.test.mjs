import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { basename, dirname, join, resolve } from 'node:path'
import { test } from 'node:test'
import { verifyCoreSource } from './verify-core-source.mjs'

test('annotated upstream tag must match both the pinned commit and integrated source tree', () => {
  const root = mkdtempSync(join(tmpdir(), 'sub2api-core-source-'))
  const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] }).trim()
  try {
    git('init')
    git('config', 'user.name', 'Core source contract')
    git('config', 'user.email', 'core-source-test@example.invalid')
    writeFileSync(join(root, 'source.txt'), 'base')
    git('add', 'source.txt')
    git('commit', '-m', 'base')
    const parent = git('rev-parse', 'HEAD')
    writeFileSync(join(root, 'source.txt'), 'upstream release')
    git('commit', '-am', 'upstream release')
    const official = git('rev-parse', 'HEAD')
    const tree = git('rev-parse', 'HEAD^{tree}')
    git('tag', '-a', 'upstream-v0.2.5', '-m', 'annotated release')
    git('checkout', '--detach', parent)
    const synthetic = git('commit-tree', tree, '-p', parent, '-m', 'synthetic-upstream-v0.2.5')
    git('merge', '--ff-only', synthetic)
    mkdirSync(join(root, 'frontend'))
    writeFileSync(join(root, 'frontend/CORE_VERSION'), '0.2.5')
    const pin = join(root, 'frontend/UPSTREAM_SUB2API_COMMIT')
    writeFileSync(pin, official)
    assert.equal(verifyCoreSource(root, 'upstream-v0.2.5').syntheticBase, synthetic)
    assert.notEqual(git('rev-parse', 'upstream-v0.2.5'), official, 'fixture must use an annotated tag')
    writeFileSync(pin, parent)
    assert.throws(() => verifyCoreSource(root, 'upstream-v0.2.5'), /Pinned upstream commit.*differs/)
    writeFileSync(pin, official)
    git('checkout', '--detach', official)
    assert.throws(() => verifyCoreSource(root, 'upstream-v0.2.5'), /No integrated synthetic upstream/)
    assert.equal(readFileSync(pin, 'utf8'), official)
  } finally {
    assert.equal(dirname(resolve(root)), resolve(tmpdir()))
    assert(basename(root).startsWith('sub2api-core-source-'))
    rmSync(root, { recursive: true, force: true })
  }
})
