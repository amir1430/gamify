// List controls in the style of Coolify's resource lists: a live search, a filter menu of grouped
// checkboxes (OR within a group, AND across), a sort menu, a grid/table switch, removable chips for
// what is applied, and a pager with a remembered page size. Every choice lives in the URL so a
// filtered list is a link; the page size and the view live in localStorage (per viewer).
import { ArrowDownUp, CalendarRange, Check, ChevronLeft, ChevronRight, LayoutGrid, ListFilter, Rows3, Search, X } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { RANGES, applyList, joinParam, pageOf, rangeSince, splitParam, type FilterGroup, type ListSpec, type Sort } from '@/lib/list'
import { cn } from '@/lib/utils'
import { fromLocalInput } from '@/lib/views'

// ── stored preferences ──────────────────────────────────────────────────────────────────────────

function useStored<T extends string | number>(key: string, initial: T): [T, (v: T) => void] {
  const [v, setV] = useState<T>(() => {
    try {
      const s = localStorage.getItem(key)
      if (s != null) return (typeof initial === 'number' ? Number(s) || initial : s) as T
    } catch {
      /* storage blocked: the default */
    }
    return initial
  })
  return [
    v,
    (x) => {
      setV(x)
      try {
        localStorage.setItem(key, String(x))
      } catch {
        /* not remembered */
      }
    },
  ]
}

export type ViewMode = 'grid' | 'table'
export const useViewMode = (list: string, initial: ViewMode = 'grid') => useStored<ViewMode>(`gamify.view.${list}`, initial)

// ── URL state ───────────────────────────────────────────────────────────────────────────────────

// Several query parameters set in one navigation ('' / [] / null removes one); page resets.
export function useParams() {
  const [sp, setSp] = useSearchParams()
  const set = (vals: Record<string, string | string[] | null>, keepPage = false) =>
    setSp(
      (p) => {
        for (const [k, v] of Object.entries(vals)) {
          const s = Array.isArray(v) ? joinParam(v) : (v ?? '')
          s ? p.set(k, s) : p.delete(k)
        }
        if (!keepPage) p.delete('page')
        return p
      },
      { replace: true },
    )
  return { sp, set }
}

// A text param typed live: the input's text now, the param after `ms` of quiet.
export function useLiveParam(key: string, ms = 250): [text: string, setText: (s: string) => void, committed: string] {
  const { sp, set } = useParams()
  const committed = sp.get(key) ?? ''
  const [text, setText] = useState(committed)
  const last = useRef(committed)
  useEffect(() => {
    // The URL changed elsewhere (back, a chip, Clear): follow it.
    if (committed !== last.current) setText(committed)
    last.current = committed
  }, [committed])
  useEffect(() => {
    if (text.trim() === committed) return
    const t = setTimeout(() => ((last.current = text.trim()), set({ [key]: text.trim() || null })), ms)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [text])
  return [text, setText, committed]
}

// ── a list held whole in the browser ────────────────────────────────────────────────────────────

export const PAGE_SIZES = [12, 24, 48, 96]

// rows searched (?q=), filtered (?<group>=a,b), sorted (?sort=) and paged (?page=). id names the
// list for its remembered page size.
export function useClientList<T>(id: string, rows: T[] | undefined, spec: ListSpec<T>, defaultSize = 24) {
  const { sp, set } = useParams()
  const [text, setText, q] = useLiveParam('q')
  const [size, setSize] = useStored(`gamify.page-size.${id}`, defaultSize)
  const filters = Object.fromEntries((spec.groups ?? []).map((g) => [g.key, splitParam(sp.get(g.key) ?? '')]))
  const sort = sp.get('sort') ?? ''
  const fkey = JSON.stringify(filters)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const all = useMemo(() => applyList(rows ?? [], spec, { q, filters, sort }), [rows, q, fkey, sort])
  const page = pageOf(all, Number(sp.get('page') ?? 1), size)
  const active = (spec.groups ?? []).reduce((n, g) => n + filters[g.key].length, 0)
  return {
    text,
    setText,
    q,
    filters,
    sort,
    active,
    filtered: all,
    total: rows?.length ?? 0,
    page,
    size,
    setFilter: (key: string, values: string[]) => set({ [key]: values }),
    toggle: (key: string, value: string) => {
      const cur = filters[key] ?? []
      set({ [key]: cur.includes(value) ? cur.filter((v) => v !== value) : [...cur, value] })
    },
    setSort: (s: string) => set({ sort: s === spec.sorts?.[0]?.value ? null : s }, true),
    setPage: (p: number) => set({ page: p > 1 ? String(p) : null }, true),
    setSize: (n: number) => (setSize(n), set({}, false)),
    clear: () => (setText(''), set({ q: null, ...Object.fromEntries((spec.groups ?? []).map((g) => [g.key, null])) })),
    filtering: !!q || active > 0,
  }
}

// ── controls ────────────────────────────────────────────────────────────────────────────────────

export function SearchBox({ value, onChange, placeholder, className }: { value: string; onChange: (s: string) => void; placeholder: string; className?: string }) {
  return (
    <div className={cn('relative w-full sm:max-w-xs', className)}>
      <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        className="pr-8 pl-8 [&::-webkit-search-cancel-button]:hidden"
        type="search"
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => e.key === 'Escape' && value && (e.stopPropagation(), onChange(''))}
        aria-label="Search"
      />
      {value && (
        <button
          type="button"
          aria-label="Clear search"
          onClick={() => onChange('')}
          className="absolute top-1/2 right-2 grid size-5 -translate-y-1/2 place-items-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <X className="size-3.5" />
        </button>
      )}
    </div>
  )
}

