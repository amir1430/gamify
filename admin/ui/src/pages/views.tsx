import { useCustom, useList } from '@refinedev/core'
import { ExternalLink, MoreHorizontal, Plus, TriangleAlert } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { JsonSummary, JsonView } from '@/components/json'
import { Chips, FilterMenu, SearchBox, useLiveParam, useParams as useQueryParams } from '@/components/list'
import { PickList, TagInput } from '@/components/options'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import {
  actionState,
  cellText,
  csvValue,
  fieldOptions,
  fieldSections,
  filterChip,
  filterValue,
  formDirty,
  initialValue,
  inputValue,
  labelOf,
  linkText,
  optionLabel,
  splitFilters,
  toLocalInput,
  usesPickList,
  webURL,
  type FormValue,
  type ViewAction,
  type ViewColumn,
  type ViewDesc,
  type ViewField,
  type ViewFilter,
  type ViewRow,
} from '@/lib/views'
import { ChangeDialog, Choice, DataTable, ExportButton, Facts, Faint, Field, Note, PageHeader, Pill, Toolbar, downloadCSV, useAction, useMe, useMoney, usePaged, useParam } from '../ui'

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
      return <Pill>{optionLabel(col, String(v))}</Pill>
    case 'strings': {
      const l = Array.isArray(v) ? v.map((x) => optionLabel(col, String(x))) : []
      if (!l.length) return <Faint />
      const shown = full ? l : l.slice(0, 3)
      return (
        <span className="inline-flex flex-wrap items-center gap-1" title={l.join(', ')}>
          {shown.map((x) => (
            <Pill key={x}>{x}</Pill>
          ))}
          {l.length > shown.length && <span className="text-xs text-muted-foreground">+{l.length - shown.length}</span>}
        </span>
      )
    }
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
// click and the actions. hide: column keys not shown (a player tab hides its player column). tools:
// what sits left of the toolbar's buttons, applied: under it. tab: it's a player's tab, whose one player needs no page
// action. An action opened from the detail dialog goes back to it.
export function ViewTable({
  view,
  filters,
  hide = [],
  tools,
  applied,
  tab = false,
}: {
  view: ViewDesc
  filters: Record<string, string>
  hide?: string[]
  tools?: ReactNode
  applied?: ReactNode
  tab?: boolean
}) {
  const { rows, loading, error, more } = usePaged<ViewRow>(`views/${view.name}`, filters)
  const { decimals } = useMoney()
  const [open, setOpen] = useState<ViewRow | null>(null)
  const [acting, setActing] = useState<{ action: ViewAction; row?: ViewRow; back?: ViewRow } | null>(null)
  const cols = view.columns.filter((c) => !c.detail_only && !hide.includes(c.key))
  const pageActions = tab ? [] : view.page_actions
  const filtering = Object.values(filters).some(Boolean)
  const exportCSV = () => {
    const out = view.columns.filter((c) => c.type !== 'image' && !hide.includes(c.key))
    downloadCSV(
      view.name,
      out.map((c) => c.key),
      (rows ?? []).map((r) => out.map((c) => csvValue(c, r, decimals(String((c.currency_key ? r[c.currency_key] : c.currency) ?? ''))))),
    )
  }
  return (
    <>
      <Toolbar>
        {tools}
        <div className="ml-auto flex items-center gap-2">
          {pageActions.map((a) => (
            <Button key={a.name} size="sm" onClick={() => setActing({ action: a })}>
              <Plus /> {a.title}
            </Button>
          ))}
          <ExportButton disabled={!rows?.length} onExport={exportCSV} />
        </div>
      </Toolbar>
      {applied}
      {error && <Note className="text-danger">{error.message}</Note>}
      <DataTable
        rows={rows}
        loading={loading}
        rowKey={(r) => String(r.id)}
        onRowClick={(r) => setOpen(r)}
        empty={filtering ? 'Nothing matches these filters.' : 'Nothing here.'}
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
                  render: (r: ViewRow) => <ActionsMenu actions={view.actions} row={r} onPick={(action) => setActing({ action, row: r })} />,
                },
              ]
            : []),
        ]}
      />
      {more}
      {open && (
        <RowDialog
          view={view}
          row={rows?.find((r) => r.id === open.id) ?? open}
          onClose={() => setOpen(null)}
          onAction={(action, row) => {
            setOpen(null)
            setActing({ action, row, back: row })
          }}
        />
      )}
      {acting && (
        <ActionDialog
          view={view}
          action={acting.action}
          row={acting.row}
          onClose={() => {
            if (acting.back) setOpen(acting.back)
            setActing(null)
          }}
        />
      )}
    </>
  )
}

