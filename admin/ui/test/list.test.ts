import { test } from 'node:test'
import assert from 'node:assert/strict'
import { applyList, by, joinParam, matchText, pageOf, rangeSince, splitParam, type ListSpec } from '../src/lib/list.ts'

type G = { name: string; on: boolean; variants: string[] }
const rows: G[] = [
  { name: 'dice', on: true, variants: ['classic'] },
  { name: 'Arena', on: false, variants: [] },
  { name: 'crash', on: true, variants: ['turbo', 'classic'] },
]
const spec: ListSpec<G> = {
  text: (g) => [g.name, ...g.variants],
  groups: [
    { key: 'status', label: 'Status', options: [], match: (g, v) => (v === 'on') === g.on },
    { key: 'variant', label: 'Variant', options: [], match: (g, v) => g.variants.includes(v) },
  ],
  sorts: [
    { value: 'name', label: 'A–Z', compare: by((g) => g.name) },
    { value: 'variants', label: 'Most variants', compare: by((g) => g.variants.length, true) },
  ],
}
const names = (gs: G[]) => gs.map((g) => g.name)

test('matchText: every word, any field, any case, @ ignored', () => {
  assert.equal(matchText(['Alice', 'alice_w'], 'ALI'), true)
  assert.equal(matchText(['Alice', 42], '42'), true)
  assert.equal(matchText(['Alice'], '@ali'), true)
  assert.equal(matchText(['crash', 'turbo'], 'cra tur'), true)
  assert.equal(matchText(['crash', 'turbo'], 'cra dice'), false)
  assert.equal(matchText([null, undefined], ''), true)
})

test('applyList: search, OR within a group, AND across, sort', () => {
  const s = (q: string, filters: Record<string, string[]> = {}, sort = '') => names(applyList(rows, spec, { q, filters, sort }))
  assert.deepEqual(s(''), ['Arena', 'crash', 'dice'])
  assert.deepEqual(s('classic'), ['crash', 'dice'])
  assert.deepEqual(s('', { status: ['on', 'off'] }), ['Arena', 'crash', 'dice'])
  assert.deepEqual(s('', { status: ['on'], variant: ['turbo'] }), ['crash'])
  assert.deepEqual(s('', { status: [] }), ['Arena', 'crash', 'dice'])
  assert.deepEqual(s('', {}, 'variants'), ['crash', 'dice', 'Arena'])
})

test('pageOf: clamps and reports the range', () => {
  const xs = Array.from({ length: 23 }, (_, i) => i)
  assert.deepEqual({ ...pageOf(xs, 2, 10), rows: undefined }, { rows: undefined, page: 2, pages: 3, from: 11, to: 20 })
  assert.deepEqual(pageOf(xs, 9, 10).rows, [20, 21, 22])
  assert.equal(pageOf(xs, 0, 10).page, 1)
  assert.deepEqual({ ...pageOf([], 1, 10), rows: undefined }, { rows: undefined, page: 1, pages: 1, from: 0, to: 0 })
})

test('params and ranges', () => {
  assert.deepEqual(splitParam('on,,off'), ['on', 'off'])
  assert.equal(joinParam(['a', 'b', 'a']), 'a,b')
  assert.equal(rangeSince('24h', Date.parse('2026-10-31T12:00:00Z')), '2026-10-30T12:00:00Z')
  assert.equal(rangeSince('nope'), '')
  assert.deepEqual([null, 'b', 'a'].sort(by((x) => x)), ['a', 'b', null])
})
