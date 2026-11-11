import { useCustom } from '@refinedev/core'
import { Blocks, Check, Cpu, Database, HeartPulse, KeyRound, Minus, Plug, RefreshCw, Search, Settings2, Wallet } from 'lucide-react'
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { bytes, duration } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Card, DataTable, Faint, Mono, Note, PageHeader, Pill, useMoney, useNow } from '../ui'
import { labelOf, type ViewColumn, type ViewRow } from '@/lib/views'
import { HealthRow } from './dashboard'
import { Cell } from './views'

export type Health = { ok: boolean; ms: number; error: string | null }
export type SystemInfo = {
  version: string
  go: string
  started_at: string
  runtime: { os: string; arch: string; cpus: number; gomaxprocs: number; goroutines: number; heap_bytes: number; sys_bytes: number; gc_runs: number }
  pool: { max: number; total: number; idle: number; in_use: number; acquires: number; waited: number }
  health: Record<string, Health>
  currencies: { code: string; decimals: number; min_amount: number; approve_above: number | null; default: boolean; transferable: boolean; withdrawable: boolean }[]
  rpcs: string[]
  history: string[]
  roles: Record<string, string[]>
  console: { telegram_login: boolean; fairness: boolean; ip_allowlist: number; views: number; brand_perms: number }
  sections: StatusSection[]
}
// A module's status table (admin.Status in Go), drawn with the views' cells.
export type StatusSection = { name: string; title: string; columns: ViewColumn[]; items: ViewRow[]; error: string | null }

const ROLES = ['owner', 'operator', 'support', 'viewer']

