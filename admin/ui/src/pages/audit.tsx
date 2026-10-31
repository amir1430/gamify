import { useList } from '@refinedev/core'
import { ListFilter } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { Chips, FilterBy, RangeFilter, SearchBox, useLiveParam, useParams, useRange } from '@/components/list'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { Choice, DataTable, ExportButton, Facts, Faint, Mono, PageHeader, Pill, Toolbar, When, dateTime, downloadCSV, useParam, usePaged, usePerm } from '../ui'

type Entry = {
  id: number
  admin_id: number | null
  action: string
  target: string
  before: unknown
  after: unknown
  reason: string | null
  ip: string | null
  created_at: string
}

// The platform's actions by area; brand views add view.<name>.<action> (searchable as text).
const ACTIONS: { label: string; options: string[] }[] = [
  { label: 'Sign-in', options: ['login.ok', 'login.fail', 'logout', 'totp.enrol'] },
  { label: 'Console users', options: ['admin.create', 'admin.update', 'admin.password'] },
  { label: 'Games', options: ['game.set', 'game.limit.set', 'game.limit.delete'] },
  { label: 'Players', options: ['user.unlink', 'user.disconnect'] },
  { label: 'Adjustments', options: ['adjust.request', 'adjust.approve', 'adjust.reject'] },
]
const KNOWN = ACTIONS.flatMap((g) => g.options)

type Admin = { id: number; email: string }

