import { test } from 'node:test'
import assert from 'node:assert/strict'
import { actionState, cellText, csvValue, fieldOptions, fieldSections, filterChip, filterValue, formDirty, optionLabel, splitFilters, fromLocalInput, groupViews, initialValue, inputValue, labelOf, linkText, toLocalInput, usesPickList, webURL, type ViewDesc, type ViewField } from '../src/lib/views.ts'

const view = (name: string, group: string): ViewDesc => ({
  name,
  title: name,
  group,
  user_filter: '',
  columns: [],
  filters: [],
  has_detail: false,
  actions: [],
})

test('groupViews: by group, in first-seen order; no group joins the fallback', () => {
  const g = groupViews([view('a', 'Predict'), view('b', ''), view('c', 'Predict'), view('d', 'Players')])
  assert.deepEqual(
    g.map((x) => [x.label, x.views.map((v) => v.name)]),
    [
      ['Predict', ['a', 'c']],
      ['More', ['b']],
      ['Players', ['d']],
    ],
  )
})

test('labelOf: the label, else the key in words', () => {
  assert.equal(labelOf({ key: 'custom_title' }), 'Custom title')
  assert.equal(labelOf({ key: 'x', label: 'Shown' }), 'Shown')
  assert.equal(labelOf({ key: 'gamma_event_id', label: 'gamma_event_id' }), 'Gamma event id')
})

test('cellText: integers grouped, money and decimals in their scale, big values exact', () => {
  assert.equal(cellText({ type: 'int' }, 1234567), '1,234,567')
  assert.equal(cellText({ type: 'int', key: 'id' }, 1112205), '1112205')
  assert.equal(cellText({ type: 'int', key: 'gamma_id' }, 1112205), '1112205')
  assert.equal(cellText({ type: 'money' }, 1234, 2), '12.34')
  assert.equal(cellText({ type: 'money' }, '9223372036854775807', 2), '92,233,720,368,547,758.07')
  assert.equal(cellText({ type: 'decimal', scale: 3 }, 2375), '2.375')
  assert.equal(cellText({ type: 'bool' }, false), 'No')
  assert.equal(cellText({ type: 'json' }, { a: 1 }), '{"a":1}')
  assert.equal(cellText({ type: 'text' }, null), '—')
  assert.equal(cellText({ type: 'text' }, ''), '—')
})

const field = (type: ViewField['type'], extra: Partial<ViewField> = {}): ViewField => ({ key: 'k', label: '', type, required: false, ...extra })

test('inputValue: typed values, unset when empty and optional', () => {
  assert.deepEqual(inputValue(field('text'), '  hi '), { value: 'hi' })
  assert.deepEqual(inputValue(field('text'), ''), {})
  assert.deepEqual(inputValue(field('text', { required: true }), ' '), { error: 'Required.' })
  assert.deepEqual(inputValue(field('int'), '9223372036854775807'), { value: 9223372036854775807n })
  assert.ok(inputValue(field('int'), '1.5').error)
  assert.deepEqual(inputValue(field('money'), '12.34', 2), { value: 1234n })
  assert.ok(inputValue(field('money'), '12.345', 2).error)
  assert.deepEqual(inputValue(field('decimal', { scale: 1 }), '-0.5'), { value: -5n })
  assert.deepEqual(inputValue(field('bool'), true), { value: true })
  assert.deepEqual(inputValue(field('bool'), ''), { value: false })
  assert.deepEqual(inputValue(field('enum', { options: ['a', 'b'] }), 'b'), { value: 'b' })
  assert.ok(inputValue(field('enum', { options: ['a'] }), 'z').error)
  assert.deepEqual(inputValue(field('json'), '{"x":[1]}'), { value: { x: [1] } })
  assert.ok(inputValue(field('json'), '{').error)
  const t = inputValue(field('time'), '2030-01-02T03:04').value as string
  assert.match(t, /^2030-01-0[12]T\d\d:\d\d:00Z$/)
})

test('time inputs round-trip through RFC 3339', () => {
  const iso = fromLocalInput('2030-06-15T12:30')
  assert.equal(toLocalInput(iso), '2030-06-15T12:30')
  assert.equal(fromLocalInput('nope'), '')
  assert.equal(toLocalInput('nope'), '')
})

test('initialValue: prefilled from the row when it has the key', () => {
  const row = { id: 1, k: 'x', on: true, amt: 1234, t: '2030-06-15T12:30:00Z', obj: { a: 1 } }
  assert.equal(initialValue(field('text'), row), 'x')
  assert.equal(initialValue({ ...field('bool'), key: 'on' }, row), true)
  assert.equal(initialValue({ ...field('bool'), key: 'missing' }, row), false)
  assert.equal(initialValue({ ...field('money'), key: 'amt' }, row, 2), '12.34')
  assert.equal(initialValue({ ...field('time'), key: 't' }, row), toLocalInput('2030-06-15T12:30:00Z'))
  assert.equal(initialValue({ ...field('enum', { options: ['y'] }) }, row), '')
  assert.equal(initialValue({ ...field('text'), key: 'obj' }, row), '')
  assert.equal(initialValue(field('text'), undefined), '')
})

test('filterValue: checked like the API, empty = unset', () => {
  assert.deepEqual(filterValue({ key: 'u', label: '', type: 'user_id' }, ' 42 '), { value: '42' })
  assert.ok(filterValue({ key: 'u', label: '', type: 'user_id' }, 'abc').error)
  assert.deepEqual(filterValue({ key: 's', label: '', type: 'text' }, ''), { value: '' })
})

