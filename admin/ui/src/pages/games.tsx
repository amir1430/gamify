import { useGo, useList } from '@refinedev/core'
import { ChevronRight, Gamepad2, Pencil, Settings } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { Chips, FilterBy, FilterMenu, NoMatch, Pager, SearchBox, SortMenu, ViewToggle, listChips, useClientList, useViewMode } from '@/components/list'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { by, matchText, type FilterGroup, type Sort } from '@/lib/list'
import { parseAmount, plain } from '@/lib/money'
import { cn } from '@/lib/utils'
import {
  ChangeDialog,
  CurrencyTabs,
  DataTable,
  Facts,
  Faint,
  Field,
  Note,
  OnOff,
  PageHeader,
  Pill,
  Section,
  Sub,
  Toolbar,
  useAction,
  useMe,
  useMoney,
  useParam,
  usePerm,
} from '../ui'

type Limits = { enabled: boolean; min_amount: number; max_amount: number; max_payout: number }
type Override = { enabled: boolean; min_amount: number | null; max_amount: number | null; max_payout: number | null }
type Game = {
  name: string
  max_amount: number // code caps, every currency
  max_payout: number
  variants: string[]
  pvp: boolean
  only: string
  enabled: boolean
  overrides: { enabled: boolean | null; limits: Record<string, Record<string, Override>> }
  effective: Record<string, { limits: Limits; variants?: Record<string, Limits> }>
}

// One row of a game's table: the whole game ('') or a variant, in one currency.
type Row = { variant: string; override?: Override; effective?: Limits }

const FIELDS = [
  { key: 'min_amount', label: 'Min amount' },
  { key: 'max_amount', label: 'Max amount', cap: 'max_amount' },
  { key: 'max_payout', label: 'Max win', cap: 'max_payout' },
] as const

// Facts about a game the list shows, searches and filters on.
const overrideCount = (g: Game) => Object.values(g.overrides.limits).reduce((n, byVariant) => n + Object.keys(byVariant).length, 0) + (g.overrides.enabled != null ? 1 : 0)
const currenciesOf = (g: Game, all: string[]) => (g.only ? [g.only] : all)
const plural = (n: number, one: string, many = one + 's') => `${n.toLocaleString()} ${n === 1 ? one : many}`

