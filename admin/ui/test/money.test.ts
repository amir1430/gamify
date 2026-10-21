import { test } from 'node:test'
import assert from 'node:assert/strict'
import { format, parseAmount } from '../src/lib/money.ts'

test('format: minor units with the currency decimals', () => {
  assert.equal(format(123456n, 2), '1,234.56')
  assert.equal(format(5, 2), '0.05')
  assert.equal(format(-150, 2), '−1.50')
  assert.equal(format(1000000, 0), '1,000,000')
  assert.equal(format(9223372036854775807n, 2), '92,233,720,368,547,758.07')
  assert.equal(format(1n, 18), '0.000000000000000001')
})

test('parseAmount: text to minor units, never floats', () => {
  assert.equal(parseAmount('12.3', 2), 1230n)
  assert.equal(parseAmount(' 1,000.05 ', 2), 100005n)
  assert.equal(parseAmount('.5', 2), 50n)
  assert.equal(parseAmount('0.1', 2), 10n)
  assert.equal(parseAmount('5.', 2), 500n)
  assert.equal(parseAmount('-1', 2), -100n)
  assert.equal(parseAmount('−1.50', 2), -150n)
  assert.equal(parseAmount('1,000', 0), 1000n)
  assert.equal(parseAmount('92233720368547758.07', 2), 9223372036854775807n)
  assert.equal(parseAmount('-92233720368547758.08', 2), -9223372036854775808n)
  for (const bad of ['', '.', '-', '1.234', 'abc', '1,00', '1e3', '0x10', '1 000', '92233720368547758.08'])
    assert.throws(() => parseAmount(bad, 2), bad)
  assert.throws(() => parseAmount('1.5', 0))
})

test('format and parseAmount round-trip', () => {
  for (const v of [0n, 1n, -1n, 99n, 100n, 123456789n, -9223372036854775808n]) assert.equal(parseAmount(format(v, 2), 2), v)
})
