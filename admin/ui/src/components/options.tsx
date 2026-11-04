import { Check, Search, X } from 'lucide-react'
import { useState, type KeyboardEvent } from 'react'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import type { ViewOption } from '@/lib/views'

const SEARCH_FROM = 7 // fewer options than this fit at a glance

function Chips({ items, onRemove, name = (v) => v }: { items: string[]; onRemove: (v: string) => void; name?: (v: string) => string }) {
  return items.map((v, i) => (
    <span key={`${v}.${i}`} className="inline-flex max-w-full items-center gap-1 rounded-md bg-brand/15 py-0.5 pr-1 pl-2 text-xs">
      <span className="truncate">{name(v)}</span>
      <button type="button" aria-label={`Remove ${name(v)}`} className="rounded-sm p-0.5 hover:bg-foreground/10" onClick={() => onRemove(v)}>
        <X className="size-3" />
      </button>
    </span>
  ))
}

// Pick one or several of a list: a search box once it's long, what's picked above it (so a pick
// the filter hides, or one that's no longer an option, can still be seen and removed).
export function PickList({
  options,
  value,
  multi,
  onChange,
  label,
}: {
  options: ViewOption[]
  value: string[]
  multi?: boolean
  onChange: (v: string[]) => void
  label: string
}) {
  const [q, setQ] = useState('')
  const needle = q.trim().toLowerCase()
  const shown = needle ? options.filter((o) => `${o.value} ${o.label ?? ''} ${o.hint ?? ''}`.toLowerCase().includes(needle)) : options
  const name = (v: string) => options.find((o) => o.value === v)?.label || v
  const toggle = (v: string) => {
    if (value.includes(v)) onChange(value.filter((x) => x !== v))
    else onChange(multi ? [...value, v] : [v])
  }
  if (options.length === 0) return <p className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">Nothing to pick here.</p>
  return (
    <div className="grid gap-2">
      {multi && value.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <Chips items={value} onRemove={toggle} name={name} />
          <button type="button" className="ml-1 text-xs text-muted-foreground hover:text-foreground hover:underline" onClick={() => onChange([])}>
            Clear
          </button>
        </div>
      )}
      <div className="overflow-hidden rounded-md border">
        {options.length >= SEARCH_FROM && (
          <div className="relative border-b">
            <Search className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              aria-label={`Search ${label}`}
              placeholder="Search…"
              className="h-9 rounded-none border-0 pl-9 shadow-none focus-visible:ring-0"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => {
                if (e.key !== 'Enter') return
                e.preventDefault() // not the form's submit: Enter takes the only match
                const open = shown.filter((o) => !o.disabled)
                if (open.length === 1) {
                  toggle(open[0].value)
                  setQ('')
                }
              }}
            />
          </div>
        )}
        <div role={multi ? 'group' : 'radiogroup'} aria-label={label} className={cn('grid max-h-64 overflow-y-auto p-1', multi && shown.length > 4 && 'sm:grid-cols-2')}>
          {shown.map((o) => {
            const on = value.includes(o.value)
            return (
              <button
                key={o.value}
                type="button"
                role={multi ? 'checkbox' : 'radio'}
                aria-checked={on}
                disabled={o.disabled}
                onClick={() => toggle(o.value)}
                className={cn('flex min-w-0 items-start gap-2 rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent disabled:pointer-events-none disabled:opacity-50', on && 'bg-brand/10')}
              >
                <span
                  className={cn(
                    'mt-0.5 flex size-4 shrink-0 items-center justify-center border',
                    multi ? 'rounded-[4px]' : 'rounded-full',
                    on && 'border-brand bg-brand text-primary-foreground',
                  )}
                >
                  {on && <Check className="size-3" />}
                </span>
                <span className="min-w-0">
                  <span className="block break-words">{o.label || o.value}</span>
                  {o.hint && <span className="block text-xs break-words text-muted-foreground">{o.hint}</span>}
                </span>
              </button>
            )
          })}
          {shown.length === 0 && <p className="col-span-full p-2 text-sm text-muted-foreground">No match.</p>}
        </div>
      </div>
      {!multi && value[0] && <p className="text-xs text-muted-foreground">Picked: {name(value[0])}. Click it again to clear.</p>}
    </div>
  )
}

// A free list of strings: Enter, a comma or leaving the box adds what's typed; Backspace on an
// empty box removes the last. Repeats are ignored.
export function TagInput({ value, onChange, placeholder, label }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string; label: string }) {
  const [text, setText] = useState('')
  const add = (raw: string) => {
    const next = [...value]
    for (const part of raw.split(',')) {
      const s = part.trim()
      if (s && !next.includes(s)) next.push(s)
    }
    if (next.length !== value.length) onChange(next)
    setText('')
  }
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault()
      add(text)
    } else if (e.key === 'Backspace' && text === '' && value.length > 0) {
      onChange(value.slice(0, -1))
    }
  }
  return (
    <div className="flex min-h-9 flex-wrap items-center gap-1.5 rounded-md border bg-transparent px-2 py-1.5 focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50">
      <Chips items={value} onRemove={(v) => onChange(value.filter((x) => x !== v))} />
      <input
        aria-label={label}
        className="min-w-24 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
        placeholder={value.length ? undefined : placeholder}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        onBlur={() => add(text)}
        onPaste={(e) => {
          const t = e.clipboardData.getData('text')
          if (!t.includes(',')) return
          e.preventDefault()
          add(text + t)
        }}
      />
    </div>
  )
}
