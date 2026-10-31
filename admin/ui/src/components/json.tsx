import { Braces, Check, ChevronRight, Copy } from 'lucide-react'
import { useState } from 'react'
import { humanKey, isDate, isObject, parseJSON, summarize, type Json } from '@/lib/json'
import { cn } from '@/lib/utils'

// A json column in a table: its top-level fields as small key/value chips and "+N", never the raw
// text. The row's detail dialog shows all of it.
export function JsonSummary({ value }: { value: unknown }) {
  const v = parseJSON(value)
  const s = summarize(v)
  if (s.text != null) return <span className="text-xs text-muted-foreground">{s.text}</span>
  return (
    <span className="flex max-w-md items-center gap-1 overflow-hidden" title="Open the row for every field">
      <Braces className="size-3.5 shrink-0 text-muted-foreground/70" />
      {s.chips.map((c) => (
        <span key={c.key} className="inline-flex max-w-40 shrink-0 items-center gap-1 rounded-md border bg-muted/40 px-1.5 py-0.5 text-[11px] leading-none">
          <span className="text-muted-foreground">{humanKey(c.key)}</span>
          <span className="truncate font-medium">{c.text}</span>
        </span>
      ))}
      {s.more > 0 && <span className="shrink-0 text-[11px] text-muted-foreground">+{s.more}</span>}
    </span>
  )
}

function Scalar({ v }: { v: Json }) {
  if (v === null) return <span className="text-muted-foreground/60">—</span>
  if (typeof v === 'boolean')
    return <span className={cn('rounded px-1.5 py-0.5 text-[11px] font-medium', v ? 'bg-ok/10 text-ok' : 'bg-secondary text-muted-foreground')}>{v ? 'Yes' : 'No'}</span>
  if (typeof v === 'number') return <span className="num">{v.toLocaleString()}</span>
  if (isDate(v))
    return (
      <time dateTime={v} title={v}>
        {v.length <= 10 ? new Date(v + 'T00:00:00Z').toLocaleDateString(undefined, { timeZone: 'UTC' }) : new Date(v).toLocaleString()}
      </time>
    )
  return <span className="break-words">{String(v)}</span>
}

// Every element an object with the same scalar keys: drawn as a table.
function asRecords(v: Json[]): string[] | null {
  if (v.length < 1 || !v.every(isObject)) return null
  const keys = Object.keys(v[0] as object)
  const same = v.every((o) => {
    const k = Object.keys(o as object)
    return k.length === keys.length && k.every((x) => keys.includes(x) && !(typeof (o as Record<string, Json>)[x] === 'object' && (o as Record<string, Json>)[x] !== null))
  })
  return same && keys.length <= 8 ? keys : null
}

