import { useCustom, useInvalidate } from '@refinedev/core'
import { Activity, ArrowRight, Clock, Gamepad2, HeartPulse, Landmark, Radio, RefreshCw, ScrollText, UserPlus, Users } from 'lucide-react'
import { Link } from 'react-router'
import { BarChart } from '@/components/bars'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { count } from '@/lib/format'
import { Card, CurrencyTabs, DataTable, Faint, Kpi, Mono, Note, PageHeader, Pill, Segmented, When, useCur, useMe, useMoney, useParam, usePerm } from '../ui'
import type { Health, SystemInfo } from './system'

type Stats = { total: number; new_24h: number; new_7d: number; new_30d: number; active_24h: number; active_7d: number; active_30d: number }
type Dashboard = {
  online: number
  currencies: Record<string, { system: Record<string, number>; rewards_paid: number }>
  players: Stats
  signups: { day: string; count: number }[]
  adjustments: Record<string, number>
  games: { total: number; enabled: number }
}
type AuditRow = { id: number; admin_id: number | null; action: string; target: string; reason: string | null; created_at: string }

const REFRESH_MS = 15_000
const RANGES = ['7', '30', '90'] as const
// System accounts in the order an operator reads them; any other the ledger adds goes after.
const ACCOUNTS = ['house', 'rewards', 'sales', 'adjust', 'pot', 'exchange', 'inflow', 'faucet', 'grant', 'floor']
const LABEL: Record<string, string> = { house: 'House', rewards: 'Rewards', sales: 'Sales', adjust: 'Adjustments' }
const label = (a: string) => LABEL[a] ?? a[0].toUpperCase() + a.slice(1)
const pct = (n: number, of: number) => (of ? `${Math.round((n / of) * 100)}%` : '—')

// The platform at a glance: who's online, players and sign-ups, what waits for an approver, the
// treasury per currency (the ledger's own system accounts), the latest console activity and the
// dependencies' health.
export function Dashboard() {
  const [range, setRange] = useParam('days')
  const days = (RANGES as readonly string[]).includes(range) ? range : '30'
  const invalidate = useInvalidate()
  const { query } = useCustom<Dashboard>({ url: '/api/dashboard', method: 'get', config: { query: { days } }, queryOptions: { refetchInterval: REFRESH_MS } })
  const d = query.data?.data
  const loading = query.isLoading
  const p = d?.players
  const pending = d?.adjustments.pending_approval ?? 0

  return (
    <>
      <PageHeader
        title="Dashboard"
        description={
          <span className="flex items-center gap-2">
            <span className="size-1.5 animate-pulse rounded-full bg-ok" />
            Live · refreshes every {REFRESH_MS / 1000} s{query.dataUpdatedAt ? ` · updated ${new Date(query.dataUpdatedAt).toLocaleTimeString()}` : ''}
          </span>
        }
        actions={
          <Button variant="outline" size="sm" onClick={() => void invalidate({ invalidates: ['all'] })} disabled={query.isFetching}>
            <RefreshCw className={query.isFetching ? 'animate-spin' : ''} /> Refresh
          </Button>
        }
      />
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
        <Kpi label="Online now" icon={Radio} tone="ok" loading={loading} value={d ? count(d.online) : '—'} hint={p ? `${pct(d!.online, p.total)} of all players` : undefined} to="/online" />
        <Kpi label="Players" icon={Users} tone="brand" loading={loading} value={p ? count(p.total, true) : '—'} hint={p && `+${count(p.new_7d)} this week`} to="/players" />
        <Kpi label="New today" icon={UserPlus} tone="brand" loading={loading} value={p ? count(p.new_24h) : '—'} hint={p && `${count(p.new_30d)} in 30 days`} />
        <Kpi label="Active · 24 h" icon={Activity} tone="brand" loading={loading} value={p ? count(p.active_24h) : '—'} hint={p && `${count(p.active_7d)} in 7 d · ${count(p.active_30d)} in 30 d`} />
        <Kpi
          label="Waiting approval"
          icon={Clock}
          tone={pending ? 'warn' : 'neutral'}
          loading={loading}
          value={d ? count(pending) : '—'}
          hint={d && `${count(d.adjustments.failed ?? 0)} failed adjustments`}
          to="/adjustments?status=pending_approval"
        />
        <Kpi
          label="Games on"
          icon={Gamepad2}
          tone={d && d.games.enabled < d.games.total ? 'warn' : 'neutral'}
          loading={loading}
          value={d ? `${d.games.enabled} / ${d.games.total}` : '—'}
          hint={d && (d.games.enabled < d.games.total ? `${d.games.total - d.games.enabled} turned off` : 'all on')}
          to="/games"
        />
      </div>

      <div className="mt-4 grid gap-4 xl:grid-cols-3">
        <Card
          className="xl:col-span-2"
          title="Sign-ups"
          icon={UserPlus}
          actions={<Segmented value={days} onChange={(v) => setRange(v === '30' ? null : v)} options={RANGES.map((r) => ({ value: r, label: `${r} d` }))} />}
        >
          {d ? (
            <>
              <div className="mb-4 flex items-baseline gap-2">
                <span className="num text-2xl font-semibold">{count(d.signups.reduce((s, x) => s + x.count, 0))}</span>
                <span className="text-xs text-muted-foreground">new players in the last {days} days (UTC)</span>
              </div>
              <BarChart
                unit="sign-ups"
                bars={d.signups.map((s) => {
                  const day = new Date(s.day)
                  return {
                    label: day.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' }),
                    full: day.toLocaleDateString(undefined, { weekday: 'short', year: 'numeric', month: 'short', day: 'numeric', timeZone: 'UTC' }),
                    value: s.count,
                  }
                })}
              />
            </>
          ) : (
            <Skeleton className="h-[230px] w-full" />
          )}
        </Card>
        <RecentActivity />
      </div>

      <div className="mt-4 grid gap-4 xl:grid-cols-3">
        <Treasury d={d} loading={loading} />
        <HealthCard />
      </div>
    </>
  )
}