// Every game, as cards (Coolify's project grid) or a table: searched by name, variant or currency,
// filtered by status, mode, stored overrides and currency, sorted, paged. A game opens its limits.
export function Games() {
  const { result, query } = useList<Game>({ resource: 'games', pagination: { mode: 'off' } })
  const all = (useMe()?.currencies ?? []).map((c) => c.code)
  const [view, setView] = useViewMode('games')
  const groups: FilterGroup<Game>[] = [
    { key: 'status', label: 'Status', options: [{ value: 'open', label: 'Open' }, { value: 'closed', label: 'Closed' }], match: (g, v) => g.enabled === (v === 'open') },
    { key: 'mode', label: 'Mode', options: [{ value: 'pvp', label: 'PvP' }, { value: 'solo', label: 'Against the house' }], match: (g, v) => g.pvp === (v === 'pvp') },
    { key: 'overrides', label: 'Overrides', options: [{ value: 'yes', label: 'Has overrides' }, { value: 'no', label: 'Code defaults only' }], match: (g, v) => overrideCount(g) > 0 === (v === 'yes') },
    { key: 'variants', label: 'Variants', options: [{ value: 'yes', label: 'Has variants' }, { value: 'no', label: 'No variants' }], match: (g, v) => g.variants.length > 0 === (v === 'yes') },
    ...(all.length > 1
      ? [{ key: 'cur', label: 'Currency', options: all.map((c) => ({ value: c, label: c.toUpperCase() })), match: (g: Game, v: string) => currenciesOf(g, all).includes(v) }]
      : []),
  ]
  const sorts: Sort<Game>[] = [
    { value: 'name', label: 'Name A–Z', compare: by((g) => g.name) },
    { value: '-name', label: 'Name Z–A', compare: by((g) => g.name, true) },
    { value: 'status', label: 'Closed first', compare: by((g) => (g.enabled ? 1 : 0)) },
    { value: 'variants', label: 'Most variants', compare: by((g) => g.variants.length, true) },
    { value: 'overrides', label: 'Most overrides', compare: by((g) => overrideCount(g), true) },
  ]
  const list = useClientList('games', result.data, { text: (g) => [g.name, ...g.variants, g.only, g.pvp ? 'pvp' : ''], groups, sorts })
  const games = result.data
  const open = games.filter((g) => g.enabled).length

  return (
    <>
      <PageHeader
        title="Games"
        meta={!query.isLoading && games.length > 0 && <Pill>{plural(games.length, 'game')}</Pill>}
        description={
          !query.isLoading && games.length > 0 ? (
            <>
              {open.toLocaleString()} open · {(games.length - open).toLocaleString()} closed. Open a game for its limits per currency and variant.
            </>
          ) : (
            'Open a game for its limits per currency and variant.'
          )
        }
      />
      {!query.isLoading && !games.length ? (
        <Note>No games registered.</Note>
      ) : (
        <>
          <Toolbar>
            <SearchBox value={list.text} onChange={list.setText} placeholder="Search name, variant or currency" />
            <FilterMenu groups={groups} values={list.filters} onChange={list.setFilter} onClear={list.clear} />
            <SortMenu sorts={sorts} value={list.sort} onChange={list.setSort} />
            <div className="ml-auto">
              <ViewToggle value={view} onChange={setView} />
            </div>
          </Toolbar>
          <Chips items={listChips(list, groups)} onClear={list.clear} />
          {query.isLoading ? (
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
              {Array.from({ length: 8 }, (_, i) => (
                <Skeleton key={i} className="h-32 rounded-xl" />
              ))}
            </div>
          ) : !list.filtered.length ? (
            <NoMatch noun="games" onClear={list.clear} />
          ) : view === 'grid' ? (
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
              {list.page.rows.map((g) => (
                <GameTile key={g.name} game={g} currencies={currenciesOf(g, all)} onFilter={list.toggle} />
              ))}
            </div>
          ) : (
            <GameTable rows={list.page.rows} all={all} onFilter={list.toggle} />
          )}
          <Pager page={list.page} total={list.filtered.length} noun={['game', 'games']} size={list.size} onPage={list.setPage} onSize={list.setSize} />
        </>
      )}
    </>
  )
}

const gamePath = (name: string) => `/games/${encodeURIComponent(name)}`

// Opens or closes a game from the list.
function OpenSwitch({ game: g }: { game: Game }) {
  const canEdit = usePerm('limits')
  const { busy, run } = useAction()
  if (!canEdit) return null
  return (
    <span onClick={(e) => e.stopPropagation()} className="flex items-center" title={g.enabled ? 'Open: switch off to stop new play' : 'Closed: switch on to open'}>
      <Switch
        checked={g.enabled}
        disabled={busy}
        aria-label={`${g.name} open`}
        onCheckedChange={(enabled) => void run(`/api/games/${encodeURIComponent(g.name)}`, { method: 'PUT', body: { enabled } })}
      />
    </span>
  )
}

