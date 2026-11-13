#!/usr/bin/env node
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const workflow = 'backend-ci.yml'

export function evaluateBackendCIRuns(runs, sourceSHA, expectedRef) {
  assert.match(sourceSHA, /^[a-f0-9]{40}$/i, 'A full source commit SHA is required')
  assert.ok(Array.isArray(runs), 'Backend CI runs must be an array')
  const latest = new Map()
  for (const run of runs) {
    if (run.head_sha !== sourceSHA) continue
    assert.ok(run.id != null, 'Backend CI run is missing its ID')
    assert.ok(Number.isInteger(run.run_attempt) && run.run_attempt > 0, `Run ${run.id} has no valid attempt`)
    const previous = latest.get(String(run.id))
    if (!previous || run.run_attempt > previous.run_attempt ||
      (run.run_attempt === previous.run_attempt && (run.updated_at ?? '') > (previous.updated_at ?? ''))) {
      latest.set(String(run.id), run)
    } else if (run.run_attempt === previous.run_attempt && (run.updated_at ?? '') === (previous.updated_at ?? '')) {
      assert.ok(run.status === previous.status && run.conclusion === previous.conclusion,
        `Conflicting snapshots for backend CI run ${run.id} attempt ${run.run_attempt}`)
    }
  }
  const current = [...latest.values()].sort((left, right) => String(left.id).localeCompare(String(right.id)))
  const failed = current.filter(run => run.status === 'completed' && run.conclusion !== 'success')
  const pending = current.filter(run => run.status !== 'completed')
  const missingRef = expectedRef && !current.some(run => run.head_branch === expectedRef)
  const describe = run => `run ${run.id} (${run.head_branch ?? 'unknown ref'}, attempt ${run.run_attempt}) ${run.status}/${run.conclusion ?? 'pending'}`
  if (failed.length) return { state: 'failed', runs: current, message: failed.map(describe).join('; ') }
  if (!current.length) return { state: 'pending', runs: current, message: `No backend CI runs found for ${sourceSHA}` }
  if (pending.length) return { state: 'pending', runs: current, message: pending.map(describe).join('; ') }
  if (missingRef) return { state: 'pending', runs: current, message: `No backend CI run for expected ref ${expectedRef} at ${sourceSHA}` }
  return { state: 'success', runs: current, message: `All ${current.length} backend CI runs passed for ${sourceSHA}` }
}

export async function listBackendCIRuns(repository, sourceSHA, requestPage) {
  assert.match(repository, /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/, 'A GitHub owner/repository is required')
  assert.match(sourceSHA, /^[a-f0-9]{40}$/i, 'A full source commit SHA is required')
  const runs = []
  let total = 0
  for (let page = 1; page <= 100; page++) {
    const endpoint = `repos/${repository}/actions/workflows/${workflow}/runs?head_sha=${sourceSHA}&per_page=100&page=${page}`
    const response = await requestPage(endpoint)
    assert.ok(Array.isArray(response.workflow_runs), 'Malformed backend CI workflow-runs response')
    assert.ok(Number.isInteger(response.total_count) && response.total_count >= 0, 'Missing backend CI run count')
    total = Math.max(total, response.total_count)
    runs.push(...response.workflow_runs)
    if (response.workflow_runs.length < 100) {
      assert.ok(new Set(runs.map(run => run.id)).size >= total,
        `Incomplete backend CI pagination: received fewer than ${total} distinct runs`)
      return runs
    }
  }
  throw new Error('Backend CI pagination exceeded its safety limit; refusing an incomplete result')
}

export async function waitForBackendCI({ repository, sourceSHA, expectedRef, timeoutMs = 1200000, pollMs = 30000,
  requestPage, now = Date.now, sleep = milliseconds => new Promise(done => setTimeout(done, milliseconds)), log = console.log }) {
  assert.ok(Number.isFinite(timeoutMs) && timeoutMs >= 0, 'Invalid backend CI timeout')
  assert.ok(Number.isFinite(pollMs) && pollMs > 0, 'Invalid backend CI polling interval')
  const deadline = now() + timeoutMs
  let lastMessage
  for (;;) {
    const runs = await listBackendCIRuns(repository, sourceSHA, requestPage)
    const result = evaluateBackendCIRuns(runs, sourceSHA, expectedRef)
    if (result.message !== lastMessage) {
      log(result.message)
      lastMessage = result.message
    }
    if (result.state === 'failed') throw new Error(`Full backend CI failed; refusing publication: ${result.message}`)
    if (result.state === 'success') return result.runs
    if (now() >= deadline) throw new Error(`Timed out waiting for complete backend CI; refusing publication: ${result.message}`)
    await sleep(Math.min(pollMs, deadline - now()))
  }
}

export async function main(args = process.argv.slice(2)) {
  const options = new Map()
  const allowed = new Set(['--repo', '--sha', '--expected-ref', '--timeout-seconds', '--poll-seconds'])
  for (let index = 0; index < args.length; index += 2) {
    assert.ok(allowed.has(args[index]) && args[index + 1] && !options.has(args[index]), `Invalid option ${args[index]}`)
    options.set(args[index], args[index + 1])
  }
  const repository = options.get('--repo') || process.env.GITHUB_REPOSITORY || process.env.GH_REPO
  const sourceSHA = options.get('--sha') || process.env.GITHUB_SHA
  const requestPage = endpoint => JSON.parse(execFileSync('gh', ['api', endpoint], {
    encoding: 'utf8', windowsHide: true, timeout: 60000, maxBuffer: 16 * 1024 * 1024,
    stdio: ['ignore', 'pipe', 'pipe'],
  }))
  await waitForBackendCI({
    repository, sourceSHA, expectedRef: options.get('--expected-ref') || process.env.CI_EXPECTED_REF, requestPage,
    timeoutMs: Number(options.get('--timeout-seconds') ?? 1200) * 1000,
    pollMs: Number(options.get('--poll-seconds') ?? 30) * 1000,
  })
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch(error => {
    console.error(error.message)
    process.exitCode = 1
  })
}