export type MenuGroup = { key: string; label: string; options: { value: string; label: string }[]; single?: boolean }

// "Filter" with a count badge: per group, checkboxes (or radios for single), and Clear filters.
export function FilterMenu({
  groups,
  values,
  onChange,
  onClear,
}: {
  groups: MenuGroup[]
  values: Record<string, string[]>
  onChange: (key: string, values: string[]) => void
  onClear: () => void
}) {
  const n = groups.reduce((s, g) => s + (values[g.key]?.length ?? 0), 0)
  const shown = groups.filter((g) => g.options.length > 0)
  if (!shown.length) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" className={cn(n > 0 && 'border-brand/40')}>
          <ListFilter /> Filter
          {n > 0 && <span className="grid h-4.5 min-w-4.5 place-items-center rounded-full bg-brand px-1 text-[10px] font-semibold text-white tabular-nums">{n}</span>}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-[min(70vh,480px)] w-60 overflow-y-auto">
        {shown.map((g, i) => {
          const cur = values[g.key] ?? []
          return (
            <div key={g.key}>
              {i > 0 && <DropdownMenuSeparator />}
              <DropdownMenuLabel className="text-xs text-muted-foreground">{g.label}</DropdownMenuLabel>
              {g.single ? (
                <DropdownMenuRadioGroup value={cur[0] ?? ''} onValueChange={(v) => onChange(g.key, v && v !== cur[0] ? [v] : [])}>
                  {g.options.map((o) => (
                    <DropdownMenuRadioItem key={o.value} value={o.value} onSelect={(e) => e.preventDefault()}>
                      {o.label}
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
              ) : (
                g.options.map((o) => (
                  <DropdownMenuCheckboxItem
                    key={o.value}
                    checked={cur.includes(o.value)}
                    onSelect={(e) => e.preventDefault()}
                    onCheckedChange={(c) => onChange(g.key, c ? [...cur, o.value] : cur.filter((v) => v !== o.value))}
                  >
                    {o.label}
                  </DropdownMenuCheckboxItem>
                ))
              )}
            </div>
          )
        })}
        {n > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={onClear} className="text-muted-foreground">
              <X /> Clear filters
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function SortMenu<T>({ sorts, value, onChange }: { sorts: Sort<T>[]; value: string; onChange: (s: string) => void }) {
  const cur = sorts.find((s) => s.value === value) ?? sorts[0]
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline">
          <ArrowDownUp /> <span className="hidden sm:inline">{cur?.label}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-52">
        <DropdownMenuLabel className="text-xs text-muted-foreground">Sort by</DropdownMenuLabel>
        {sorts.map((s) => (
          <DropdownMenuItem key={s.value} onSelect={() => onChange(s.value)}>
            <Check className={cn(s.value !== cur?.value && 'invisible')} /> {s.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function ViewToggle({ value, onChange }: { value: ViewMode; onChange: (v: ViewMode) => void }) {
  const opt = (v: ViewMode, Icon: typeof LayoutGrid, label: string) => (
    <button
      type="button"
      aria-label={label}
      aria-pressed={value === v}
      title={label}
      onClick={() => onChange(v)}
      className={cn('grid size-7 place-items-center rounded-md transition-colors', value === v ? 'bg-background text-foreground shadow-xs' : 'text-muted-foreground hover:text-foreground')}
    >
      <Icon className="size-4" />
    </button>
  )
  return (
    <div className="flex h-9 items-center gap-0.5 rounded-lg bg-muted p-1">
      {opt('grid', LayoutGrid, 'Grid view')}
      {opt('table', Rows3, 'Table view')}
    </div>
  )
}

// What is applied, as chips that remove themselves, and Clear all.
export function Chips({ items, onClear }: { items: { key: string; label: ReactNode; onRemove: () => void }[]; onClear?: () => void }) {
  if (!items.length) return null
  return (
    <div className="-mt-2 mb-4 flex flex-wrap items-center gap-1.5">
      {items.map((c) => (
        <span key={c.key} className="inline-flex h-6 items-center gap-1 rounded-full border bg-card pr-1 pl-2.5 text-xs">
          {c.label}
          <button type="button" aria-label="Remove filter" onClick={c.onRemove} className="grid size-4 place-items-center rounded-full text-muted-foreground hover:bg-accent hover:text-foreground">
            <X className="size-3" />
          </button>
        </span>
      ))}
      {onClear && items.length > 1 && (
        <button type="button" onClick={onClear} className="px-1.5 text-xs text-muted-foreground hover:text-foreground">
          Clear all
        </button>
      )}
    </div>
  )
}

// Chips for a client list's search and filter groups.
export function listChips<T>(list: ReturnType<typeof useClientList<T>>, groups: FilterGroup<T>[]) {
  const out: { key: string; label: ReactNode; onRemove: () => void }[] = []
  if (list.q) out.push({ key: 'q', label: <>“{list.q}”</>, onRemove: () => list.setText('') })
  for (const g of groups)
    for (const v of list.filters[g.key] ?? [])
      out.push({
        key: `${g.key}=${v}`,
        label: (
          <>
            <span className="text-muted-foreground">{g.label}:</span> {g.options.find((o) => o.value === v)?.label ?? v}
          </>
        ),
        onRemove: () => list.toggle(g.key, v),
      })
  return out
}

// "1–24 of 40 games", page size, previous / next.
export function Pager({
  page,
  total,
  noun,
  size,
  onPage,
  onSize,
}: {
  page: { page: number; pages: number; from: number; to: number }
  total: number
  noun: [one: string, many: string]
  size: number
  onPage: (p: number) => void
  onSize: (n: number) => void
}) {
  if (!total) return null
  return (
    <div className="mt-3 flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
      <span className="tabular-nums">
        {page.from}–{page.to} of {total.toLocaleString()} {total === 1 ? noun[0] : noun[1]}
      </span>
      <div className="flex items-center gap-2">
        <span className="hidden sm:inline">Per page</span>
        <Select value={String(size)} onValueChange={(v) => onSize(Number(v))}>
          <SelectTrigger size="sm" className="h-7 w-18" aria-label="Per page">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {PAGE_SIZES.map((n) => (
              <SelectItem key={n} value={String(n)}>
                {n}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button variant="outline" size="icon" className="size-7" disabled={page.page <= 1} onClick={() => onPage(page.page - 1)} aria-label="Previous page">
          <ChevronLeft />
        </Button>
        <span className="tabular-nums">
          {page.page} / {page.pages}
        </span>
        <Button variant="outline" size="icon" className="size-7" disabled={page.page >= page.pages} onClick={() => onPage(page.page + 1)} aria-label="Next page">
          <ChevronRight />
        </Button>
      </div>
    </div>
  )
}

// Nothing matches what is applied.
export function NoMatch({ noun, onClear }: { noun: string; onClear: () => void }) {
  return (
    <div className="grid place-items-center gap-2 rounded-xl border border-dashed py-14 text-center">
      <Search className="size-5 text-muted-foreground/60" />
      <p className="text-sm font-medium">No matching {noun}</p>
      <p className="text-xs text-muted-foreground">Try a different search or filter.</p>
      <Button variant="outline" size="sm" className="mt-1" onClick={onClear}>
        Clear search and filters
      </Button>
    </div>
  )
}

// A cell value that filters the list by itself on click (Coolify's table cells).
export function FilterBy({ on, title, children }: { on: () => void; title?: string; children: ReactNode }) {
  return (
    <button
      type="button"
      title={title ?? 'Filter by this'}
      onClick={(e) => (e.stopPropagation(), on())}
      className="rounded-full outline-none hover:opacity-80 focus-visible:ring-2 focus-visible:ring-ring"
    >
      {children}
    </button>
  )
}

// ── time ranges (server lists with ?since= / ?until=) ───────────────────────────────────────────

// ?range=<preset> or ?range=custom with ?from= / ?to= (datetime-local values) as the API's since and
// until. A preset is resolved when picked, so the query stays put until the range changes.
export function useRange() {
  const { sp, set } = useParams()
  const range = sp.get('range') ?? ''
  const from = sp.get('from') ?? ''
  const to = sp.get('to') ?? ''
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const since = useMemo(() => (range === 'custom' ? fromLocalInput(from) : rangeSince(range)), [range, from])
  const until = range === 'custom' ? fromLocalInput(to) : ''
  return {
    range,
    from,
    to,
    since,
    until,
    set: (r: string) => set({ range: r || null, from: r === 'custom' ? from : null, to: r === 'custom' ? to : null }),
    setCustom: (k: 'from' | 'to', v: string) => set({ [k]: v || null }),
    label: range === 'custom' ? [from && `from ${new Date(from).toLocaleString()}`, to && `until ${new Date(to).toLocaleString()}`].filter(Boolean).join(' ') || 'Custom' : (RANGES.find((r) => r.value === range)?.label ?? ''),
  }
}

export function RangeFilter({ r }: { r: ReturnType<typeof useRange> }) {
  return (
    <>
      <Select value={r.range || 'any'} onValueChange={(v) => r.set(v === 'any' ? '' : v)}>
        <SelectTrigger className="w-40" aria-label="Time range">
          <CalendarRange className="size-4" />
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="any">Any time</SelectItem>
          {RANGES.map((x) => (
            <SelectItem key={x.value} value={x.value}>
              {x.label}
            </SelectItem>
          ))}
          <SelectItem value="custom">Custom range…</SelectItem>
        </SelectContent>
      </Select>
      {r.range === 'custom' && (
        <div className="flex items-center gap-1.5">
          <Input type="datetime-local" className="w-auto" value={r.from} onChange={(e) => r.setCustom('from', e.target.value)} aria-label="From" />
          <span className="text-xs text-muted-foreground">to</span>
          <Input type="datetime-local" className="w-auto" value={r.to} onChange={(e) => r.setCustom('to', e.target.value)} aria-label="To" />
        </div>
      )}
    </>
  )
}
