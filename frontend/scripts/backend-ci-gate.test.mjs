import assert from 'node:assert/strict'
import { test } from 'node:test'
import { evaluateBackendCIRuns, listBackendCIRuns, waitForBackendCI } from './backend-ci-gate.mjs'

const sha = 'a'.repeat(40)
const successful = (id, branch = 'main', attempt = 1) => ({
  id, head_sha: sha, head_branch: branch, run_attempt: attempt, status: 'completed', conclusion: 'success',
})
const response = runs => ({ total_count: runs.length, workflow_runs: runs })

test('main success cannot hide an independently failed, cancelled or skipped tag run', () => {
  for (const conclusion of ['failure', 'cancelled', 'timed_out', 'skipped', 'neutral', 'action_required', null]) {
    const result = evaluateBackendCIRuns([successful(1), { ...successful(2, 'v0.4.8'), conclusion }], sha)
    assert.equal(result.state, 'failed')
    assert.match(result.message, /run 2/)
  }
})

test('every pending state blocks even alongside a successful run', () => {
  for (const status of ['queued', 'in_progress', 'waiting', 'requested', 'pending', 'unknown']) {
    const result = evaluateBackendCIRuns([successful(1), { ...successful(2), status, conclusion: null }], sha)
    assert.equal(result.state, 'pending')
  }
})

test('only the latest attempt of each distinct run is authoritative, regardless of ordering', () => {
  const oldFailure = { ...successful(2, 'v0.4.8'), conclusion: 'failure' }
  const latest = successful(2, 'v0.4.8', 2)
  for (const runs of [[oldFailure, latest], [latest, oldFailure]]) {
    const result = evaluateBackendCIRuns([successful(1), ...runs], sha)
    assert.equal(result.state, 'success')
    assert.equal(result.runs.length, 2)
    assert.equal(result.runs[1].run_attempt, 2)
  }
  assert.equal(evaluateBackendCIRuns([
    successful(1), successful(2), { ...latest, status: 'in_progress', conclusion: null },
  ], sha).state, 'pending')
})

test('a later snapshot wins within an attempt; ambiguous snapshots fail closed', () => {
  const earlier = { ...successful(1), updated_at: '2026-11-12T10:00:00Z', status: 'in_progress', conclusion: null }
  const later = { ...successful(1), updated_at: '2026-11-12T10:01:00Z' }
  assert.equal(evaluateBackendCIRuns([later, earlier], sha).state, 'success')
  assert.throws(() => evaluateBackendCIRuns([successful(1), { ...successful(1), conclusion: 'failure' }], sha), /Conflicting snapshots/)
})

test('another SHA cannot satisfy the gate and the triggering ref must have appeared', () => {
  assert.equal(evaluateBackendCIRuns([{ ...successful(1), head_sha: 'b'.repeat(40) }], sha).state, 'pending')
  assert.equal(evaluateBackendCIRuns([successful(1)], sha, 'v0.4.8').state, 'pending')
  assert.equal(evaluateBackendCIRuns([successful(1), successful(2, 'v0.4.8')], sha, 'v0.4.8').state, 'success')
})

test('malformed run identity and attempts never allow publication', () => {
  assert.throws(() => evaluateBackendCIRuns([successful(1)], 'short'), /full source/)
  assert.throws(() => evaluateBackendCIRuns([{ ...successful(1), id: null }], sha), /missing its ID/)
  assert.throws(() => evaluateBackendCIRuns([{ ...successful(1), run_attempt: null }], sha), /valid attempt/)
})

test('pagination finds a failed run beyond the first hundred successes', async () => {
  const endpoints = []
  const firstPage = Array.from({ length: 100 }, (_, index) => successful(index + 1))
  const runs = await listBackendCIRuns('owner/repo', sha, async endpoint => {
    endpoints.push(endpoint)
    return { total_count: 101, workflow_runs: endpoints.length === 1 ? firstPage : [{ ...successful(101), conclusion: 'failure' }] }
  })
  assert.equal(endpoints.length, 2)
  assert.match(endpoints[0], new RegExp(`/backend-ci.yml/runs\\?head_sha=${sha}&per_page=100&page=1$`))
  assert.match(endpoints[1], /page=2$/)
  assert.equal(evaluateBackendCIRuns(runs, sha).state, 'failed')
})

test('a full last page is followed until the empty terminal page', async () => {
  let pages = 0
  const runs = await listBackendCIRuns('owner/repo', sha, async () => ({
    total_count: 100, workflow_runs: ++pages === 1 ? Array.from({ length: 100 }, (_, index) => successful(index)) : [],
  }))
  assert.equal(pages, 2)
  assert.equal(runs.length, 100)
})

test('truncated, malformed and failed API responses fail closed', async () => {
  await assert.rejects(listBackendCIRuns('owner/repo', sha, async () => ({ total_count: 2, workflow_runs: [successful(1)] })), /Incomplete/)
  await assert.rejects(listBackendCIRuns('owner/repo', sha, async () => ({ total_count: 0 })), /Malformed/)
  await assert.rejects(listBackendCIRuns('owner/repo', sha, async () => { throw new Error('API unavailable') }), /API unavailable/)
})

test('waiting sees a delayed tag run and waits for its latest attempt to finish', async () => {
  let calls = 0
  let elapsed = 0
  const snapshots = [[], [successful(1)], [successful(1), { ...successful(2, 'v0.4.8', 2), status: 'in_progress', conclusion: null }], [successful(1), successful(2, 'v0.4.8', 2)]]
  const runs = await waitForBackendCI({ repository: 'owner/repo', sourceSHA: sha, expectedRef: 'v0.4.8',
    requestPage: async () => response(snapshots[calls++]), timeoutMs: 100, pollMs: 10,
    now: () => elapsed, sleep: async duration => { elapsed += duration }, log: () => {},
  })
  assert.equal(calls, 4)
  assert.equal(runs.length, 2)
  assert.equal(elapsed, 30)
})

test('terminal failure aborts immediately and timeout cannot become success', async () => {
  let sleeps = 0
  await assert.rejects(waitForBackendCI({ repository: 'owner/repo', sourceSHA: sha,
    requestPage: async () => response([successful(1), { ...successful(2), conclusion: 'failure' }]),
    sleep: async () => { sleeps++ }, log: () => {},
  }), /refusing publication/)
  assert.equal(sleeps, 0)
  await assert.rejects(waitForBackendCI({ repository: 'owner/repo', sourceSHA: sha,
    requestPage: async () => response([]), timeoutMs: 0, log: () => {},
  }), /Timed out/)
})
