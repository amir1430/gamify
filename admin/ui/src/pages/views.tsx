import { useCustom, useList } from '@refinedev/core'
import { ExternalLink, MoreHorizontal } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { JsonSummary, JsonView } from '@/components/json'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import {
  cellText,
  filterValue,
  initialValue,
  inputValue,
  labelOf,
  linkText,
  toLocalInput,
  webURL,
  type ViewAction,
  type ViewColumn,
  type ViewDesc,
  type ViewField,
  type ViewFilter,
  type ViewRow,
} from '@/lib/views'
import { ChangeDialog, Choice, DataTable, Facts, Faint, Field, Note, PageHeader, Pill, Toolbar, useAction, useMe, useMoney, usePaged, useParam } from '../ui'

// The brand's views this admin may open (GET /api/views; the API already left out the others).
export function useViews(): ViewDesc[] | undefined {
  const { result, query } = useList<ViewDesc>({ resource: 'views', pagination: { mode: 'off' }, errorNotification: false })
  return query.isSuccess ? result.data : undefined
}

// ── cells ───────────────────────────────────────────────────────────────────────────────────────

// full: the detail dialog's look (whole text and link, a large image) in place of the table's.
function Cell({ col, row, full = false }: { col: ViewColumn; row: ViewRow; full?: boolean }) {
  const { decimals } = useMoney()
  const v = row[col.key]
  if (v == null || v === '') return <Faint />
  switch (col.type) {
    case 'url': {
      const href = webURL(v)
      if (!href) return <span className={full ? 'break-all' : 'block max-w-80 truncate'}>{String(v)}</span>
      return (
        <a
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          title={href}
          onClick={(e) => e.stopPropagation()}
          className="inline-flex max-w-full items-center gap-1 text-brand hover:underline"
        >
          <span className={full ? 'break-all' : 'max-w-72 truncate'}>{linkText(href)}</span>
          <ExternalLink className="size-3.5 shrink-0" />
        </a>
      )
    }
    case 'image': {
      const src = webURL(v)
      if (!src) return <Faint />
      return full ? (
        <a href={src} target="_blank" rel="noopener noreferrer" title="Open full size">
          <img src={src} alt="" loading="lazy" className="max-h-56 max-w-full rounded-lg border object-contain" />
        </a>
      ) : (
        <img src={src} alt="" loading="lazy" className="size-9 rounded-md border object-cover" />
      )
    }
    case 'bool':
      return <Pill tone={v ? 'ok' : 'neutral'}>{v ? 'Yes' : 'No'}</Pill>
    case 'enum':
      return <Pill>{String(v)}</Pill>
    case 'user_id':
      return (
        <Link to={`/players/${v}`} onClick={(e) => e.stopPropagation()} className="text-brand hover:underline">
          #{String(v)}
        </Link>
      )
    case 'time':
      return <span className="whitespace-nowrap text-muted-foreground">{cellText(col, v)}</span>
    case 'json':
      return <JsonSummary value={v} />
    case 'money': {
      const code = String((col.currency_key ? row[col.currency_key] : col.currency) ?? '')
      return (
        <span className="num">
          {cellText(col, v, decimals(code))} {code && <span className="text-xs text-muted-foreground">{code.toUpperCase()}</span>}
        </span>
      )
    }
    case 'int':
    case 'decimal':
      return <span className="num">{cellText(col, v)}</span>
  }
  return full ? <span className="whitespace-pre-wrap">{cellText(col, v)}</span> : <span className="block max-w-80 truncate">{cellText(col, v)}</span>
}

const numeric = (c: ViewColumn) => c.type === 'int' || c.type === 'decimal' || c.type === 'money'

// ── a view's table ──────────────────────────────────────────────────────────────────────────────

// A view's rows with filters (query parameters, '' = unset), "Load more", a detail dialog on row
// click and the actions. hide: column keys not shown (a player tab hides its player column).
export function ViewTable({ view, filters, hide = [] }: { view: ViewDesc; filters: Record<string, string>; hide?: string[] }) {
  const { rows, loading, error, more } = usePaged<ViewRow>(`views/${view.name}`, filters)
  const [open, setOpen] = useState<ViewRow | null>(null)
  const [acting, setActing] = useState<{ action: ViewAction; row: ViewRow } | null>(null)
  const cols = view.columns.filter((c) => !c.detail_only && !hide.includes(c.key))
  return (
    <>
      {error && <Note className="text-danger">{error.message}</Note>}
      <DataTable
        rows={rows}
        loading={loading}
        rowKey={(r) => String(r.id)}
        onRowClick={(r) => setOpen(r)}
        empty="Nothing here."
        columns={[
          ...cols.map((c) => ({
            key: c.key,
            title: labelOf(c),
            align: numeric(c) ? ('right' as const) : undefined,
            render: (r: ViewRow) => <Cell col={c} row={r} />,
          })),
          ...(view.actions.length
            ? [
                {
                  key: '__actions',
                  align: 'right' as const,
                  render: (r: ViewRow) => <ActionsMenu actions={view.actions} onPick={(action) => setActing({ action, row: r })} />,
                },
              ]
            : []),
        ]}
      />
      {more}
      {open && (
        <RowDialog
          view={view}
          row={open}
          onClose={() => setOpen(null)}
          onAction={(action, row) => {
            setOpen(null)
            setActing({ action, row })
          }}
        />
      )}
      {acting && <ActionDialog view={view} action={acting.action} row={acting.row} onClose={() => setActing(null)} />}
    </>
  )
}

