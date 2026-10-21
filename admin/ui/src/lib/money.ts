// Amounts are integer minor units (int64 on the server) with a currency's display decimals
// (/api/me's currencies). Text ⇄ amount goes through bigint, never floats.

const MAX = (1n << 63n) - 1n
const MINUS = '−' // −, as the casino shows it

// 123456n, 2 → "1,234.56"; -150, 2 → "−1.50"; 1000, 0 → "1,000".
export function format(amount: bigint | number, decimals: number): string {
  const n = typeof amount === 'bigint' ? amount : BigInt(Math.trunc(amount))
  const abs = n < 0n ? -n : n
  const s = abs.toString().padStart(decimals + 1, '0')
  const whole = s.slice(0, s.length - decimals).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  const frac = decimals > 0 ? '.' + s.slice(s.length - decimals) : ''
  return (n < 0n ? MINUS : '') + whole + frac
}

// "1,234.5", 2 → 123450n; "-1" or "−1" → negative; at most `decimals` fraction digits. Throws on
// anything else, or beyond int64.
export function parseAmount(s: string, decimals: number): bigint {
  const m = /^([-−])?(\d{1,3}(?:,\d{3})+|\d*)(?:\.(\d*))?$/.exec(s.trim())
  if (!m || (!m[2] && !m[3]) || (m[3] ?? '').length > decimals) throw new Error(`not an amount with at most ${decimals} decimals: ${s}`)
  const v = BigInt(m[2].replace(/,/g, '') || '0') * 10n ** BigInt(decimals) + BigInt((m[3] ?? '').padEnd(decimals, '0') || '0')
  if (v > (m[1] ? MAX + 1n : MAX)) throw new Error(`amount too large: ${s}`)
  return m[1] ? -v : v
}