function Treasury({ d, loading }: { d?: Dashboard; loading: boolean }) {
  const currencies = useMe()?.currencies ?? []
  const [cur, setCur] = useCur()
  const { money } = useMoney()
  const c = d?.currencies[cur]
  const sys = c?.system
  const names = sys ? [...ACCOUNTS.filter((a) => a in sys), ...Object.keys(sys).filter((a) => !ACCOUNTS.includes(a)).sort()] : undefined
  const house = sys?.house ?? 0
  return (
    <Card
      className="xl:col-span-2"
      title="Treasury"
      icon={Landmark}
      bodyClassName="p-0"
      actions={
        <>
          {currencies.length > 1 && <CurrencyTabs value={cur} onChange={setCur} currencies={currencies} />}
          <Link to="/treasury" className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
            Open <ArrowRight className="size-3" />
          </Link>
        </>
      }
    >
      <div className="grid grid-cols-3 divide-x border-b">
        {[
          ['House net', sys ? money(house, cur, false) : '—', house < 0 ? 'text-danger' : house > 0 ? 'text-ok' : ''],
          ['Rewards paid', c ? money(c.rewards_paid, cur, false) : '—', ''],
          ['Adjust net', sys ? money(sys.adjust ?? 0, cur, false) : '—', ''],
        ].map(([l, v, cls]) => (
          <div key={l} className="px-4 py-3">
            <div className="text-xs text-muted-foreground">
              {l} · {cur.toUpperCase()}
            </div>
            <div className={`num mt-0.5 truncate text-lg font-semibold ${cls}`}>{v}</div>
          </div>
        ))}
      </div>
      <DataTable
        className="rounded-none border-0 shadow-none"
        rows={names}
        loading={loading}
        rowKey={(a) => a}
        columns={[
          { key: 'account', title: 'System account', render: (a) => <span className="font-medium">{label(a)}</span> },
          { key: 'balance', title: 'Balance', align: 'right', render: (a) => <span className="num">{money(sys![a], cur)}</span> },
        ]}
      />
      <Note className="mt-0 px-4 py-3">Credits minus debits of each system account, from the ledger. House is what the house has won net; Adjustments is what admins credited net, sign flipped.</Note>
    </Card>
  )
}

