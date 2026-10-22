import { useGetIdentity, useInfiniteList, useInvalidate, usePermissions, useUpdate, type BaseKey, type NotificationProvider } from '@refinedev/core'
import { Check, ChevronRight, Copy, Inbox } from 'lucide-react'
import { useCallback, useState, type ReactNode } from 'react'
import { Link, useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { format } from '@/lib/money'
import { cn } from '@/lib/utils'
import type { Currency, Me } from './providers/auth'

// ── state in the URL, roles, changes ────────────────────────────────────────────────────────────

// /api/me (undefined while loading).
export const useMe = () => useGetIdentity<Me>().data ?? undefined

// Whether the admin holds a permission (view, adjust, a brand's own…), from /api/me.
export const usePerm = (perm: string) => !!usePermissions<string[]>({}).data?.includes(perm)

// A query parameter as page state, so every view is a link an admin can share. '' / null removes it.
export function useParam(key: string): [string, (v: string | null) => void] {
  const [sp, setSp] = useSearchParams()
  const set = (v: string | null) =>
    setSp(
      (p) => {
        v ? p.set(key, v) : p.delete(key)
        return p
      },
      { replace: true },
    )
  return [sp.get(key) ?? '', set]
}

// ?cur=<code>, the first currency (the ledger's default) otherwise.
export function useCur(): [string, (c: string) => void] {
  const first = useMe()?.currencies[0]?.code ?? ''
  const [v, set] = useParam('cur')
  return [v || first, (c) => set(c === first ? null : c)]
}

// Update one record (meta.action: a sub-path, meta.method: other than PUT); onDone after success.
// Errors show as toasts (the notification provider).
export function useChange(resource: string, id: BaseKey, onDone: () => void) {
  const { mutate, mutation } = useUpdate()
  return {
    busy: mutation.isPending,
    run: (values: object, meta?: { action?: string; method?: string }) => mutate({ resource, id, values, meta }, { onSuccess: onDone }),
  }
}

// A write outside Refine's CRUD (approve, disconnect, a limit): busy while it runs, a toast with the
// API's message on failure; after success every loaded list and record is refetched.
// ok false: it failed (already toasted).
export function useAction() {
  const invalidate = useInvalidate()
  const [busy, setBusy] = useState(false)
  const run = useCallback(
    async <T = unknown,>(path: string, init: Parameters<typeof api>[1], failed = 'Not saved'): Promise<{ ok: boolean; data?: T }> => {
      setBusy(true)
      try {
        const data = await api<T>(path, init)
        await invalidate({ invalidates: ['all'] })
        return { ok: true, data }
      } catch (e) {
        toast.error(failed, { description: (e as Error).message })
        return { ok: false }
      } finally {
        setBusy(false)
      }
    },
    [invalidate],
  )
  return { busy, run }
}

// Refine's notifications as sonner toasts.
export const notificationProvider: NotificationProvider = {
  open: ({ type, message, description, key }) =>
    void (type === 'error' ? toast.error(message, { id: key, description }) : toast.success(message, { id: key, description })),
  close: (key) => void toast.dismiss(key),
}

// ── page structure ───────────────────────────────────────────────────────────────────────────────

export type Crumb = { label: string; to?: string }

export function PageHeader({
  title,
  crumbs,
  description,
  meta,
  actions,
}: {
  title: ReactNode
  crumbs?: Crumb[]
  description?: ReactNode
  meta?: ReactNode
  actions?: ReactNode
}) {
  return (
    <header className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0 space-y-1.5">
        {crumbs && (
          <nav aria-label="Breadcrumb" className="flex items-center gap-1.5 text-xs text-muted-foreground">
            {crumbs.map((c, i) => (
              <span key={i} className="flex items-center gap-1.5">
                {i > 0 && <ChevronRight className="size-3" />}
                {c.to ? (
                  <Link to={c.to} className="hover:text-foreground">
                    {c.label}
                  </Link>
                ) : (
                  <span className="text-foreground/80">{c.label}</span>
                )}
              </span>
            ))}
          </nav>
        )}
        <div className="flex flex-wrap items-center gap-2.5">
          <h1 className="truncate text-xl font-semibold tracking-tight">{title}</h1>
          {meta}
        </div>
        {description && <p className="max-w-3xl text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </header>
  )
}

export function Section({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={cn('mt-8', className)}>
      <div className="mb-3 flex flex-wrap items-end justify-between gap-2">
        <div>
          <h2 className="text-sm font-semibold">{title}</h2>
          {description && <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>}
        </div>
        {actions}
      </div>
      {children}
    </section>
  )
}

export const Note = ({ children, className }: { children: ReactNode; className?: string }) => (
  <p className={cn('mt-3 text-xs leading-relaxed text-muted-foreground', className)}>{children}</p>
)

export function Toolbar({ children }: { children: ReactNode }) {
  return <div className="mb-4 flex flex-wrap items-center gap-2">{children}</div>
}

// ── lists ───────────────────────────────────────────────────────────────────────────────────────

export const PAGE = 50

// A keyset list: pages of PAGE appended by "Load more" (the API's next cursor). filters are plain
// query parameters ('' = unset); changing them starts over.
export function usePaged<T>(resource: string, filters: Record<string, string> = {}, opts: { enabled?: boolean; quiet?: boolean } = {}) {
  const { result, query } = useInfiniteList<T & { id: number }>({
    resource,
    filters: Object.entries(filters).map(([field, value]) => ({ field, operator: 'eq' as const, value })),
    pagination: { mode: 'server', pageSize: PAGE },
    queryOptions: { enabled: opts.enabled ?? true, retry: opts.quiet ? false : undefined },
    errorNotification: opts.quiet ? false : undefined,
  })
  const rows = result.data?.pages.flatMap((p) => p.data) as T[] | undefined
  const more = result.hasNextPage ? (
    <div className="flex justify-center pt-3">
      <Button variant="outline" size="sm" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>
        {query.isFetchingNextPage ? 'Loading…' : 'Load more'}
      </Button>
    </div>
  ) : null
  return { rows, loading: query.isLoading, error: query.error, more }
}

export const dateTime = (iso: string | null | undefined) => (iso ? new Date(iso).toLocaleString() : '—')
export const When = ({ at }: { at: string | null | undefined }) => <span className="whitespace-nowrap text-muted-foreground">{dateTime(at)}</span>

// ── numbers ─────────────────────────────────────────────────────────────────────────────────────

type Tone = 'ok' | 'danger' | 'warn' | 'brand' | 'neutral'
const TONE_TEXT: Record<Tone, string> = { ok: 'text-ok', danger: 'text-danger', warn: 'text-warn', brand: 'text-brand', neutral: '' }

export function Stats({ items }: { items: { label: string; value: ReactNode; hint?: ReactNode; tone?: Tone }[] }) {
  return (
    <div className="grid grid-cols-2 overflow-hidden rounded-lg border bg-card sm:grid-cols-3 lg:grid-cols-[repeat(auto-fit,minmax(150px,1fr))]">
      {items.map((s) => (
        <div key={s.label} className="-mb-px -mr-px border-r border-b px-4 py-3.5">
          <div className="text-xs text-muted-foreground">{s.label}</div>
          <div className={cn('num mt-1 truncate text-lg font-semibold tracking-tight', TONE_TEXT[s.tone ?? 'neutral'])}>{s.value}</div>
          {s.hint && <div className="mt-0.5 text-xs text-muted-foreground">{s.hint}</div>}
        </div>
      ))}
    </div>
  )
}

// Label / value pairs in a bordered grid.
export function Facts({ items, cols = 3 }: { items: [label: string, value: ReactNode][]; cols?: 1 | 2 | 3 }) {
  return (
    <dl
      className={cn(
        'grid overflow-hidden rounded-lg border bg-card',
        cols === 1 ? 'grid-cols-1' : cols === 2 ? 'sm:grid-cols-2' : 'sm:grid-cols-2 lg:grid-cols-3',
      )}
    >
      {items.map(([label, value]) => (
        <div key={label} className="-mb-px -mr-px flex min-w-0 flex-col gap-1 border-r border-b px-4 py-3">
          <dt className="text-xs text-muted-foreground">{label}</dt>
          <dd className="min-w-0 truncate text-sm">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

// Minor units in a currency's decimals (/api/me), with its code: money(1234, 'usd') → "12.34 USD".
export function useMoney() {
  const cur = useMe()?.currencies
  const decimals = (code: string) => cur?.find((c) => c.code === code)?.decimals ?? 0
  return {
    decimals,
    money: (amount: number | bigint, code: string, withCode = true) => format(amount, decimals(code)) + (withCode ? ` ${code.toUpperCase()}` : ''),
  }
}

// A signed amount, green when it adds.
export const Signed = ({ amount, children }: { amount: number; children: ReactNode }) => (
  <span className={cn('num', amount > 0 && 'text-ok', amount < 0 && 'text-danger')}>{children}</span>
)

export const Faint = ({ children = '—' }: { children?: ReactNode }) => <span className="text-muted-foreground/60">{children}</span>
export const Mono = ({ children }: { children: ReactNode }) => <span className="font-mono text-xs text-muted-foreground">{children}</span>
export const Sub = ({ children }: { children: ReactNode }) => <div className="mt-0.5 text-xs font-normal text-muted-foreground">{children}</div>

// ── status ──────────────────────────────────────────────────────────────────────────────────────

const PILL: Record<Tone, string> = {
  ok: 'bg-ok/10 text-ok ring-ok/20',
  danger: 'bg-danger/10 text-danger ring-danger/20',
  warn: 'bg-warn/10 text-warn ring-warn/20',
  brand: 'bg-brand/10 text-brand ring-brand/20',
  neutral: 'bg-secondary text-muted-foreground ring-border',
}

export function Pill({ tone = 'neutral', dot, children }: { tone?: Tone; dot?: boolean; children: ReactNode }) {
  return (
    <span className={cn('inline-flex h-5 items-center gap-1.5 rounded-full px-2 text-[11px] font-medium whitespace-nowrap ring-1 ring-inset', PILL[tone])}>
      {dot && <span className="size-1.5 rounded-full bg-current" />}
      {children}
    </span>
  )
}

export const OnOff = ({ on }: { on: boolean }) => (
  <Pill tone={on ? 'ok' : 'danger'} dot>
    {on ? 'On' : 'Off'}
  </Pill>
)

// ── tables ──────────────────────────────────────────────────────────────────────────────────────

export type Column<T> = { key: string; title?: ReactNode; align?: 'right'; className?: string; render: (row: T) => ReactNode }

export function DataTable<T>({
  rows,
  columns,
  rowKey,
  onRowClick,
  loading,
  empty = 'Nothing here yet.',
  className,
}: {
  rows: T[] | undefined
  columns: Column<T>[]
  rowKey: (row: T) => string | number
  onRowClick?: (row: T) => void
  loading?: boolean
  empty?: ReactNode
  className?: string
}) {
  const r = (c: Column<T>) => cn(c.align === 'right' && 'text-right', c.className)
  return (
    <div className={cn('overflow-hidden rounded-lg border bg-card', className)}>
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            {columns.map((c) => (
              <TableHead key={c.key} className={cn('h-9 bg-muted/40 text-xs font-medium text-muted-foreground', r(c))}>
                {c.title}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {loading && !rows?.length
            ? Array.from({ length: 4 }, (_, i) => (
                <TableRow key={i} className="hover:bg-transparent">
                  {columns.map((c) => (
                    <TableCell key={c.key} className="py-3.5">
                      <Skeleton className="h-4 w-full max-w-28" />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            : rows?.map((row) => (
                <TableRow
                  key={rowKey(row)}
                  onClick={onRowClick && (() => onRowClick(row))}
                  className={cn('hover:bg-accent/40', onRowClick && 'cursor-pointer')}
                >
                  {columns.map((c) => (
                    <TableCell key={c.key} className={cn('py-2.5', r(c))}>
                      {c.render(row)}
                    </TableCell>
                  ))}
                </TableRow>
              ))}
          {!loading && !rows?.length && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={columns.length} className="py-12 text-center text-sm text-muted-foreground">
                <Inbox className="mx-auto mb-2 size-5 opacity-50" />
                {empty}
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  )
}

// ── controls ────────────────────────────────────────────────────────────────────────────────────

export function Segmented<T extends string>({ value, onChange, options }: { value: T; onChange: (v: T) => void; options: { value: T; label: ReactNode }[] }) {
  return (
    <Tabs value={value} onValueChange={(v) => onChange(v as T)}>
      <TabsList>
        {options.map((o) => (
          <TabsTrigger key={o.value} value={o.value} className="px-3">
            {o.label}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  )
}

export const CurrencyTabs = ({ value, onChange, currencies }: { value: string; onChange: (c: string) => void; currencies: Currency[] }) => (
  <Segmented value={value} onChange={onChange} options={currencies.map((c) => ({ value: c.code, label: c.code.toUpperCase() }))} />
)

export type Option = { value: string; label: string; disabled?: boolean }
const NONE = '__none__' // Radix Select reserves '' for "no value"

// A select over plain options or groups of them; '' is a real option here (e.g. "Any status").
export function Choice({
  value,
  onChange,
  options,
  groups,
  placeholder,
  className,
  label,
}: {
  value: string
  onChange: (v: string) => void
  options?: Option[]
  groups?: { label: string; options: Option[] }[]
  placeholder?: string
  className?: string
  label?: string
}) {
  const item = (o: Option) => (
    <SelectItem key={o.value || NONE} value={o.value || NONE} disabled={o.disabled}>
      {o.label}
    </SelectItem>
  )
  const known = [...(options ?? []), ...(groups ?? []).flatMap((g) => g.options)].some((o) => o.value === value)
  return (
    <Select value={known ? value || NONE : ''} onValueChange={(v) => onChange(v === NONE ? '' : v)}>
      <SelectTrigger className={cn('min-w-40', className)} aria-label={label}>
        <SelectValue placeholder={placeholder} />
      </SelectTrigger>
      <SelectContent>
        {options?.map(item)}
        {groups?.map((g) => (
          <SelectGroup key={g.label}>
            <SelectLabel>{g.label}</SelectLabel>
            {g.options.map(item)}
          </SelectGroup>
        ))}
      </SelectContent>
    </Select>
  )
}

export function Field({
  label,
  hint,
  error,
  children,
  className,
}: {
  label: ReactNode
  hint?: ReactNode
  error?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <div className={cn('grid gap-1.5', className)}>
      <Label className="text-xs font-medium text-muted-foreground">{label}</Label>
      {children}
      {error ? <p className="text-xs text-danger">{error}</p> : hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

// ── changes ─────────────────────────────────────────────────────────────────────────────────────

// A dialog for a change: its fields, then the reason every change needs (stored in the audit log).
// Escape and the backdrop close it, unless it's saving.
export function ChangeDialog({
  title,
  description,
  okText = 'Save',
  danger,
  wide,
  busy,
  disabled,
  reason,
  onReason,
  onSubmit,
  onClose,
  children,
  footer,
}: {
  title: ReactNode
  description?: ReactNode
  okText?: string
  danger?: boolean
  wide?: boolean
  busy: boolean
  disabled?: boolean
  reason?: string // with onReason: the reason field is shown and required
  onReason?: (v: string) => void
  onSubmit?: () => void
  onClose: () => void
  children?: ReactNode
  footer?: ReactNode // replaces Cancel / ok
}) {
  const blocked = busy || !!disabled || (!!onReason && !reason?.trim())
  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent className={cn('max-h-[90vh] overflow-y-auto', wide ? 'sm:max-w-4xl' : 'sm:max-w-lg')} showCloseButton={!busy}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            if (!blocked) onSubmit?.()
          }}
        >
          {children}
          {onReason && (
            <Field label="Reason" hint="Goes to the audit log.">
              <Textarea
                value={reason}
                maxLength={500}
                placeholder="Why this change?"
                onChange={(e) => onReason(e.target.value)}
                autoFocus={!children}
                rows={2}
              />
            </Field>
          )}
          <DialogFooter className="pt-2">
            {footer ?? (
              <>
                <Button type="button" variant="ghost" onClick={onClose} disabled={busy}>
                  Cancel
                </Button>
                <Button type="submit" variant={danger ? 'destructive' : 'default'} disabled={blocked}>
                  {busy ? 'Saving…' : okText}
                </Button>
              </>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ── detail pages ────────────────────────────────────────────────────────────────────────────────

// Section tabs under a detail page's header, kept in ?tab= (the first tab is the default).
export function useTab<T extends string>(tabs: readonly T[]): [T, (t: T) => void] {
  const [v, set] = useParam('tab')
  const tab = (tabs as readonly string[]).includes(v) ? (v as T) : tabs[0]
  return [tab, (t) => set(t === tabs[0] ? null : t)]
}

export function TabBar<T extends string>({
  value,
  onChange,
  tabs,
}: {
  value: T
  onChange: (t: T) => void
  tabs: { value: T; label: string; count?: number }[]
}) {
  return (
    <div className="mb-6 flex gap-1 overflow-x-auto border-b" role="tablist">
      {tabs.map((t) => (
        <button
          key={t.value}
          role="tab"
          aria-selected={t.value === value}
          onClick={() => onChange(t.value)}
          className={cn(
            '-mb-px flex shrink-0 items-center gap-2 border-b-2 px-3 pb-2.5 text-sm transition-colors',
            t.value === value ? 'border-foreground text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground',
          )}
        >
          {t.label}
          {t.count != null && <span className="rounded-full bg-secondary px-1.5 text-[11px] text-muted-foreground tabular-nums">{t.count}</span>}
        </button>
      ))}
    </div>
  )
}

// An ID with a copy button.
export function Copyable({ value, label }: { value: string | number; label?: string }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      title="Copy"
      className="group inline-flex items-center gap-1.5 font-mono text-xs text-muted-foreground hover:text-foreground"
      onClick={(e) => {
        e.stopPropagation()
        void navigator.clipboard?.writeText(String(value)).then(() => (setDone(true), setTimeout(() => setDone(false), 1200)))
      }}
    >
      {label ?? value}
      {done ? <Check className="size-3 text-ok" /> : <Copy className="size-3 opacity-0 group-hover:opacity-100" />}
    </button>
  )
}

// Destructive actions, set apart at the bottom of a page.
export function DangerZone({ items }: { items: { title: string; description: ReactNode; action: ReactNode }[] }) {
  return (
    <Section title="Danger zone">
      <div className="divide-y divide-danger/20 rounded-lg border border-danger/30">
        {items.map((i) => (
          <div key={i.title} className="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <div className="text-sm font-medium">{i.title}</div>
              <div className="mt-0.5 text-xs text-muted-foreground">{i.description}</div>
            </div>
            <div className="shrink-0">{i.action}</div>
          </div>
        ))}
      </div>
    </Section>
  )
}
