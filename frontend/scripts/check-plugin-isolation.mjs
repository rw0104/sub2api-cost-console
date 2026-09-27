import { execFileSync } from 'node:child_process'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const tracked = execFileSync('git', ['ls-files', '-z'], { cwd: root, encoding: 'utf8' })
  .split('\0').filter(Boolean)
const misplaced = tracked.filter(path => path.startsWith('plugins/') || path.startsWith('.retired-plugin-artifacts/'))
if (misplaced.length) {
  console.error('Plugin implementations must be developed in a separate repository, outside the host checkout:')
  console.error(misplaced.join('\n'))
  process.exitCode = 1
} else {
  console.log('Host repository contains no embedded plugin development workspace')
}