test('webURL: http(s) only; linkText drops the scheme', () => {
  assert.equal(webURL('https://polymarket.com/event/x'), 'https://polymarket.com/event/x')
  assert.equal(webURL('http://a.b/'), 'http://a.b/')
  for (const bad of ['javascript:alert(1)', 'data:image/png;base64,AA', 'polymarket.com/x', '', 5, null]) assert.equal(webURL(bad), null, String(bad))
  assert.equal(linkText('https://polymarket.com/event/x/'), 'polymarket.com/event/x')
  const f: ViewField = { key: 'u', label: 'u', type: 'url', required: false }
  assert.deepEqual(inputValue(f, 'https://a.b/c'), { value: 'https://a.b/c' })
  assert.ok(inputValue(f, 'javascript:alert(1)').error)
})

const strs = (x: Partial<ViewField> = {}): ViewField => ({ key: 'blocks', label: 'blocks', type: 'strings', required: false, ...x })

test('a strings field prefills from the row and sends a list, or nothing when empty', () => {
  assert.deepEqual(initialValue(strs(), { id: 1, blocks: ['bets', 'swaps'] }), ['bets', 'swaps'])
  assert.deepEqual(initialValue(strs(), { id: 1, blocks: null }), [])
  assert.deepEqual(inputValue(strs(), ['a']), { value: ['a'] })
  assert.deepEqual(inputValue(strs(), []), {})
  assert.deepEqual(inputValue(strs({ required: true }), []), { error: 'Required.' })
  assert.equal(cellText({ type: 'strings' }, ['a', 'b']), 'a, b')
})

test('an enum with options_from takes what the row offered', () => {
  const f: ViewField = { key: 'm', label: 'm', type: 'enum', required: false, options_from: 'list' }
  const row = { id: 1, m: 'x', list: [{ value: 'x', hint: 'first' }] }
  assert.equal(initialValue(f, row), 'x')
  assert.deepEqual(inputValue(f, 'x'), { value: 'x' })
  assert.deepEqual(fieldOptions(f, row), [{ value: 'x', hint: 'first' }])
  assert.deepEqual(fieldOptions(f, { id: 1 }), [])
  assert.equal(usesPickList(f), true)
})

test('options with help come first', () => {
  const o = fieldOptions(strs({ options: ['dice', 'bets', 'mines'], option_help: { bets: 'every game' } }), undefined)
  assert.deepEqual(o.map((x) => x.value), ['bets', 'dice', 'mines'])
})

test('sections join neighbours with the same group', () => {
  const s = fieldSections([strs({ key: 'a', group: 'A' }), strs({ key: 'b', group: 'A' }), strs({ key: 'c' }), strs({ key: 'd', group: 'A' })])
  assert.deepEqual(s.map((x) => [x.group, x.fields.length]), [['A', 2], ['', 1], ['A', 1]])
})

test('option labels name a value, in cells and in the choices', () => {
  const labels = { option_labels: { flipduel: 'Coin Flip Duel' } }
  assert.equal(optionLabel(labels, 'flipduel'), 'Coin Flip Duel')
  assert.equal(optionLabel(labels, 'dice'), 'dice')
  assert.equal(cellText({ type: 'strings', ...labels }, ['flipduel', 'dice']), 'Coin Flip Duel, dice')
  assert.equal(cellText({ type: 'enum', ...labels }, 'flipduel'), 'Coin Flip Duel')
  assert.equal(fieldOptions(strs({ options: ['flipduel'], ...labels }), undefined)[0].label, 'Coin Flip Duel')
})

test('an action without a state on the row is plainly available', () => {
  assert.deepEqual(actionState({ id: 1, _actions: { lift: { unavailable: 'no' } } }, 'lift'), { unavailable: 'no' })
  assert.deepEqual(actionState({ id: 1 }, 'lift'), {})
  assert.deepEqual(actionState(undefined, 'lift'), {})
})

test('a form is dirty only once a value changed', () => {
  const start = { a: 'x', on: false, l: ['b'] }
  assert.equal(formDirty({ a: 'x', on: false, l: ['b'] }, start), false)
  assert.equal(formDirty({ a: 'y', on: false, l: ['b'] }, start), true)
  assert.equal(formDirty({ a: 'x', on: false, l: [] }, start), true)
})

test('filters split into the menu, the search and their own inputs', () => {
  const f = (key: string, type: ViewField['type']) => ({ key, label: key, type })
  const s = splitFilters([f('kind', 'enum'), f('title', 'text'), f('slug', 'text'), f('open', 'bool'), f('user', 'user_id')])
  assert.deepEqual(s.menu.map((x) => x.key), ['kind', 'open'])
  assert.equal(s.search?.key, 'title')
  assert.deepEqual(s.inputs.map((x) => x.key), ['slug', 'user'])
  assert.equal(filterChip({ key: 'x', label: 'x', type: 'bool' }, 'false'), 'No')
  assert.equal(filterChip({ key: 'x', label: 'x', type: 'enum', option_labels: { a: 'Alpha' } }, 'a'), 'Alpha')
})

test('a CSV cell keeps money exact and names values', () => {
  assert.equal(csvValue({ key: 'p', label: 'p', type: 'money' }, { id: 1, p: 1050 }, 2), '10.50')
  assert.equal(csvValue({ key: 'b', label: 'b', type: 'strings' }, { id: 1, b: ['a', 'b'] }), 'a, b')
  assert.equal(csvValue({ key: 'n', label: 'n', type: 'text' }, { id: 1 }), '')
})