// Nakama's Status and Configuration in one page: the dependencies' health, the build and process,
// the database pool, the currencies, every RPC the brand registered (by namespace), the activity
// sources and what each console role may do.
export function System() {
  const { query } = useCustom<SystemInfo>({ url: '/api/system', method: 'get', queryOptions: { refetchInterval: 10_000 } })
  const s = query.data?.data
  const now = useNow(1000)
  const { money } = useMoney()
  const down = s ? Object.values(s.health).filter((h) => !h.ok).length : 0

  return (
    <>
      <PageHeader
        title="System"
        description="Health of what the platform runs on, the build, and how this brand is configured. Refreshes every 10 s."
        meta={s && <Pill tone={down ? 'danger' : 'ok'} dot>{down ? `${down} down` : 'All systems healthy'}</Pill>}
        actions={
          <Button variant="outline" size="sm" onClick={() => void query.refetch()} disabled={query.isFetching}>
            <RefreshCw className={query.isFetching ? 'animate-spin' : ''} /> Refresh
          </Button>
        }
      />
      {query.isError && <Note className="text-danger">{query.error.message}</Note>}
      {!s ? (
        <div className="grid gap-4 lg:grid-cols-3">
          {Array.from({ length: 3 }, (_, i) => (
            <Skeleton key={i} className="h-48 rounded-xl" />
          ))}
        </div>
      ) : (
        <>
          <div className="grid gap-4 lg:grid-cols-3">
            <Card title="Dependencies" icon={HeartPulse}>
              <div className="space-y-3">
                {Object.entries(s.health)
                  .sort(([a], [b]) => a.localeCompare(b))
                  .map(([n, h]) => (
                    <div key={n}>
                      <HealthRow name={n} h={h} />
                      {h.error && <p className="mt-1 ml-5 font-mono text-xs break-all text-danger">{h.error}</p>}
                    </div>
                  ))}
              </div>
            </Card>
            <Card title="Build & process" icon={Cpu}>
              <Pairs
                items={[
                  ['gamify', <Mono>{s.version}</Mono>],
                  ['Go', <Mono>{s.go}</Mono>],
                  ['Platform', `${s.runtime.os}/${s.runtime.arch}`],
                  ['Uptime', <span title={new Date(s.started_at).toLocaleString()}>{duration(now - Date.parse(s.started_at))}</span>],
                  ['CPUs · GOMAXPROCS', `${s.runtime.cpus} · ${s.runtime.gomaxprocs}`],
                  ['Goroutines', s.runtime.goroutines.toLocaleString()],
                  ['Heap · from OS', `${bytes(s.runtime.heap_bytes)} · ${bytes(s.runtime.sys_bytes)}`],
                  ['GC runs', s.runtime.gc_runs.toLocaleString()],
                ]}
              />
            </Card>
            <Card title="Database pool" icon={Database}>
              <div className="mb-4">
                <div className="mb-1.5 flex items-baseline justify-between text-xs text-muted-foreground">
                  <span>Connections in use</span>
                  <span className="num text-sm font-semibold text-foreground">
                    {s.pool.in_use} / {s.pool.max}
                  </span>
                </div>
                <Meter parts={[{ n: s.pool.in_use, cls: 'bg-brand' }, { n: s.pool.idle, cls: 'bg-brand/30' }]} of={s.pool.max} />
                <div className="mt-1.5 flex gap-3 text-[11px] text-muted-foreground">
                  <span className="flex items-center gap-1">
                    <span className="size-2 rounded-sm bg-brand" /> in use
                  </span>
                  <span className="flex items-center gap-1">
                    <span className="size-2 rounded-sm bg-brand/30" /> idle
                  </span>
                  <span className="flex items-center gap-1">
                    <span className="size-2 rounded-sm bg-muted" /> not opened
                  </span>
                </div>
              </div>
              <Pairs
                items={[
                  ['Open', s.pool.total.toLocaleString()],
                  ['Acquires', s.pool.acquires.toLocaleString()],
                  ['Had to wait', <span className={s.pool.waited ? 'text-warn' : ''}>{s.pool.waited.toLocaleString()}</span>],
                ]}
              />
            </Card>
          </div>

          <div className="mt-4 grid gap-4 lg:grid-cols-3">
            <Card className="lg:col-span-2" title="Currencies" icon={Wallet} bodyClassName="p-0">
              <DataTable
                className="rounded-none border-0 shadow-none"
                rows={s.currencies}
                rowKey={(c) => c.code}
                columns={[
                  {
                    key: 'code',
                    title: 'Currency',
                    render: (c) => (
                      <span className="flex items-center gap-2 font-medium">
                        {c.code.toUpperCase()} {c.default && <Pill tone="brand">default</Pill>}
                      </span>
                    ),
                  },
                  { key: 'dec', title: 'Decimals', align: 'right', render: (c) => <span className="num">{c.decimals}</span> },
                  { key: 'min', title: 'Min amount', align: 'right', render: (c) => <span className="num">{money(c.min_amount, c.code)}</span> },
                  {
                    key: 'appr',
                    title: 'Second approver above',
                    align: 'right',
                    render: (c) => (c.approve_above == null ? <Faint>never</Faint> : <span className="num">{money(c.approve_above, c.code)}</span>),
                  },
                  { key: 'transfer', title: 'Player transfers', render: (c) => <YesNo on={c.transferable} /> },
                  { key: 'withdraw', title: 'Withdrawable', render: (c) => <YesNo on={c.withdrawable} /> },
                ]}
              />
            </Card>
            <Card title="Console" icon={Settings2}>
              <Pairs
                items={[
                  ['Telegram login', <YesNo on={s.console.telegram_login} />],
                  ['Provable fairness', <YesNo on={s.console.fairness} />],
                  ['IP allowlist', s.console.ip_allowlist ? `${s.console.ip_allowlist} range${s.console.ip_allowlist > 1 ? 's' : ''}` : <Faint>open</Faint>],
                  ['Brand views', s.console.views.toLocaleString()],
                  ['Brand permissions', s.console.brand_perms.toLocaleString()],
                  ['Activity sources', s.history.length ? s.history.join(', ') : <Faint>none</Faint>],
                ]}
              />
            </Card>
          </div>

          {s.sections.map((sec) => (
            <Section key={sec.name} section={sec} />
          ))}

          <div className="mt-4 grid gap-4 lg:grid-cols-2">
            <Rpcs rpcs={s.rpcs} />
            <Card title="Roles & permissions" icon={KeyRound} bodyClassName="p-0">
              <Matrix roles={s.roles} />
            </Card>
          </div>
        </>
      )}
    </>
  )
}

