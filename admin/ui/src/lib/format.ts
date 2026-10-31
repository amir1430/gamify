// Small display helpers, pure so node --test covers them.

const UNITS: [number, string][] = [
  [60, 's'],
  [60, 'm'],
  [24, 'h'],
  [30, 'd'],
  [12, 'mo'],
  [Infinity, 'y'],
]

// "just now", "5m ago", "3h ago", "2d ago", "in 4m" (a future time). now in ms.
export function ago(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return '—'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return '—'
  let s = Math.round((now - t) / 1000)
  const future = s < 0
  s = Math.abs(s)
  if (s < 10) return 'just now'
  let v = s
  for (const [size, unit] of UNITS) {
    if (v < size) return future ? `in ${v}${unit}` : `${v}${unit} ago`
    v = Math.floor(v / size)
  }
  return '—'
}

// 3725000 ms → "1h 2m"; 90000 → "1m 30s"; 2 days → "2d 0h".
export function duration(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d) return `${d}d ${h}h`
  if (h) return `${h}h ${m}m`
  if (m) return `${m}m ${s % 60}s`
  return `${s}s`
}

// 1536 → "1.5 KiB".
export function bytes(n: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) (n /= 1024), i++
  return `${i ? n.toFixed(1) : n} ${units[i]}`
}

// 1234 → "1,234"; 1234567 → "1.2M" when compact.
export function count(n: number, compact = false): string {
  if (!compact || Math.abs(n) < 10_000) return n.toLocaleString('en-US')
  return new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 }).format(n)
}

// Rows as RFC 4180 CSV (CRLF, fields with , " or a newline quoted). A field starting with
// = + - @ is prefixed with ' so a spreadsheet never runs it as a formula.
export function toCSV(header: string[], rows: unknown[][]): string {
  const cell = (v: unknown) => {
    let s = v == null ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v)
    if (/^[=+\-@\t\r]/.test(s) && !/^-?\d+(\.\d+)?$/.test(s)) s = `'${s}`
    return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
  }
  return [header, ...rows].map((r) => r.map(cell).join(',')).join('\r\n') + '\r\n'
}