// An unavailable action stays listed, greyed out with its reason, so it's clear why it can't run.
function ActionsMenu({ actions, row, onPick }: { actions: ViewAction[]; row: ViewRow; onPick: (a: ViewAction) => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" aria-label="Actions" onClick={(e) => e.stopPropagation()}>
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-w-72" onClick={(e) => e.stopPropagation()}>
        {actions.map((a) => {
          const { unavailable } = actionState(row, a.name)
          return (
            <DropdownMenuItem key={a.name} disabled={!!unavailable} onSelect={() => onPick(a)} className={a.confirm ? 'text-danger' : undefined}>
              <span>
                {a.title}
                {unavailable && <span className="block text-xs text-muted-foreground">{unavailable}</span>}
              </span>
            </DropdownMenuItem>
          )
        })}
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
          {view.actions.map((a) => {
            const { unavailable } = actionState(r, a.name)
            return (
              <Button key={a.name} type="button" variant={a.confirm ? 'destructive' : 'outline'} disabled={!!unavailable} title={unavailable} onClick={() => onAction(a, r)}>
                {a.title}
              </Button>
            )
          })}
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

// The row a form changes: the view's Summary columns, atop the form.
function RowSummary({ view, row }: { view: ViewDesc; row: ViewRow }) {
  const cols = view.summary.map((k) => view.columns.find((c) => c.key === k)).filter((c) => c != null)
  if (!cols.length) return null
  return (
    <div className="flex items-center gap-3 rounded-lg border bg-muted/40 p-3">
      {cols
        .filter((c) => c.type === 'image' && webURL(row[c.key]))
        .map((c) => (
          <Cell key={c.key} col={c} row={row} />
        ))}
      <dl className="flex min-w-0 flex-wrap gap-x-6 gap-y-1">
        {cols
          .filter((c) => c.type !== 'image')
          .map((c) => (
            <div key={c.key} className="min-w-0">
              <dt className="text-xs text-muted-foreground">{labelOf(c)}</dt>
              <dd className="text-sm">
                <Cell col={c} row={row} full />
              </dd>
            </div>
          ))}
      </dl>
    </div>
  )
}

// An action's dialog. A field with row-dependent options may need the full record: the list's row
// doesn't always carry them, so it's fetched before the form opens.
export function ActionDialog({ view, action, row, onClose }: { view: ViewDesc; action: ViewAction; row?: ViewRow; onClose: () => void }) {
  const fetch = !!row && view.has_detail && action.fields.some((f) => f.options_from && row[f.options_from] === undefined)
  const { query } = useCustom<ViewRow>({
    url: `/api/views/${view.name}/${encodeURIComponent(String(row?.id))}`,
    method: 'get',
    queryOptions: { enabled: fetch },
    errorNotification: false,
  })
  const full = query.data?.data
  if (!fetch || full) return <ActionForm view={view} action={action} row={full ? { ...row, ...full } : row} onClose={onClose} />
  return (
    <ChangeDialog
      title={`${action.title} · #${String(row?.id)}`}
      large
      busy={false}
      onClose={onClose}
      footer={
        <Button type="button" variant="ghost" onClick={onClose}>
          Close
        </Button>
      }
    >
      {query.isError ? <Note className="text-danger">{query.error.message}</Note> : <Skeleton className="h-48 w-full" />}
    </ChangeDialog>
  )
}

// An action's form: its fields (prefilled from the row where it has the same key) by section, the
// reason when the action needs one, its confirmation text; POST …/actions/<name>, then every list
// refetches. A form of several fields, or one with a list to pick from, gets a bigger dialog.
function ActionForm({ view, action, row, onClose }: { view: ViewDesc; action: ViewAction; row?: ViewRow; onClose: () => void }) {
  const { decimals } = useMoney()
  const dec = (f: ViewField) => (f.type === 'money' && f.currency ? decimals(f.currency) : 0)
  const [initial] = useState<Record<string, FormValue>>(() => Object.fromEntries(action.fields.map((f) => [f.key, initialValue(f, row, dec(f))])))
  const [values, setValues] = useState(initial)
  const [reason, setReason] = useState('')
  const [typed, setTyped] = useState('')
  const [tried, setTried] = useState(false)
  const { busy, run } = useAction()
  const results = new Map(action.fields.map((f) => [f.key, inputValue(f, values[f.key] ?? '', dec(f))]))
  const invalid = [...results.values()].some((p) => p.error)
  const set = (k: string, v: FormValue) => setValues((x) => ({ ...x, [k]: v }))
  const large = action.fields.length >= 4 || action.fields.some((f) => usesPickList(f) || f.type === 'strings')
  const { impact } = actionState(row, action.name)
  const path = row ? `${encodeURIComponent(String(row.id))}/actions` : 'actions'

  return (
    <ChangeDialog
      title={row ? `${action.title} · #${String(row.id)}` : action.title}
      description={action.confirm || undefined}
      okText={action.title}
      danger={!!action.confirm}
      large={large}
      busy={busy}
      disabled={!!action.type_confirm && typed.trim() !== action.type_confirm}
      dirty={formDirty(values, initial) || !!typed}
      reason={action.reason ? reason : undefined}
      onReason={action.reason ? setReason : undefined}
      onClose={onClose}
      onSubmit={async () => {
        setTried(true)
        if (invalid) return
        const input: Record<string, unknown> = {}
        for (const [k, p] of results) if (p.value !== undefined) input[k] = p.value
        const r = await run(`/api/views/${view.name}/${path}/${action.name}`, {
          method: 'POST',
          body: { reason: reason.trim(), input },
        })
        if (!r.ok) return
        toast.success(`${action.title}: done`)
        onClose()
      }}
    >
      {row && <RowSummary view={view} row={row} />}
      {impact && (
        <p className="flex items-start gap-2 rounded-lg border border-warn/40 bg-warn/10 p-3 text-sm">
          <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warn" />
          {impact}
        </p>
      )}
      {fieldSections(action.fields).map((s, i) => (
        <section key={`${s.group}.${i}`} className="grid gap-3">
          {s.group && <h3 className="text-sm font-medium">{s.group}</h3>}
          <div className={large ? 'grid gap-4 md:grid-cols-2' : 'grid gap-4'}>
            {s.fields.map((f) => (
              <FieldInput
                key={f.key}
                field={f}
                row={row}
                value={values[f.key] ?? ''}
                error={tried ? results.get(f.key)?.error : undefined}
                onChange={(v) => set(f.key, v)}
              />
            ))}
          </div>
        </section>
      ))}
      {action.type_confirm && (
        <Field label={`Type ${action.type_confirm} to confirm`}>
          <Input aria-label="Confirmation" autoComplete="off" className="font-mono" value={typed} onChange={(e) => setTyped(e.target.value)} />
        </Field>
      )}
    </ChangeDialog>
  )
}

function FieldInput({
  field: f,
  row,
  value,
  error,
  onChange,
}: {
  field: ViewField
  row?: ViewRow
  value: FormValue
  error?: string
  onChange: (v: FormValue) => void
}) {
  const label = labelOf(f) + (f.required ? '' : ' (optional)')
  if (f.type === 'bool')
    return (
      <label className="flex items-center justify-between gap-4 rounded-md border px-3 py-2.5 text-sm">
        <span>
          {labelOf(f)}
          {f.help && <span className="mt-0.5 block text-xs text-muted-foreground">{f.help}</span>}
        </span>
        <Switch checked={value === true} onCheckedChange={(v) => onChange(v)} />
      </label>
    )
  if (usesPickList(f)) {
    const multi = f.type === 'strings'
    return (
      <Field label={label} error={error} hint={f.help} className="col-span-full">
        <PickList
          label={labelOf(f)}
          multi={multi}
          options={fieldOptions(f, row)}
          value={multi ? (value as string[]) : value ? [String(value)] : []}
          onChange={(v) => onChange(multi ? v : (v[0] ?? ''))}
        />
      </Field>
    )
  }
  if (f.type === 'strings')
    return (
      <Field label={label} error={error} hint={f.help ?? 'Enter or a comma adds one.'} className="col-span-full">
        <TagInput label={labelOf(f)} value={value as string[]} onChange={onChange} placeholder={f.placeholder} />
      </Field>
    )
  const s = String(value)
  const auto =
    f.type === 'money' ? (f.currency ? f.currency.toUpperCase() : 'In minor units.') : f.type === 'time' ? 'Your local time.' : f.type === 'json' ? 'JSON.' : undefined
  const hint = [f.help, auto].filter(Boolean).join(' ') || undefined
  return (
    <Field label={label} error={error} hint={hint} className={f.type === 'json' ? 'col-span-full' : undefined}>
      {f.type === 'enum' ? (
        <Choice
          className="w-full"
          value={s}
          onChange={onChange}
          options={[...(f.required ? [] : [{ value: '', label: '—' }]), ...(f.options ?? []).map((o) => ({ value: o, label: optionLabel(f, o) }))]}
          placeholder="Pick one"
        />
      ) : f.type === 'json' ? (
        <Textarea className="font-mono text-xs" rows={4} placeholder={f.placeholder} value={s} onChange={(e) => onChange(e.target.value)} />
      ) : (
        <Input
          type={f.type === 'time' ? 'datetime-local' : f.type === 'url' || f.type === 'image' ? 'url' : 'text'}
          placeholder={f.placeholder ?? (f.type === 'url' || f.type === 'image' ? 'https://' : undefined)}
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

// A filter with an input of its own, in the URL (?<key>=), so a filtered view is a link. It applies
// on Enter and on leaving the box.
function FilterInput({ filter: f }: { filter: ViewFilter }) {
  const [v, set] = useParam(f.key)
  const shown = f.type === 'time' && v ? toLocalInput(v) : v
  const [text, setText] = useState(shown)
  const [error, setError] = useState('')
  const label = labelOf(f)
  useEffect(() => setText(shown), [shown]) // a chip cleared it
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

// The first Text filter, applied as it's typed.
function FilterSearch({ filter: f }: { filter: ViewFilter }) {
  const [text, setText] = useLiveParam(f.key)
  return <SearchBox value={text} onChange={setText} placeholder={labelOf(f)} />
}

// A view's filters: the search, the filter menu, inputs for the rest, and what's applied as chips.
function ViewFilters({ view }: { view: ViewDesc }) {
  const { sp, set } = useQueryParams()
  const { menu, search, inputs } = splitFilters(view.filters)
  const value = (f: ViewFilter) => sp.get(f.key) ?? ''
  const groups = menu.map((f) => {
    const options = f.type === 'bool' ? ['true', 'false'] : (f.options ?? [])
    return { key: f.key, label: labelOf(f), single: true, options: options.map((o) => ({ value: o, label: filterChip(f, o) })) }
  })
  return (
    <>
      {search && <FilterSearch filter={search} />}
      <FilterMenu
        groups={groups}
        values={Object.fromEntries(menu.map((f) => [f.key, value(f) ? [value(f)] : []]))}
        onChange={(k, v) => set({ [k]: v[0] ?? null })}
        onClear={() => set(Object.fromEntries(menu.map((f) => [f.key, null])))}
      />
      {inputs.map((f) => (
        <FilterInput key={f.key} filter={f} />
      ))}
    </>
  )
}

function FilterChips({ view }: { view: ViewDesc }) {
  const { sp, set } = useQueryParams()
  const applied = view.filters.filter((f) => sp.get(f.key))
  return (
    <Chips
      onClear={() => set(Object.fromEntries(applied.map((f) => [f.key, null])))}
      items={applied.map((f) => ({
        key: f.key,
        label: (
          <>
            <span className="text-muted-foreground">{labelOf(f)}:</span> {filterChip(f, sp.get(f.key)!)}
          </>
        ),
        onRemove: () => set({ [f.key]: null }),
      }))}
    />
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
      <ViewTable key={view.name} view={view} filters={filters} tools={<ViewFilters key={view.name} view={view} />} applied={<FilterChips view={view} />} />
    </>
  )
}