// One game as a card: icon and name, then its counts and quick actions.
function GameTile({ game: g, currencies, onFilter }: { game: Game; currencies: string[]; onFilter: (key: string, v: string) => void }) {
  const go = useGo()
  const n = overrideCount(g)
  return (
    <div
      role="link"
      tabIndex={0}
      onClick={() => go({ to: gamePath(g.name), type: 'push' })}
      onKeyDown={(e) => e.key === 'Enter' && go({ to: gamePath(g.name), type: 'push' })}
      className="group flex cursor-pointer flex-col rounded-xl border bg-card shadow-xs transition-colors hover:border-brand/40 hover:bg-accent/20 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
      aria-label={`Open ${g.name}`}
    >
      <div className="flex min-w-0 items-center gap-3 px-4 pt-4 pb-3">
        <span className={cn('grid size-10 shrink-0 place-items-center rounded-lg border bg-muted/40', !g.enabled && 'opacity-50')}>
          <Gamepad2 className="size-5 text-muted-foreground" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="truncate font-semibold" title={g.name}>
            {g.name}
          </div>
          <div className="mt-1 flex flex-wrap gap-1.5">
            <FilterBy on={() => onFilter('status', g.enabled ? 'open' : 'closed')}>
              <OnOff on={g.enabled} />
            </FilterBy>
            {g.pvp && (
              <FilterBy on={() => onFilter('mode', 'pvp')}>
                <Pill>PvP</Pill>
              </FilterBy>
            )}
            {g.only && (
              <FilterBy on={() => onFilter('cur', g.only)}>
                <Pill>{g.only.toUpperCase()} only</Pill>
              </FilterBy>
            )}
          </div>
        </div>
      </div>
      <div className="mx-4 flex items-center gap-2 border-t py-2.5 text-sm">
        <span className="min-w-0 flex-1 truncate text-muted-foreground">
          <b className="font-medium text-foreground">{g.variants.length}</b> {g.variants.length === 1 ? 'variant' : 'variants'} ·{' '}
          <b className="font-medium text-foreground">{currencies.length}</b> {currencies.length === 1 ? 'currency' : 'currencies'}
          {n > 0 && (
            <>
              {' '}
              · <b className="font-medium text-brand">{n}</b> {n === 1 ? 'override' : 'overrides'}
            </>
          )}
        </span>
        <OpenSwitch game={g} />
        <Button asChild variant="ghost" size="icon" className="size-8 text-muted-foreground" onClick={(e) => e.stopPropagation()}>
          <Link to={gamePath(g.name)} aria-label={`${g.name} limits`} title="Limits">
            <Settings />
          </Link>
        </Button>
      </div>
    </div>
  )
}

function GameTable({ rows, all, onFilter }: { rows: Game[]; all: string[]; onFilter: (key: string, v: string) => void }) {
  const go = useGo()
  return (
    <DataTable
      rows={rows}
      rowKey={(g) => g.name}
      onRowClick={(g) => go({ to: gamePath(g.name), type: 'push' })}
      columns={[
        {
          key: 'name',
          title: 'Game',
          render: (g) => (
            <span className="flex items-center gap-2.5">
              <Gamepad2 className="size-4 text-muted-foreground" />
              <span className="font-medium">{g.name}</span>
            </span>
          ),
        },
        {
          key: 'status',
          title: 'Status',
          render: (g) => (
            <FilterBy on={() => onFilter('status', g.enabled ? 'open' : 'closed')}>
              <OnOff on={g.enabled} />
            </FilterBy>
          ),
        },
        {
          key: 'mode',
          title: 'Mode',
          render: (g) => (
            <FilterBy on={() => onFilter('mode', g.pvp ? 'pvp' : 'solo')}>
              <Pill>{g.pvp ? 'PvP' : 'House'}</Pill>
            </FilterBy>
          ),
        },
        { key: 'variants', title: 'Variants', align: 'right', render: (g) => <span className="num">{g.variants.length}</span> },
        {
          key: 'cur',
          title: 'Currencies',
          render: (g) => <span className="text-xs text-muted-foreground uppercase">{currenciesOf(g, all).join(', ')}</span>,
        },
        { key: 'ovr', title: 'Overrides', align: 'right', render: (g) => (overrideCount(g) ? <span className="num text-brand">{overrideCount(g)}</span> : <Faint>none</Faint>) },
        { key: 'open', align: 'right', render: (g) => <OpenSwitch game={g} /> },
        { key: 'go', align: 'right', className: 'w-8', render: () => <ChevronRight className="size-4 text-muted-foreground" /> },
      ]}
    />
  )
}

// /games/:name: a game's switch, then per currency (tabs) the whole game and each variant with the
// stored override and the limits in effect. Overrides can only tighten the code caps.
export function GamePage() {
  const { name = '' } = useParams()
  const { result, query } = useList<Game>({ resource: 'games', pagination: { mode: 'off' } })
  const g = result.data.find((x) => x.name === name)
  const crumbs = [{ label: 'Games', to: '/games' }, { label: name }]
  if (query.isLoading) return <Skeleton className="h-64 w-full rounded-xl" />
  if (!g)
    return (
      <>
        <PageHeader title={name} crumbs={crumbs} />
        <Note>No game named “{name}” is registered.</Note>
      </>
    )
  return <GameDetail game={g} crumbs={crumbs} />
}

