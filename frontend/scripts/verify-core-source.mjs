import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export function verifyCoreSource(repositoryDirectory, upstreamRef) {
  const read = path => readFileSync(resolve(repositoryDirectory, path), 'utf8').trim()
  const version = read('frontend/CORE_VERSION')
  const pinnedCommit = read('frontend/UPSTREAM_SUB2API_COMMIT')
  if (!/^\d+\.\d+\.\d+$/.test(version) || !/^[a-f0-9]{40}$/.test(pinnedCommit)) {
    throw new Error('Invalid core version or full upstream commit')
  }
  function git(...args) {
    const result = spawnSync('git', args, { cwd: repositoryDirectory, encoding: 'utf8', windowsHide: true })
    if (result.status !== 0) throw new Error(`git ${args.join(' ')} failed: ${result.stderr || result.error}`)
    return result.stdout.trim()
  }
  // Pass the revision as one argument: PowerShell interprets unquoted ^{commit}.
  const officialCommit = git('rev-parse', '--verify', `${upstreamRef}^{commit}`)
  if (officialCommit !== pinnedCommit) {
    throw new Error(`Pinned upstream commit ${pinnedCommit} differs from ${upstreamRef}: ${officialCommit}`)
  }
  const officialTree = git('show', '-s', '--format=%T', officialCommit)
  const candidates = git('log', 'HEAD', '--format=%H', '--regexp-ignore-case', '--extended-regexp',
    `--grep=synthetic[- ]upstream[- ]v${version.replaceAll('.', '\\.')}($|[^0-9.])`).split('\n').filter(Boolean)
  const syntheticBase = candidates.find(candidate => git('show', '-s', '--format=%T', candidate) === officialTree)
  if (!syntheticBase) throw new Error(`No integrated synthetic upstream v${version} matches official tree ${officialTree}`)
  return { version, upstreamCommit: officialCommit, upstreamTree: officialTree, syntheticBase }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const version = readFileSync(resolve(root, 'frontend/CORE_VERSION'), 'utf8').trim()
  console.log(JSON.stringify(verifyCoreSource(root, process.argv[2] || `upstream-v${version}`), null, 2))
}
