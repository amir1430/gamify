import { test } from 'node:test'
import assert from 'node:assert/strict'
import { ago, bytes, count, duration, toCSV } from '../src/lib/format.ts'

test('ago: relative to now', () => {
  const now = Date.parse('2026-10-31T12:00:00Z')
  assert.equal(ago('2026-10-31T11:59:58Z', now), 'just now')
  assert.equal(ago('2026-10-31T11:59:00Z', now), '1m ago')
  assert.equal(ago('2026-10-31T11:55:00Z', now), '5m ago')
  assert.equal(ago('2026-10-31T09:00:00Z', now), '3h ago')
  assert.equal(ago('2026-10-29T12:00:00Z', now), '2d ago')
  assert.equal(ago('2026-08-31T12:00:00Z', now), '2mo ago')
  assert.equal(ago('2024-10-31T12:00:00Z', now), '2y ago')
  assert.equal(ago('2026-10-31T12:04:00Z', now), 'in 4m')
  assert.equal(ago(null, now), '—')
  assert.equal(ago('nope', now), '—')
})

test('duration, bytes, count', () => {
  assert.equal(duration(3_725_000), '1h 2m')
  assert.equal(duration(90_000), '1m 30s')
  assert.equal(duration(2 * 86_400_000), '2d 0h')
  assert.equal(duration(-5), '0s')
  assert.equal(bytes(512), '512 B')
  assert.equal(bytes(1536), '1.5 KiB')
  assert.equal(bytes(5 * 1024 ** 3), '5.0 GiB')
  assert.equal(count(1234), '1,234')
  assert.equal(count(1_234_567, true), '1.2M')
  assert.equal(count(9_999, true), '9,999')
})

test('toCSV: quoting and formula guard', () => {
  assert.equal(toCSV(['a', 'b'], [[1, 'x,y']]), 'a,b\r\n1,"x,y"\r\n')
  assert.equal(toCSV(['a'], [['say "hi"']]), 'a\r\n"say ""hi"""\r\n')
  assert.equal(toCSV(['a'], [['=1+1'], ['-5'], ['-x'], [null], [{ k: 1 }]]), 'a\r\n\'=1+1\r\n-5\r\n\'-x\r\n\r\n"{""k"":1}"\r\n')
})