function GameDetail({ game: g, crumbs }: { game: Game; crumbs: { label: string; to?: string }[] }) {
  const currencies = (useMe()?.currencies ?? []).filter((c) => !g.only || c.code === g.only)
  const [cur, setCur] = useParam('cur')
  const code = currencies.some((c) => c.code === cur) ? cur : (currencies[0]?.code ?? '')
  const [vq, setVq] = useParam('variant')
  const [changed, setChanged] = useParam('changed')
  const { money } = useMoney()
  const canEdit = usePerm('limits')
  const [editing, setEditing] = useState<{ currency: string; row: Row } | null>(null)

  const eff = g.effective[code]
  const ovr = g.overrides.limits[code] ?? {}
  const rows: Row[] = ['', ...g.variants]
    .map((v) => ({ variant: v, override: ovr[v], effective: v ? (eff?.variants?.[v] ?? eff?.limits) : eff?.limits }))
    .filter((r) => (!vq || !r.variant || matchText([r.variant], vq)) && (!changed || !r.variant || r.override))
  const cell = (r: Row, f: (typeof FIELDS)[number]) => (
    <>
      <span className="num">{r.effective ? money(r.effective[f.key], code, false) : '—'}</span>
      {r.override?.[f.key] != null && <Sub>override {money(r.override[f.key]!, code, false)}</Sub>}
      {'cap' in f && <Sub>cap {money(g[f.cap], code, false)}</Sub>}
    </>
  )

  return (
    <>
      <PageHeader
        title={g.name}
        crumbs={crumbs}
        meta={
          <>
            <OnOff on={g.enabled} />
            {g.pvp && <Pill>PvP</Pill>}
            {g.only && <Pill>{g.only.toUpperCase()} only</Pill>}
          </>
        }
        description="Limits in effect per currency, from the code caps and the stored overrides. An override can tighten a cap, never loosen it."
        actions={
          canEdit && (
            <label className="flex items-center gap-2 rounded-lg border px-3 py-1.5 text-sm text-muted-foreground">
              Open
              <OpenSwitch game={g} />
            </label>
          )
        }
      />
      <Facts
        items={[
          ['Max amount (code cap)', <span className="num">{currencies.map((c) => money(g.max_amount, c.code)).join(' · ') || '—'}</span>],
          ['Max win (code cap)', <span className="num">{currencies.map((c) => money(g.max_payout, c.code)).join(' · ') || '—'}</span>],
          ['Variants · overrides', `${g.variants.length} · ${overrideCount(g)}`],
        ]}
      />
      <Section
        title="Limits"
        description={g.variants.length ? 'The whole game first, then each variant (a variant without its own override follows the whole game).' : undefined}
        actions={currencies.length > 1 && <CurrencyTabs value={code} onChange={(c) => setCur(c === currencies[0]?.code ? null : c)} currencies={currencies} />}
      >
        {g.variants.length > 5 && (
          <Toolbar>
            <SearchBox value={vq} onChange={(s) => setVq(s || null)} placeholder={`Search ${g.variants.length} variants`} />
            <label className="flex items-center gap-2 text-sm text-muted-foreground">
              <Switch checked={!!changed} onCheckedChange={(c) => setChanged(c ? '1' : null)} />
              With an override only
            </label>
          </Toolbar>
        )}
        <DataTable
          rows={rows}
          rowKey={(r) => r.variant}
          empty="No variant matches."
          columns={[
            { key: 'v', title: 'Variant', render: (r) => (r.variant ? <span className="font-mono text-xs">{r.variant}</span> : <span className="font-medium">Whole game</span>) },
            {
              key: 'on',
              title: 'Status',
              render: (r) => (
                <>
                  <OnOff on={!!r.effective?.enabled} />
                  {r.override && !r.override.enabled && <Sub>off by override</Sub>}
                </>
              ),
            },
            ...FIELDS.map((f) => ({ key: f.key, title: f.label, align: 'right' as const, render: (r: Row) => cell(r, f) })),
            { key: 'stored', title: 'Override', render: (r) => (r.override ? <Pill tone="brand">stored</Pill> : <Faint>none</Faint>) },
            ...(canEdit
              ? [
                  {
                    key: 'edit',
                    align: 'right' as const,
                    render: (r: Row) => (
                      <Button variant="ghost" size="sm" onClick={() => setEditing({ currency: code, row: r })}>
                        <Pencil /> Edit
                      </Button>
                    ),
                  },
                ]
              : []),
          ]}
        />
      </Section>
      {editing && <EditLimit game={g} {...editing} onClose={() => setEditing(null)} />}
    </>
  )
}