// Every console change and sign-in, newest first. Search (?q=: action, target, reason) and filters
// (?admin=, ?action=, ?target=, a time range) are the API's; a row opens its before/after, and its
// action or admin filters the log by itself.
export function Audit() {
  const { set } = useParams()
  const [admin, setAdmin] = useParam('admin')
  const [action, setAction] = useParam('action')
  const [target, setTarget] = useParam('target')
  const [text, setText, q] = useLiveParam('q')
  const [idText, setIdText] = useState(admin)
  const range = useRange()
  const [open, setOpen] = useState<Entry | null>(null)
  const canAdmins = usePerm('admins')
  const admins = useList<Admin>({ resource: 'admins', pagination: { mode: 'off' }, queryOptions: { enabled: canAdmins }, errorNotification: false }).result.data
  const email = (id: number | null) => (id == null ? null : (admins.find((x) => x.id === id)?.email ?? null))
  const { rows, loading, more } = usePaged<Entry>('audit', { admin, action, target, q, since: range.since, until: range.until })
  const clear = () => (setText(''), setIdText(''), set({ admin: null, action: null, target: null, q: null, range: null, from: null, to: null }))
  const chips = [
    q && { key: 'q', label: <>“{q}”</>, onRemove: () => setText('') },
    action && { key: 'action', label: <>Action: {action}</>, onRemove: () => setAction(null) },
    admin && { key: 'admin', label: <>By {email(Number(admin)) ?? `admin #${admin}`}</>, onRemove: () => (setAdmin(null), setIdText('')) },
    target && { key: 'target', label: <>Target: {target}</>, onRemove: () => setTarget(null) },
    range.range && { key: 'range', label: range.label, onRemove: () => range.set('') },
  ].filter((c) => !!c) as { key: string; label: ReactNode; onRemove: () => void }[]

  return (
    <>
      <PageHeader title="Audit log" description="Every change made from the console, with who, why and what it was before and after." />
      <Toolbar>
        <SearchBox value={text} onChange={setText} placeholder="Search action, target or reason" />
        <Choice
          label="Action"
          value={!action || KNOWN.includes(action) ? action : 'other'}
          onChange={(v) => setAction(v || null)}
          options={[{ value: '', label: 'All actions' }, ...(action && !KNOWN.includes(action) ? [{ value: 'other', label: action }] : [])]}
          groups={ACTIONS.map((g) => ({ label: g.label, options: g.options.map((o) => ({ value: o, label: o })) }))}
        />
        {canAdmins ? (
          <Choice
            label="Console user"
            className="min-w-44"
            value={admin}
            onChange={(v) => setAdmin(v || null)}
            options={[{ value: '', label: 'Any console user' }, ...admins.map((x) => ({ value: String(x.id), label: x.email }))]}
          />
        ) : (
          <Input
            className="w-32"
            inputMode="numeric"
            placeholder="Admin ID"
            value={idText}
            onChange={(e) => {
              const v = e.target.value.replace(/\D/g, '')
              setIdText(v)
              setAdmin(v || null)
            }}
            aria-label="Admin ID"
          />
        )}
        <RangeFilter r={range} />
        <div className="ml-auto">
          <ExportButton
            disabled={!rows?.length}
            onExport={() =>
              downloadCSV(
                'audit',
                ['id', 'created_at', 'admin_id', 'admin_email', 'action', 'target', 'reason', 'ip', 'before', 'after'],
                (rows ?? []).map((e) => [e.id, e.created_at, e.admin_id, email(e.admin_id), e.action, e.target, e.reason, e.ip, e.before, e.after]),
              )
            }
          />
        </div>
      </Toolbar>
      <Chips items={chips} onClear={clear} />
      <DataTable
        rows={rows}
        loading={loading}
        rowKey={(e) => e.id}
        empty={chips.length ? 'No entries match these filters.' : 'No entries yet.'}
        onRowClick={setOpen}
        columns={[
          { key: 'when', title: 'When', render: (e) => <When at={e.created_at} /> },
          {
            key: 'admin',
            title: 'Admin',
            render: (e) =>
              e.admin_id == null ? (
                <Faint />
              ) : (
                <FilterBy on={() => (setAdmin(String(e.admin_id)), setIdText(String(e.admin_id)))} title="Only this console user">
                  <span className="text-xs">{email(e.admin_id) ?? <Mono>#{e.admin_id}</Mono>}</span>
                </FilterBy>
              ),
          },
          {
            key: 'action',
            title: 'Action',
            render: (e) => (
              <FilterBy on={() => setAction(e.action)} title="Only this action">
                <Pill tone={e.action === 'login.fail' ? 'danger' : 'neutral'}>{e.action}</Pill>
              </FilterBy>
            ),
          },
          { key: 'target', title: 'Target', render: (e) => <Target t={e.target} onFilter={() => setTarget(e.target)} /> },
          { key: 'reason', title: 'Reason', className: 'whitespace-normal min-w-48 text-muted-foreground', render: (e) => e.reason || <Faint /> },
          { key: 'ip', title: 'IP', render: (e) => (e.ip ? <Mono>{e.ip}</Mono> : <Faint />) },
        ]}
      />
      {more}
      {open && <EntryDialog e={open} onClose={() => setOpen(null)} />}
    </>
  )
}

// user:42 → the player, adjust:7 → adjustments, game:arena/… → the game; anything else as text.
// The funnel filters the log to this target.
function Target({ t, onFilter }: { t: string; onFilter: () => void }) {
  const [kind, rest = ''] = t.split(/:(.*)/s)
  const to =
    kind === 'user' ? `/players/${rest}` : kind === 'adjust' ? '/adjustments' : kind === 'game' ? `/games/${encodeURIComponent(rest.split('/')[0])}` : null
  return (
    <span className="group inline-flex items-center gap-1.5">
      {to ? (
        <Link to={to} className="font-mono text-xs text-brand hover:underline" onClick={(e) => e.stopPropagation()}>
          {t}
        </Link>
      ) : (
        <Mono>{t}</Mono>
      )}
      <button
        type="button"
        title="Only this target"
        aria-label="Only this target"
        onClick={(e) => (e.stopPropagation(), onFilter())}
        className="text-muted-foreground opacity-0 group-hover:opacity-100 hover:text-foreground focus-visible:opacity-100"
      >
        <ListFilter className="size-3.5" />
      </button>
    </span>
  )
}

const show = (v: unknown) => (v === undefined ? '' : JSON.stringify(v, null, 2))

// Before and after side by side, key by key for objects, changed keys marked.
function EntryDialog({ e, onClose }: { e: Entry; onClose: () => void }) {
  const obj = (v: unknown) => (v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null)
  const b = obj(e.before)
  const a = obj(e.after)
  const keys = b || a ? [...new Set([...Object.keys(b ?? {}), ...Object.keys(a ?? {})])].sort() : null
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>
            {e.action} · <span className="font-mono text-sm">{e.target}</span>
          </DialogTitle>
          <DialogDescription>
            #{e.id} · {dateTime(e.created_at)}
          </DialogDescription>
        </DialogHeader>
        <Facts
          cols={3}
          items={[
            ['Admin', e.admin_id == null ? '—' : `#${e.admin_id}`],
            ['IP', e.ip ?? '—'],
            ['Reason', e.reason || '—'],
          ]}
        />
        {keys ? (
          <div className="overflow-hidden rounded-lg border text-sm">
            <div className="grid grid-cols-[minmax(8rem,auto)_1fr_1fr] bg-muted/40 text-xs font-medium text-muted-foreground">
              <div className="px-3 py-2">Field</div>
              <div className="px-3 py-2">Before</div>
              <div className="px-3 py-2">After</div>
            </div>
            {keys.map((k) => {
              const was = show(b?.[k])
              const now = show(a?.[k])
              return (
                <div key={k} className={cn('grid grid-cols-[minmax(8rem,auto)_1fr_1fr] border-t', was !== now && 'bg-warn/10')}>
                  <div className="px-3 py-2 font-mono text-xs">{k}</div>
                  <pre className="px-3 py-2 font-mono text-xs whitespace-pre-wrap text-muted-foreground">{was || '—'}</pre>
                  <pre className="px-3 py-2 font-mono text-xs whitespace-pre-wrap">{now || '—'}</pre>
                </div>
              )
            })}
          </div>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2">
            {(['Before', 'After'] as const).map((l) => {
              const v = l === 'Before' ? e.before : e.after
              return (
                <div key={l}>
                  <div className="mb-1 text-xs text-muted-foreground">{l}</div>
                  <pre className="rounded-lg border bg-muted/30 p-3 font-mono text-xs whitespace-pre-wrap">{v == null ? '—' : show(v)}</pre>
                </div>
              )
            })}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
