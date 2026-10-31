// Lists held whole in the browser (games, console users, online players): search, filters, sort and
// pages, pure so node --test covers them. Lists the API pages by keyset filter on the server instead.

// A filter group: an option matches a row; several options of one group match any of them (OR),
// different groups must all match (AND).
export type FilterGroup<T> = { key: string; label: string; options: { value: string; label: string }[]; match: (row: T, value: string) => boolean }

export type Sort<T> = { value: string; label: string; compare: (a: T, b: T) => number }

export type ListSpec<T> = {
  text: (row: T) => (string | number | null | undefined)[] // what the search box looks through
  groups?: FilterGroup<T>[]
  sorts?: Sort<T>[]
}

export type ListState = { q: string; filters: Record<string, string[]>; sort: string }

// Whether every word of q is part of one of the row's texts, ignoring case and a leading @.
export function matchText(texts: (string | number | null | undefined)[], q: string): boolean {
  const words = q.toLowerCase().replace(/(^|\s)@/g, '$1').split(/\s+/).filter(Boolean)
  if (!words.length) return true
  const hay = texts.filter((t) => t != null && t !== '').map((t) => String(t).toLowerCase())
  return words.every((w) => hay.some((h) => h.includes(w)))
}

// rows searched, filtered and sorted (the first sort when state.sort is unknown; stable otherwise).
export function applyList<T>(rows: readonly T[], spec: ListSpec<T>, state: ListState): T[] {
  const groups = (spec.groups ?? []).filter((g) => state.filters[g.key]?.length)
  const out = rows.filter((r) => matchText(spec.text(r), state.q) && groups.every((g) => state.filters[g.key].some((v) => g.match(r, v))))
  const sort = spec.sorts?.find((s) => s.value === state.sort) ?? spec.sorts?.[0]
  return sort ? out.map((r, i) => [r, i] as const).sort((a, b) => sort.compare(a[0], b[0]) || a[1] - b[1]).map(([r]) => r) : out
}

// One page of n rows (page from 1, clamped to the last page) and the range shown, "21–40".
export function pageOf<T>(rows: readonly T[], page: number, size: number): { rows: T[]; page: number; pages: number; from: number; to: number } {
  const pages = Math.max(1, Math.ceil(rows.length / size))
  const p = Math.min(Math.max(1, Math.floor(page) || 1), pages)
  const start = (p - 1) * size
  const slice = rows.slice(start, start + size)
  return { rows: slice, page: p, pages, from: slice.length ? start + 1 : 0, to: start + slice.length }
}

// ?status=on,off ⇄ ['on', 'off'].
export const splitParam = (v: string) => v.split(',').filter(Boolean)
export const joinParam = (vs: string[]) => [...new Set(vs)].join(',')

// Time range presets for lists with ?since=: the RFC 3339 start for a preset, now in ms.
export const RANGES = [
  { value: '1h', label: 'Last hour', ms: 3600_000 },
  { value: '24h', label: 'Last 24 hours', ms: 86_400_000 },
  { value: '7d', label: 'Last 7 days', ms: 7 * 86_400_000 },
  { value: '30d', label: 'Last 30 days', ms: 30 * 86_400_000 },
  { value: '90d', label: 'Last 90 days', ms: 90 * 86_400_000 },
] as const

export function rangeSince(range: string, now = Date.now()): string {
  const r = RANGES.find((x) => x.value === range)
  return r ? new Date(now - r.ms).toISOString().replace(/\.\d{3}Z$/, 'Z') : ''
}

// A comparator of strings or numbers, nulls last.
export function by<T>(key: (row: T) => string | number | null | undefined, desc = false): (a: T, b: T) => number {
  return (a, b) => {
    const x = key(a)
    const y = key(b)
    if (x == null || x === '') return y == null || y === '' ? 0 : 1
    if (y == null || y === '') return -1
    const c = typeof x === 'number' && typeof y === 'number' ? x - y : String(x).localeCompare(String(y), undefined, { numeric: true, sensitivity: 'base' })
    return desc ? -c : c
  }
}
