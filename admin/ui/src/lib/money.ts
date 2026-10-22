// Amounts are integer minor units (int64 on the server) with a currency's display decimals
// (/api/me's currencies). Text ⇄ amount goes through bigint, never floats.

const MAX = (1n << 63n) - 1n

// 1234, 2 → "12.34"; -5, 2 → "-0.05"; 1234567, 0 → "1,234,567" (grouped for reading only).
export function format(amount: bigint | number, decimals: number): string {
  const n = typeof amount === 'bigint' ? amount : BigInt(Math.trunc(amount))
  const abs = n < 0n ? -n : n
  const s = abs.toString().padStart(decimals + 1, '0')
  const whole = s.slice(0, s.length - decimals).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  const frac = decimals > 0 ? '.' + s.slice(s.length - decimals) : ''
  return (n < 0n ? '-' : '') + whole + frac
}

// What an admin types into an amount field: digits, an optional leading "-", an optional "."
// with 1..decimals digits. "12.34", 2 → 1234n. Throws on anything else (spaces, grouping, exponents,
// more decimals than the currency has, "-0") or beyond int64: an amount is never rounded or guessed.
export function parseAmount(s: string, decimals: number): bigint {
  const m = /^(-)?(\d+)(?:\.(\d+))?$/.exec(s)
  if (!m || (m[3] ?? '').length > decimals) throw new Error(`not an amount with at most ${decimals} decimals: ${JSON.stringify(s)}`)
  const v = BigInt(m[2]) * 10n ** BigInt(decimals) + BigInt((m[3] ?? '').padEnd(decimals, '0') || '0')
  if (m[1] && v === 0n) throw new Error('-0 is not an amount')
  if (v > (m[1] ? MAX + 1n : MAX)) throw new Error(`amount out of range: ${s}`)
  return m[1] ? -v : v
}

// An amount for an input's value (no grouping, so parseAmount takes it back).
export const plain = (amount: bigint | number, decimals: number) => format(amount, decimals).replace(/,/g, '')