function ActionsMenu({ actions, onPick }: { actions: ViewAction[]; onPick: (a: ViewAction) => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" aria-label="Actions" onClick={(e) => e.stopPropagation()}>
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
        {actions.map((a) => (
          <DropdownMenuItem key={a.name} onSelect={() => onPick(a)} className={a.confirm ? 'text-danger' : undefined}>
            {a.title}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// Every column of one row: the fresh record (GET) when the view has a detail page, else the list's.
function RowDialog({ view, row, onClose, onAction }: { view: ViewDesc; row: ViewRow; onClose: () => void; onAction: (a: ViewAction, r: ViewRow) => void }) {
  const { query } = useCustom<ViewRow>({
    url: `/api/views/${view.name}/${encodeURIComponent(String(row.id))}`,
    method: 'get',
    queryOptions: { enabled: view.has_detail },
    errorNotification: false,
  })
  const r = (view.has_detail && query.data?.data) || row
  return (
    <ChangeDialog
      title={`${view.title} · #${String(row.id)}`}
      wide
      busy={false}
      onClose={onClose}
      footer={
        <>
          {view.actions.map((a) => (
            <Button key={a.name} type="button" variant={a.confirm ? 'destructive' : 'outline'} onClick={() => onAction(a, r)}>
              {a.title}
            </Button>
          ))}
          <Button type="button" variant="ghost" onClick={onClose}>
            Close
          </Button>
        </>
      }
    >
      {view.has_detail && query.isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : (
        <>
          {query.isError && <Note className="text-danger">{query.error.message}</Note>}
          {view.columns
            .filter((c) => c.type === 'image' && webURL(r[c.key]))
            .map((c) => (
              <div key={c.key}>
                <Cell col={c} row={r} full />
              </div>
            ))}
          <Facts
            cols={2}
            items={view.columns.filter((c) => c.type !== 'json' && c.type !== 'image').map((c) => [labelOf(c), <Cell col={c} row={r} full />] as [string, ReactNode])}
          />
          {view.columns
            .filter((c) => c.type === 'json')
            .map((c) => (r[c.key] == null || r[c.key] === '' ? null : <JsonView key={c.key} label={labelOf(c)} value={r[c.key]} />))}
        </>
      )}
    </ChangeDialog>
  )
}

// ── actions ─────────────────────────────────────────────────────────────────────────────────────

// An action's form: its fields (prefilled from the row where it has the same key), the reason when
// the action needs one, its confirmation text; POST …/actions/<name>, then every list refetches.
export function ActionDialog({ view, action, row, onClose }: { view: ViewDesc; action: ViewAction; row: ViewRow; onClose: () => void }) {
  const { decimals } = useMoney()
  const dec = (f: ViewField) => (f.type === 'money' && f.currency ? decimals(f.currency) : 0)
  const [values, setValues] = useState<Record<string, string | boolean>>(() =>
    Object.fromEntries(action.fields.map((f) => [f.key, initialValue(f, row, dec(f))])),
  )
  const [reason, setReason] = useState('')
  const [tried, setTried] = useState(false)
  const { busy, run } = useAction()
  const parsed = action.fields.map((f) => [f, inputValue(f, values[f.key] ?? '', dec(f))] as const)
  const invalid = parsed.some(([, p]) => p.error)
  const set = (k: string, v: string | boolean) => setValues((x) => ({ ...x, [k]: v }))

  return (
    <ChangeDialog
      title={`${action.title} · #${String(row.id)}`}
      description={action.confirm || undefined}
      okText={action.title}
      danger={!!action.confirm}
      busy={busy}
      reason={action.reason ? reason : undefined}
      onReason={action.reason ? setReason : undefined}
      onClose={onClose}
      onSubmit={async () => {
        setTried(true)
        if (invalid) return
        const input: Record<string, unknown> = {}
        for (const [f, p] of parsed) if (p.value !== undefined) input[f.key] = p.value
        const r = await run(`/api/views/${view.name}/${encodeURIComponent(String(row.id))}/actions/${action.name}`, {
          method: 'POST',
          body: { reason: reason.trim(), input },
        })
        if (!r.ok) return
        toast.success(`${action.title}: done`)
        onClose()
      }}
    >
      {parsed.map(([f, p]) => (
        <FieldInput key={f.key} field={f} value={values[f.key] ?? ''} error={tried ? p.error : undefined} onChange={(v) => set(f.key, v)} />
      ))}
    </ChangeDialog>
  )
}

function FieldInput({ field: f, value, error, onChange }: { field: ViewField; value: string | boolean; error?: string; onChange: (v: string | boolean) => void }) {
  const label = labelOf(f) + (f.required ? '' : ' (optional)')
  if (f.type === 'bool')
    return (
      <label className="flex items-center justify-between gap-4 rounded-md border px-3 py-2.5 text-sm">
        {labelOf(f)}
        <Switch checked={value === true} onCheckedChange={(v) => onChange(v)} />
      </label>
    )
  const s = String(value)
  const hint =
    f.type === 'money' ? (f.currency ? f.currency.toUpperCase() : 'In minor units.') : f.type === 'time' ? 'Your local time.' : f.type === 'json' ? 'JSON.' : undefined
  return (
    <Field label={label} error={error} hint={hint}>
      {f.type === 'enum' ? (
        <Choice
          className="w-full"
          value={s}
          onChange={onChange}
          options={[...(f.required ? [] : [{ value: '', label: '—' }]), ...(f.options ?? []).map((o) => ({ value: o, label: o }))]}
          placeholder="Pick one"
        />
      ) : f.type === 'json' ? (
        <Textarea className="font-mono text-xs" rows={4} value={s} onChange={(e) => onChange(e.target.value)} />
      ) : (
        <Input
          type={f.type === 'time' ? 'datetime-local' : f.type === 'url' || f.type === 'image' ? 'url' : 'text'}
          placeholder={f.type === 'url' || f.type === 'image' ? 'https://' : undefined}
          inputMode={f.type === 'int' || f.type === 'user_id' ? 'numeric' : f.type === 'money' || f.type === 'decimal' ? 'decimal' : undefined}
          className={f.type === 'text' ? undefined : 'font-mono'}
          value={s}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </Field>
  )
}

// ── filters ─────────────────────────────────────────────────────────────────────────────────────

// One filter in the URL (?<key>=), so a filtered view is a link. Text-like ones apply on Enter.
function FilterInput({ filter: f, column }: { filter: ViewFilter; column?: ViewColumn }) {
  const [v, set] = useParam(f.key)
  const [text, setText] = useState(f.type === 'time' && v ? toLocalInput(v) : v)
  const [error, setError] = useState('')
  const label = labelOf(f)
  if (f.type === 'enum' || f.type === 'bool') {
    const options = f.type === 'bool' ? ['true', 'false'] : (f.options?.length ? f.options : (column?.options ?? []))
    return (
      <Choice
        label={label}
        value={v}
        onChange={(x) => set(x || null)}
        options={[{ value: '', label: `Any ${label.toLowerCase()}` }, ...options.map((o) => ({ value: o, label: f.type === 'bool' ? `${label}: ${o === 'true' ? 'yes' : 'no'}` : o }))]}
      />
    )
  }
  return (
    <form
      className="w-full max-w-56"
      onSubmit={(e) => {
        e.preventDefault()
        const r = filterValue(f, text)
        setError(r.error ?? '')
        if (!r.error) set(r.value || null)
      }}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) e.currentTarget.requestSubmit()
      }}
    >
      <Input
        aria-label={label}
        aria-invalid={!!error}
        title={error || undefined}
        type={f.type === 'time' ? 'datetime-local' : 'text'}
        placeholder={label}
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
    </form>
  )
}

// /views/:name: a brand view with its filters in the URL.
export function ViewPage() {
  const name = useParams().name!
  const views = useViews()
  const view = views?.find((v) => v.name === name)
  const [sp] = useSearchParams()
  const filters = Object.fromEntries((view?.filters ?? []).map((f) => [f.key, sp.get(f.key) ?? '']))
  const brand = useMe()?.brand.name || 'Admin'
  useEffect(() => {
    if (view) document.title = `${view.title} · ${brand}`
  }, [view, brand])
  if (!views) return <Skeleton className="h-48 w-full rounded-lg" />
  if (!view) return <Note>This page doesn't exist, or your role can't open it.</Note>
  return (
    <>
      <PageHeader title={view.title} crumbs={view.group ? [{ label: view.group }, { label: view.title }] : undefined} />
      {view.filters.length > 0 && (
        <Toolbar>
          {view.filters.map((f) => (
            <FilterInput key={`${view.name}.${f.key}`} filter={f} column={view.columns.find((c) => c.key === f.key)} />
          ))}
        </Toolbar>
      )}
      <ViewTable key={view.name} view={view} filters={filters} />
    </>
  )
}