function Records({ rows, keys }: { rows: Record<string, Json>[]; keys: string[] }) {
  return (
    <div className="overflow-x-auto rounded-md border">
      <table className="w-full text-xs">
        <thead className="bg-muted/40 text-muted-foreground">
          <tr>
            {keys.map((k) => (
              <th key={k} className="px-2.5 py-1.5 text-left font-medium whitespace-nowrap">
                {humanKey(k)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i} className="border-t">
              {keys.map((k) => (
                <td key={k} className={cn('px-2.5 py-1.5', typeof r[k] === 'number' && 'text-right')}>
                  <Scalar v={r[k]} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function Value({ v, depth }: { v: Json; depth: number }) {
  if (Array.isArray(v)) {
    if (!v.length) return <span className="text-xs text-muted-foreground">empty list</span>
    const keys = asRecords(v)
    if (keys) return <Records rows={v as Record<string, Json>[]} keys={keys} />
    if (v.every((x) => x === null || typeof x !== 'object'))
      return (
        <span className="flex flex-wrap gap-1">
          {v.map((x, i) => (
            <span key={i} className="rounded-md border bg-muted/40 px-1.5 py-0.5 text-xs">
              <Scalar v={x} />
            </span>
          ))}
        </span>
      )
    return <Tree obj={Object.fromEntries(v.map((x, i) => [`#${i + 1}`, x]))} depth={depth} raw />
  }
  if (isObject(v)) return <Tree obj={v} depth={depth} />
  return <Scalar v={v} />
}

// Fields as rows: label on the left, value on the right; nested objects fold (open two levels deep).
function Tree({ obj, depth, raw }: { obj: Record<string, Json>; depth: number; raw?: boolean }) {
  const keys = Object.keys(obj)
  if (!keys.length) return <span className="text-xs text-muted-foreground">empty</span>
  return (
    <dl className={cn('divide-y', depth > 0 && 'rounded-md border bg-background/40')}>
      {keys.map((k) => (
        <Row key={k} label={raw ? k : humanKey(k)} name={k} v={obj[k]} depth={depth} />
      ))}
    </dl>
  )
}

function Row({ label, name, v, depth }: { label: string; name: string; v: Json; depth: number }) {
  const nested = v !== null && typeof v === 'object' && !(Array.isArray(v) && (asRecords(v) || v.every((x) => x === null || typeof x !== 'object')))
  const [open, setOpen] = useState(depth < 2)
  const size = Array.isArray(v) ? `${v.length} item${v.length === 1 ? '' : 's'}` : isObject(v) ? `${Object.keys(v).length} field${Object.keys(v).length === 1 ? '' : 's'}` : ''
  if (nested)
    return (
      <div className="px-3 py-2">
        <button type="button" onClick={() => setOpen((o) => !o)} className="flex w-full items-center gap-1.5 text-left text-xs font-medium" title={name}>
          <ChevronRight className={cn('size-3.5 text-muted-foreground transition-transform', open && 'rotate-90')} />
          {label}
          <span className="font-normal text-muted-foreground">· {size}</span>
        </button>
        {open && (
          <div className="mt-2 ml-5">
            <Value v={v} depth={depth + 1} />
          </div>
        )}
      </div>
    )
  return (
    <div className="grid gap-1 px-3 py-2 sm:grid-cols-[minmax(9rem,14rem)_1fr] sm:gap-4">
      <dt className="text-xs text-muted-foreground" title={name}>
        {label}
      </dt>
      <dd className="min-w-0 text-sm">
        <Value v={v} depth={depth + 1} />
      </dd>
    </div>
  )
}

// A json column in full: a readable tree (default) or the raw JSON, pretty-printed, with copy.
export function JsonView({ label, value }: { label: string; value: unknown }) {
  const v = parseJSON(value)
  const [raw, setRaw] = useState(false)
  const [copied, setCopied] = useState(false)
  const text = typeof v === 'string' ? v : JSON.stringify(v, null, 2)
  const structured = v !== null && typeof v === 'object'
  return (
    <section className="overflow-hidden rounded-xl border bg-card shadow-xs">
      <div className="flex items-center gap-2 border-b bg-muted/30 px-3 py-2">
        <Braces className="size-4 text-muted-foreground" />
        <h3 className="text-sm font-medium">{label}</h3>
        <div className="ml-auto flex items-center gap-1">
          {structured && (
            <div className="flex rounded-md border bg-background p-0.5 text-xs" role="tablist">
              {(['Fields', 'Raw'] as const).map((m) => (
                <button
                  key={m}
                  type="button"
                  role="tab"
                  aria-selected={(m === 'Raw') === raw}
                  onClick={() => setRaw(m === 'Raw')}
                  className={cn('rounded px-2 py-0.5', (m === 'Raw') === raw ? 'bg-accent font-medium text-foreground' : 'text-muted-foreground hover:text-foreground')}
                >
                  {m}
                </button>
              ))}
            </div>
          )}
          <button
            type="button"
            onClick={() => void navigator.clipboard?.writeText(text).then(() => (setCopied(true), setTimeout(() => setCopied(false), 1200)))}
            className="flex items-center gap-1 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
            aria-label={`Copy ${label}`}
          >
            {copied ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
            {copied ? 'Copied' : 'Copy'}
          </button>
        </div>
      </div>
      {raw || !structured ? (
        <pre className="overflow-x-auto p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">{text}</pre>
      ) : (
        <div>
          <Value v={v} depth={0} />
        </div>
      )}
    </section>
  )
}
