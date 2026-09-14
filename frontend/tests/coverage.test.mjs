import assert from 'node:assert/strict'
import { test } from 'node:test'
import { build } from 'esbuild'

const result = await build({
  entryPoints: ['src/lib/coverage.ts'],
  bundle: true,
  format: 'esm',
  platform: 'node',
  write: false,
})
const { coverageResources, coverageInterval } = await import(
  'data:text/javascript;base64,' + Buffer.from(result.outputFiles[0].text).toString('base64')
)
const now = Date.parse('2026-09-14T12:00:00Z')
const schedule = { namespace: 'app', spec: { backup: { schedule: '@daily' } } }
const snap = (path, age = 1) => ({
  namespace: 'app',
  spec: { paths: [path], date: new Date(now - age * 3600000).toISOString() },
})

test('a fresh volume does not hide a missing or overdue sibling', () => {
  const rows = coverageResources(
    [schedule],
    [snap('/data/a'), snap('/data/b', 48)],
    ['a', 'b', 'c'].map((name) => ({ namespace: 'app', name })),
    now,
  )
  assert.equal(rows.find((r) => r.name === 'a').state, 'fresh')
  assert.equal(rows.find((r) => r.name === 'b').state, 'stale')
  assert.equal(rows.find((r) => r.name === 'c').state, 'missing')
})
test('explicit exclusions stay excluded even with old snapshot evidence', () => {
  const rows = coverageResources(
    [schedule],
    [snap('/data/a', 48)],
    [{ namespace: 'app', name: 'a', backupExcluded: true }],
    now,
  )
  assert.equal(rows[0].state, 'excluded')
})
test('SQL dump evidence is tracked separately from its database PVC', () => {
  const rows = coverageResources(
    [schedule],
    [snap('/db.sql')],
    [{ namespace: 'app', name: 'db' }],
    now,
  )
  assert.equal(rows.find((r) => r.kind === 'SQL dump').state, 'fresh')
  assert.equal(rows.find((r) => r.kind === 'PVC').state, 'missing')
})
test('unknown or ambiguous cadence never produces a fresh status', () => {
  for (const schedules of [
    [{ namespace: 'app', spec: { backup: { schedule: 'garbage' } } }],
    [schedule, schedule],
  ]) {
    assert.equal(coverageResources(schedules, [snap('/data/a')], null, now)[0].state, 'unknown')
  }
  assert.equal(coverageResources([], [snap('/data/a')], null, now)[0].state, 'unscheduled')
})
test('invalid and future dates cannot claim current backup coverage', () => {
  assert.equal(
    coverageResources(
      [schedule],
      [{ namespace: 'app', spec: { paths: ['/data/a'], date: 'invalid' } }],
      null,
      now,
    )[0].state,
    'missing',
  )
  assert.equal(coverageResources([schedule], [snap('/data/a', -1)], null, now)[0].state, 'unknown')
})
test('only predictable cron expressions are classified', () => {
  for (const cron of ['garbage', '0 0 1 1 *', '0 25 * * *', '*/0 * * * *', '0 0 * * MON'])
    assert.equal(coverageInterval(cron), null, cron)
  assert.equal(coverageInterval('* * * * *'), 60000)
  assert.equal(coverageInterval('37 */6 * * *'), 6 * 3600000)
  assert.equal(coverageInterval('@daily-random'), 86400000)
})
