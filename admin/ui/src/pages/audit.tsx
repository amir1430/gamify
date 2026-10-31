import { Search } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { Choice, DataTable, ExportButton, Facts, Faint, Mono, PageHeader, Pill, Toolbar, When, dateTime, downloadCSV, useParam, usePaged } from '../ui'

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

// The platform's actions; brand views add view.<name>.<action>, typed into the filter as text.
const ACTIONS = [
  'login.ok',
  'login.fail',
  'logout',
  'totp.enrol',
  'admin.create',
  'admin.update',
  'admin.password',
  'game.set',
  'game.limit.set',
  'game.limit.delete',
  'user.unlink',
  'user.disconnect',
  'adjust.request',
  'adjust.approve',
  'adjust.reject',
]

// Every console change and sign-in, newest first. Filters (?admin=, ?action=, ?target=) are the
// API's; a row opens its before/after.
export function Audit() {
  const [admin, setAdmin] = useParam('admin')
  const [action, setAction] = useParam('action')
  const [target, setTarget] = useParam('target')
  const [text, setText] = useState({ admin, target })
  const [open, setOpen] = useState<Entry | null>(null)
  const { rows, loading, more } = usePaged<Entry>('audit', { admin, action, target })

  return (
    <>
      <PageHeader title="Audit log" description="Every change made from the console, with who, why and what it was before and after." />
      <Toolbar>
        <Choice
          label="Action"
          value={ACTIONS.includes(action) ? action : ''}
          onChange={(v) => setAction(v || null)}
          options={[{ value: '', label: 'All actions' }, ...ACTIONS.map((a) => ({ value: a, label: a }))]}
        />
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            setAdmin(text.admin.trim() || null)
            setTarget(text.target.trim() || null)
          }}
        >
          <Input
            className="w-32"
            inputMode="numeric"
            placeholder="Admin ID"
            value={text.admin}
            onChange={(e) => setText((t) => ({ ...t, admin: e.target.value }))}
            aria-label="Admin ID"
          />
          <div className="relative w-full max-w-xs">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="pl-8"
              type="search"
              placeholder="Target, e.g. user:42 or game:arena"
              value={text.target}
              onChange={(e) => setText((t) => ({ ...t, target: e.target.value }))}
              aria-label="Target"
            />
          </div>
          <Button type="submit" variant="outline">
            Filter
          </Button>
        </form>
        {(admin || action || target) && (
          <Button variant="ghost" size="sm" onClick={() => (setAdmin(null), setAction(null), setTarget(null), setText({ admin: '', target: '' }))}>
            Clear
          </Button>
        )}
        <div className="ml-auto">
          <ExportButton
            disabled={!rows?.length}
            onExport={() =>
              downloadCSV(
                'audit',
                ['id', 'created_at', 'admin_id', 'action', 'target', 'reason', 'ip', 'before', 'after'],
                (rows ?? []).map((e) => [e.id, e.created_at, e.admin_id, e.action, e.target, e.reason, e.ip, e.before, e.after]),
              )
            }
          />
        </div>
      </Toolbar>
      <DataTable
        rows={rows}
        loading={loading}
        rowKey={(e) => e.id}
        empty={admin || action || target ? 'No entries match these filters.' : 'No entries yet.'}
        onRowClick={setOpen}
        columns={[
          { key: 'when', title: 'When', render: (e) => <When at={e.created_at} /> },
          { key: 'admin', title: 'Admin', render: (e) => (e.admin_id == null ? <Faint /> : <Mono>#{e.admin_id}</Mono>) },
          { key: 'action', title: 'Action', render: (e) => <Pill tone={e.action === 'login.fail' ? 'danger' : 'neutral'}>{e.action}</Pill> },
          { key: 'target', title: 'Target', render: (e) => <Target t={e.target} /> },
          { key: 'reason', title: 'Reason', className: 'whitespace-normal min-w-48 text-muted-foreground', render: (e) => e.reason || <Faint /> },
          { key: 'ip', title: 'IP', render: (e) => (e.ip ? <Mono>{e.ip}</Mono> : <Faint />) },
        ]}
      />
      {more}
      {open && <EntryDialog e={open} onClose={() => setOpen(null)} />}
    </>
  )
}

// user:42 → the player, adjust:7 → adjustments, game:arena/… → games; anything else as text.
function Target({ t }: { t: string }) {
  const [kind, rest] = t.split(/:(.*)/s)
  const to = kind === 'user' ? `/players/${rest}` : kind === 'adjust' ? '/adjustments' : kind === 'game' ? '/games' : null
  if (!to) return <Mono>{t}</Mono>
  return (
    <Link to={to} className="font-mono text-xs text-brand hover:underline" onClick={(e) => e.stopPropagation()}>
      {t}
    </Link>
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
