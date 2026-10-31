import { test } from 'node:test'
import assert from 'node:assert/strict'
import { humanKey, isDate, parseJSON, short, summarize } from '../src/lib/json.ts'

test('parseJSON: JSON text parsed, other strings kept', () => {
  assert.deepEqual(parseJSON('{"a":1}'), { a: 1 })
  assert.deepEqual(parseJSON(' [1,2] '), [1, 2])
  assert.equal(parseJSON('hello'), 'hello')
  assert.equal(parseJSON('{broken'), '{broken')
  assert.deepEqual(parseJSON({ a: 1 }), { a: 1 })
  assert.equal(parseJSON(undefined), null)
})

test('humanKey', () => {
  assert.equal(humanKey('first_buy_bonus'), 'First buy bonus')
  assert.equal(humanKey('pointsToUnlock'), 'Points to unlock')
  assert.equal(humanKey('n'), 'N')
})

test('isDate and short', () => {
  assert.ok(isDate('2026-10-12T00:00:00Z'))
  assert.ok(isDate('2026-10-12'))
  assert.ok(!isDate('12/10/2026'))
  assert.ok(!isDate(5))
  assert.equal(short({ a: 1, b: 2 }), '{2}')
  assert.equal(short([1, 2, 3]), '[3]')
  assert.equal(short(1234567), '1,234,567')
  assert.equal(short('2026-10-12T00:00:00Z'), '2026-10-12')
  assert.equal(short('x'.repeat(30), 10), 'xxxxxxxxx…')
  assert.equal(short(null), 'null')
  assert.equal(short(true), 'Yes')
})

test('summarize', () => {
  assert.deepEqual(summarize({ season: { n: 1 }, tier: 'gold', points: 1500, a: 1, b: 2 }), {
    chips: [
      { key: 'season', text: '{1}' },
      { key: 'tier', text: 'gold' },
      { key: 'points', text: '1,500' },
    ],
    more: 2,
  })
  assert.deepEqual(summarize([1, 2]), { chips: [], more: 0, text: '2 items' })
  assert.deepEqual(summarize({}), { chips: [], more: 0, text: 'empty' })
  assert.deepEqual(summarize('plain'), { chips: [], more: 0, text: 'plain' })
})
