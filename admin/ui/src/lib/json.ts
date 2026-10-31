// Reading a view's json column: parse it, summarise it for a table cell, label its keys.

export type Json = null | boolean | number | string | Json[] | { [k: string]: Json }

// The column's value as JSON: a string is parsed when it is JSON text, else kept as a string.
export function parseJSON(v: unknown): Json {
  if (typeof v !== 'string') return (v ?? null) as Json
  const t = v.trim()
  if (!/^[[{"]|^-?\d|^(true|false|null)$/.test(t)) return v
  try {
    return JSON.parse(t) as Json
  } catch {
    return v
  }
}

export const isObject = (v: Json): v is { [k: string]: Json } => !!v && typeof v === 'object' && !Array.isArray(v)

// first_buy_bonus / firstBuyBonus → "First buy bonus".
export function humanKey(k: string): string {
  const s = k
    .replace(/([a-z\d])([A-Z])/g, '$1 $2')
    .replace(/[_-]+/g, ' ')
    .trim()
    .toLowerCase()
  return s ? s[0].toUpperCase() + s.slice(1) : k
}

const ISO = /^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?)?$/

// An RFC 3339 / ISO date string.
export const isDate = (v: Json): v is string => typeof v === 'string' && ISO.test(v) && !Number.isNaN(Date.parse(v))

export type Chip = { key: string; text: string }

// A scalar as a short text; containers as their size ("{3}", "[5]").
export function short(v: Json, max = 24): string {
  if (v === null) return 'null'
  if (Array.isArray(v)) return `[${v.length}]`
  if (typeof v === 'object') return `{${Object.keys(v).length}}`
  if (typeof v === 'number') return v.toLocaleString('en-US')
  if (typeof v === 'boolean') return v ? 'Yes' : 'No'
  const s = isDate(v) ? v.slice(0, 10) : String(v)
  return s.length > max ? s.slice(0, max - 1) + '…' : s
}

// A table cell's summary: up to max top-level fields as chips, how many more, or for anything
// but an object a single text.
export function summarize(v: Json, max = 3): { chips: Chip[]; more: number; text?: string } {
  if (Array.isArray(v)) return { chips: [], more: 0, text: v.length === 1 ? '1 item' : `${v.length} items` }
  if (!isObject(v)) return { chips: [], more: 0, text: short(v, 60) }
  const keys = Object.keys(v)
  if (!keys.length) return { chips: [], more: 0, text: 'empty' }
  return { chips: keys.slice(0, max).map((k) => ({ key: k, text: short(v[k]) })), more: Math.max(0, keys.length - max) }
}
