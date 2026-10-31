// Brand views (admin.View in Go, GET /api/views): the descriptions the console draws its generic
// pages from, and the pure parts of drawing them: grouping for the menu, cell values, action inputs.
import { format, parseAmount, plain } from './money.ts'

export type FieldType = 'text' | 'int' | 'decimal' | 'money' | 'bool' | 'time' | 'enum' | 'user_id' | 'json' | 'url' | 'image'

export type ViewColumn = {
  key: string
  label: string
  type: FieldType
  scale?: number
  currency_key?: string
  currency?: string
  options?: string[]
  detail_only?: boolean
}
export type ViewFilter = { key: string; label: string; type: FieldType; options?: string[] }
export type ViewField = { key: string; label: string; type: FieldType; required: boolean; scale?: number; currency?: string; options?: string[] }
export type ViewAction = { name: string; title: string; confirm: string; reason: boolean; fields: ViewField[] }
export type ViewDesc = {
  name: string
  title: string
  group: string
  user_filter: string
  columns: ViewColumn[]
  filters: ViewFilter[]
  has_detail: boolean
  actions: ViewAction[]
}
export type ViewRow = Record<string, unknown> & { id: number | string }

// A key as a label when the brand gave none (the API sends the key then): "custom_title" → "Custom title".
export const labelOf = (x: { key: string; label?: string }) =>
  x.label && x.label !== x.key ? x.label : (x.key.charAt(0).toUpperCase() + x.key.slice(1)).replace(/_/g, ' ')

// Views by group, in registration order of each group's first view; '' joins `fallback`.
export function groupViews(views: ViewDesc[], fallback = 'More'): { label: string; views: ViewDesc[] }[] {
  const out: { label: string; views: ViewDesc[] }[] = []
  for (const v of views) {
    const label = v.group || fallback
    let g = out.find((x) => x.label === label)
    if (!g) out.push((g = { label, views: [] }))
    g.views.push(v)
  }
  return out
}

// v as an http(s) URL for an href or src, else null (never javascript: or data:).
export function webURL(v: unknown): string | null {
  if (typeof v !== 'string') return null
  try {
    const u = new URL(v)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.host ? u.href : null
  } catch {
    return null
  }
}

// A link's text: host and path, without the scheme and a trailing slash.
export const linkText = (href: string) => href.replace(/^https?:\/\//, '').replace(/\/$/, '')

// An integer the API sent as a number, or as a string beyond 2^53.
const big = (v: unknown): bigint | null => {
  if (typeof v === 'number' && Number.isInteger(v)) return BigInt(v)
  if (typeof v === 'string' && /^-?\d+$/.test(v)) return BigInt(v)
  return null
}

// A cell as text, for the types drawn as plain text (bool, time, user_id and enum get their own
// look in the page, but fall back here). decimals: the currency's, for money. IDs (user_id, a key
// "id" or "…_id") aren't grouped.
export function cellText(col: { type: FieldType; scale?: number; key?: string }, v: unknown, decimals = 0): string {
  if (v == null || v === '') return '—'
  switch (col.type) {
    case 'int':
    case 'user_id': {
      const n = big(v)
      const id = col.type === 'user_id' || col.key === 'id' || !!col.key?.endsWith('_id')
      return n == null || id ? String(v) : format(n, 0)
    }
    case 'decimal':
    case 'money': {
      const n = big(v)
      return n == null ? String(v) : format(n, col.type === 'money' ? decimals : (col.scale ?? 0))
    }
    case 'bool':
      return v ? 'Yes' : 'No'
    case 'time':
      return typeof v === 'string' ? new Date(v).toLocaleString() : String(v)
    case 'json':
      return typeof v === 'string' ? v : JSON.stringify(v)
  }
  return String(v)
}

// RFC 3339 ⇄ an <input type="datetime-local"> value (the browser's local time, to the minute).
export function toLocalInput(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}
export function fromLocalInput(s: string): string {
  const d = new Date(s)
  return isNaN(d.getTime()) ? '' : d.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

// A form value for a field, prefilled from the row when it has the same key (bool: a boolean,
// anything else: the text the input shows).
export function initialValue(f: ViewField, row: ViewRow | undefined, decimals = 0): string | boolean {
  const v = row?.[f.key]
  if (f.type === 'bool') return v === true
  if (v == null || v === '') return ''
  switch (f.type) {
    case 'time':
      return typeof v === 'string' ? toLocalInput(v) : ''
    case 'money':
    case 'decimal': {
      const n = big(v)
      return n == null ? '' : plain(n, f.type === 'money' ? decimals : (f.scale ?? 0))
    }
    case 'json':
      return typeof v === 'string' ? v : JSON.stringify(v, null, 2)
    case 'enum':
      return f.options?.includes(String(v)) ? String(v) : ''
  }
  return typeof v === 'object' ? '' : String(v)
}

// One input as the API takes it (see admin.Call): undefined = not sent; error = what to fix.
// Integers go as bigints (the request body writes them as exact JSON integers).
export function inputValue(f: ViewField, raw: string | boolean, decimals = 0): { value?: unknown; error?: string } {
  if (f.type === 'bool') return { value: raw === true }
  const s = String(raw).trim()
  if (s === '') return f.required ? { error: 'Required.' } : {}
  switch (f.type) {
    case 'int':
    case 'user_id':
      return /^-?\d{1,19}$/.test(s) ? { value: BigInt(s) } : { error: 'A whole number.' }
    case 'money':
    case 'decimal': {
      const d = f.type === 'money' ? decimals : (f.scale ?? 0)
      try {
        return { value: parseAmount(s, d) }
      } catch {
        return { error: d ? `A number with at most ${d} decimals.` : 'A whole number.' }
      }
    }
    case 'time': {
      const iso = fromLocalInput(s)
      return iso ? { value: iso } : { error: 'A date and time.' }
    }
    case 'json':
      try {
        return { value: JSON.parse(s) }
      } catch {
        return { error: 'Valid JSON.' }
      }
    case 'enum':
      return f.options?.includes(s) ? { value: s } : { error: 'Pick one.' }
    case 'url':
    case 'image':
      return webURL(s) ? { value: s } : { error: 'A link starting with https://.' }
  }
  return { value: s }
}

// A filter value as the query string takes it ('' = unset).
export function filterValue(f: ViewFilter, raw: string): { value: string; error?: string } {
  const s = raw.trim()
  if (s === '') return { value: '' }
  switch (f.type) {
    case 'int':
    case 'user_id':
    case 'money':
    case 'decimal':
      return /^-?\d{1,19}$/.test(s) ? { value: s } : { value: '', error: 'A whole number.' }
    case 'time': {
      const iso = fromLocalInput(s)
      return iso ? { value: iso } : { value: '', error: 'A date and time.' }
    }
  }
  return { value: s }
}
