import { test } from 'node:test'
import assert from 'node:assert/strict'
import { cellText, filterValue, fromLocalInput, groupViews, initialValue, inputValue, labelOf, linkText, toLocalInput, webURL, type ViewDesc, type ViewField } from '../src/lib/views.ts'

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