// A module's status table; a section whose rows failed says so instead.
function Section({ section: sec }: { section: StatusSection }) {
  return (
    <Card className="mt-4" title={sec.title} icon={Blocks} bodyClassName="p-0">
      {sec.error ? (
        <Note className="m-0 px-4 py-3 text-danger">Couldn't load this section.</Note>
      ) : (
        <DataTable
          className="rounded-none border-0 shadow-none"
          rows={sec.items.map((r, i) => ({ ...r, id: i }))}
          rowKey={(r) => r.id}
          empty="Nothing registered."
          columns={sec.columns.map((c) => ({
            key: c.key,
            title: labelOf(c),
            align: c.type === 'int' || c.type === 'decimal' || c.type === 'money' ? ('right' as const) : undefined,
            render: (r: ViewRow) => <Cell col={c} row={r} />,
          }))}
        />
      )}
    </Card>
  )
}

const YesNo = ({ on }: { on: boolean }) => <Pill tone={on ? 'ok' : 'neutral'}>{on ? 'On' : 'Off'}</Pill>

function Pairs({ items }: { items: [string, React.ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
      {items.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted-foreground">{k}</dt>
          <dd className="num min-w-0 truncate text-right">{v}</dd>
        </div>
      ))}
    </dl>
  )
}

function Meter({ parts, of }: { parts: { n: number; cls: string }[]; of: number }) {
  return (
    <div className="flex h-2 gap-0.5 overflow-hidden rounded-full bg-muted">
      {parts.map((p, i) => (p.n ? <div key={i} className={cn('h-full', p.cls)} style={{ width: `${(p.n / Math.max(of, 1)) * 100}%` }} /> : null))}
    </div>
  )
}

// RPC methods grouped by namespace (the part before the first "."), filterable.
function Rpcs({ rpcs }: { rpcs: string[] }) {
  const [q, setQ] = useState('')
  const shown = rpcs.filter((r) => r.toLowerCase().includes(q.trim().toLowerCase()))
  const ns = new Map<string, string[]>()
  for (const r of shown) {
    const n = r.includes('.') ? r.slice(0, r.indexOf('.')) : '(none)'
    ns.set(n, [...(ns.get(n) ?? []), r])
  }
  return (
    <Card
      title={`RPCs · ${rpcs.length}`}
      icon={Plug}
      actions={
        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input className="h-7 w-40 pl-7 text-xs" placeholder="Filter" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Filter RPCs" />
        </div>
      }
    >
      {!shown.length ? (
        <p className="py-6 text-center text-sm text-muted-foreground">No RPC matches.</p>
      ) : (
        <div className="max-h-96 space-y-4 overflow-y-auto pr-1">
          {[...ns].map(([n, ms]) => (
            <div key={n}>
              <div className="mb-1.5 flex items-center gap-2 text-xs font-medium">
                {n} <span className="rounded-full bg-secondary px-1.5 text-[11px] text-muted-foreground">{ms.length}</span>
              </div>
              <div className="flex flex-wrap gap-1.5">
                {ms.map((m) => (
                  <span key={m} className="rounded-md border bg-muted/40 px-2 py-0.5 font-mono text-xs">
                    {m}
                  </span>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

function Matrix({ roles }: { roles: Record<string, string[]> }) {
  const perms = [...new Set(Object.values(roles).flat())].sort((a, b) => Number(a.includes('.')) - Number(b.includes('.')) || a.localeCompare(b))
  return (
    <div className="max-h-[26rem] overflow-auto">
      <table className="w-full text-sm">
        <thead className="sticky top-0 bg-card">
          <tr className="border-b text-xs text-muted-foreground">
            <th className="px-4 py-2 text-left font-medium">Permission</th>
            {ROLES.map((r) => (
              <th key={r} className="px-2 py-2 text-center font-medium capitalize">
                {r}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {perms.map((p) => (
            <tr key={p} className="border-b last:border-0 hover:bg-accent/30">
              <td className="px-4 py-1.5 font-mono text-xs">{p}</td>
              {ROLES.map((r) => (
                <td key={r} className="px-2 py-1.5 text-center">
                  {roles[r]?.includes(p) ? <Check className="mx-auto size-4 text-ok" aria-label="yes" /> : <Minus className="mx-auto size-3.5 text-muted-foreground/40" aria-label="no" />}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
