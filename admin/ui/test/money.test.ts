import { test } from 'node:test'
import assert from 'node:assert/strict'
import { format, parseAmount, plain } from '../src/lib/money.ts'

test('format: minor units with the currency decimals', () => {
  assert.equal(format(1234, 2), '12.34')
  assert.equal(format(-5, 2), '-0.05')
  assert.equal(format(7, 0), '7')
  assert.equal(format(123456n, 2), '1,234.56')
  assert.equal(format(9223372036854775807n, 2), '92,233,720,368,547,758.07')
  assert.equal(format(1n, 18), '0.000000000000000001')
})

test('parseAmount: text to minor units, never rounded', () => {
  assert.equal(parseAmount('12.34', 2), 1234n)
  assert.equal(parseAmount('12', 2), 1200n)
  assert.equal(parseAmount('-0.5', 2), -50n)
  assert.equal(parseAmount('9223372036854775807', 0), 9223372036854775807n)
  assert.equal(parseAmount('-9223372036854775808', 0), -9223372036854775808n)
  assert.throws(() => parseAmount('0.001', 2))
  for (const bad of ['1.005', '1e3', ' 12', '12 ', '', '-0', '-0.00', '1,000', '.5', '5.', '-', '+1', '0x10', '1 000', '−1'])
    assert.throws(() => parseAmount(bad, 2), bad)
  assert.throws(() => parseAmount('9223372036854775808', 0)) // above int64
  assert.throws(() => parseAmount('1.5', 0))
})

test('plain and parseAmount round-trip', () => {
  for (const v of [1n, -1n, 99n, 100n, 123456789n, 9223372036854775807n, -9223372036854775808n]) assert.equal(parseAmount(plain(v, 2), 2), v)
  assert.equal(plain(0n, 2), '0.00')
})