// The override of one game, currency and variant: on/off and each limit, empty = no override (the
// cap, or the whole game's limit for a variant). Clear removes the stored row.
function EditLimit({ game, currency, row, onClose }: { game: Game; currency: string; row: Row; onClose: () => void }) {
  const { decimals, money } = useMoney()
  const d = decimals(currency)
  const o = row.override
  const [enabled, setEnabled] = useState(o?.enabled ?? true)
  const [vals, setVals] = useState<Record<string, string>>(() => Object.fromEntries(FIELDS.map((f) => [f.key, o?.[f.key] != null ? plain(o[f.key]!, d) : ''])))
  const { busy, run } = useAction()
  const path = `/api/games/${encodeURIComponent(game.name)}/limits/${encodeURIComponent(currency)}/${row.variant ? encodeURIComponent(row.variant) : '_'}`

  const parsed: Record<string, bigint | null> = {}
  const errors: Record<string, string> = {}
  for (const f of FIELDS) {
    const s = vals[f.key]
    if (!s) {
      parsed[f.key] = null
      continue
    }
    try {
      const v = parseAmount(s, d)
      if (v <= 0n) errors[f.key] = 'Must be above zero.'
      else if ('cap' in f && v > BigInt(game[f.cap])) errors[f.key] = `Above the cap of ${money(game[f.cap], currency)}.`
      parsed[f.key] = v
    } catch {
      errors[f.key] = d ? `A positive number with at most ${d} decimals.` : 'A positive whole number.'
    }
  }

  return (
    <ChangeDialog
      title={`${game.name} · ${currency.toUpperCase()} · ${row.variant || 'whole game'}`}
      description="Empty fields keep the default. Changes reach every server within seconds."
      busy={busy}
      disabled={Object.keys(errors).length > 0}
      onClose={onClose}
      onSubmit={async () => {
        if ((await run(path, { method: 'PUT', body: { enabled, ...parsed } })).ok) onClose()
      }}
      footer={
        <>
          {o && (
            <Button
              type="button"
              variant="ghost"
              className="mr-auto text-danger"
              disabled={busy}
              onClick={async () => (await run(path, { method: 'DELETE' })).ok && onClose()}
            >
              Clear override
            </Button>
          )}
          <Button type="button" variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" disabled={busy || Object.keys(errors).length > 0}>
            {busy ? 'Saving…' : 'Save'}
          </Button>
        </>
      }
    >
      <label className="flex items-center justify-between gap-4 rounded-lg border px-4 py-3">
        <span>
          <span className="block text-sm font-medium">Open</span>
          <span className="block text-xs text-muted-foreground">Off stops new play here; games in play can finish.</span>
        </span>
        <Switch checked={enabled} onCheckedChange={setEnabled} />
      </label>
      {FIELDS.map((f) => (
        <Field
          key={f.key}
          label={f.label}
          error={errors[f.key]}
          hint={
            <>
              In effect: {row.effective ? money(row.effective[f.key], currency) : '—'}
              {'cap' in f && <> · cap {money(game[f.cap], currency)}</>}
            </>
          }
        >
          <Input className="font-mono" inputMode="decimal" placeholder="default" value={vals[f.key]} onChange={(e) => setVals((v) => ({ ...v, [f.key]: e.target.value }))} />
        </Field>
      ))}
    </ChangeDialog>
  )
}
