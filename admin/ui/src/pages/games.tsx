import { useList } from '@refinedev/core'
import { Pencil } from 'lucide-react'
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { parseAmount, plain } from '@/lib/money'
import { ChangeDialog, DataTable, Faint, Field, Note, OnOff, PageHeader, Pill, Sub, useAction, useMe, useMoney, usePerm } from '../ui'

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

// Every game as a card: its on/off switch, then per currency the whole game and each variant with
// the stored override and the limits in effect. Overrides can only tighten the code caps.
export function Games() {
  const { result, query } = useList<Game>({ resource: 'games', pagination: { mode: 'off' } })
  return (
    <>
      <PageHeader
        title="Games"
        description="Limits in effect per currency, from the code caps and the stored overrides. An override can tighten a cap, never loosen it."
      />
      {query.isLoading && <Skeleton className="h-48 w-full rounded-lg" />}
      {!query.isLoading && !result.data.length && <Note>No games registered.</Note>}
      <div className="grid gap-6">
        {result.data.map((g) => (
          <GameCard key={g.name} game={g} />
        ))}
      </div>
    </>
  )
}

function GameCard({ game: g }: { game: Game }) {
  const currencies = (useMe()?.currencies ?? []).filter((c) => !g.only || c.code === g.only)
  const { money } = useMoney()
  const canEdit = usePerm('limits')
  const { busy, run } = useAction()
  const [editing, setEditing] = useState<{ currency: string; row: Row } | null>(null)

  return (
    <section className="rounded-xl border bg-card shadow-xs">
      <header className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3">
        <div className="flex flex-wrap items-center gap-2.5">
          <h2 className="font-semibold">{g.name}</h2>
          <OnOff on={g.enabled} />
          {g.pvp && <Pill>PvP</Pill>}
          {g.only && <Pill>{g.only.toUpperCase()} only</Pill>}
        </div>
        {canEdit && (
          <label className="flex items-center gap-2 text-sm text-muted-foreground">
            Open
            <Switch
              checked={g.enabled}
              disabled={busy}
              onCheckedChange={(enabled) => void run(`/api/games/${encodeURIComponent(g.name)}`, { method: 'PUT', body: { enabled } })}
            />
          </label>
        )}
      </header>
      <div className="grid gap-4 p-4">
        {currencies.map(({ code }) => {
          const eff = g.effective[code]
          const ovr = g.overrides.limits[code] ?? {}
          const rows: Row[] = ['', ...g.variants].map((v) => ({ variant: v, override: ovr[v], effective: v ? (eff?.variants?.[v] ?? eff?.limits) : eff?.limits }))
          const cell = (r: Row, f: (typeof FIELDS)[number]) => (
            <>
              <span className="num">{r.effective ? money(r.effective[f.key], code, false) : '—'}</span>
              {r.override?.[f.key] != null && <Sub>override {money(r.override[f.key]!, code, false)}</Sub>}
              {'cap' in f && <Sub>cap {money(g[f.cap], code, false)}</Sub>}
            </>
          )
          return (
            <div key={code}>
              <div className="mb-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">{code}</div>
              <DataTable
                rows={rows}
                rowKey={(r) => r.variant}
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
            </div>
          )
        })}
      </div>
      {editing && <EditLimit game={g} {...editing} onClose={() => setEditing(null)} />}
    </section>
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