function RecentActivity() {
  const { query } = useCustom<{ items: AuditRow[] }>({ url: '/api/audit', method: 'get', config: { query: { limit: 8 } }, queryOptions: { refetchInterval: REFRESH_MS } })
  const rows = query.data?.data.items
  return (
    <Card
      title="Recent console activity"
      icon={ScrollText}
      bodyClassName="p-0"
      actions={
        <Link to="/audit" className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
          All <ArrowRight className="size-3" />
        </Link>
      }
    >
      {!rows ? (
        <div className="space-y-3 p-4">
          {Array.from({ length: 5 }, (_, i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </div>
      ) : !rows.length ? (
        <p className="p-6 text-center text-sm text-muted-foreground">Nothing yet.</p>
      ) : (
        <ol className="divide-y">
          {rows.map((r) => (
            <li key={r.id}>
              <Link to={`/audit?target=${encodeURIComponent(r.target)}`} className="flex items-start gap-3 px-4 py-2.5 hover:bg-accent/40">
                <span className={`mt-1.5 size-2 shrink-0 rounded-full ${r.action === 'login.fail' ? 'bg-danger' : r.action.startsWith('adjust') ? 'bg-warn' : 'bg-brand'}`} />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2">
                    <span className="truncate font-mono text-xs">{r.action}</span>
                    <span className="ml-auto shrink-0 text-xs">
                      <When at={r.created_at} />
                    </span>
                  </span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {r.admin_id == null ? 'unknown admin' : `admin #${r.admin_id}`} · {r.target}
                    {r.reason ? ` · ${r.reason}` : ''}
                  </span>
                </span>
              </Link>
            </li>
          ))}
        </ol>
      )}
    </Card>
  )
}

const DEPS: Record<string, string> = { postgres: 'Postgres', realtime: 'Realtime', ledger: 'Ledger' }

export function HealthRow({ name, h }: { name: string; h: Health }) {
  return (
    <div className="flex items-center gap-3">
      <span className={`size-2 shrink-0 rounded-full ${h.ok ? 'bg-ok' : 'bg-danger'}`} />
      <span className="text-sm font-medium">{DEPS[name] ?? name}</span>
      <span className="ml-auto flex items-center gap-2">
        <Mono>{h.ms.toFixed(1)} ms</Mono>
        <Pill tone={h.ok ? 'ok' : 'danger'}>{h.ok ? 'Healthy' : 'Down'}</Pill>
      </span>
    </div>
  )
}

function HealthCard() {
  const canView = usePerm('view')
  const { query } = useCustom<SystemInfo>({ url: '/api/system', method: 'get', queryOptions: { enabled: canView, refetchInterval: 30_000 }, errorNotification: false })
  const s = query.data?.data
  return (
    <Card
      title="System health"
      icon={HeartPulse}
      actions={
        <Link to="/system" className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
          Details <ArrowRight className="size-3" />
        </Link>
      }
    >
      {!s ? (
        <div className="space-y-3">
          {Array.from({ length: 3 }, (_, i) => (
            <Skeleton key={i} className="h-6 w-full" />
          ))}
        </div>
      ) : (
        <div className="space-y-3">
          {Object.entries(s.health)
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([n, h]) => (
              <HealthRow key={n} name={n} h={h} />
            ))}
          <div className="grid grid-cols-2 gap-2 border-t pt-3 text-xs">
            <span className="text-muted-foreground">Version</span>
            <span className="truncate text-right font-mono">{s.version}</span>
            <span className="text-muted-foreground">Up since</span>
            <span className="text-right">
              <When at={s.started_at} />
            </span>
            <span className="text-muted-foreground">DB pool in use</span>
            <span className="num text-right">
              {s.pool.in_use} / {s.pool.max}
            </span>
            <span className="text-muted-foreground">RPCs</span>
            <span className="num text-right">{s.rpcs.length || <Faint />}</span>
          </div>
        </div>
      )}
    </Card>
  )
}
