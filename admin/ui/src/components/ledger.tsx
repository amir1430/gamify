// Ledger history (GET /api/treasury/{currency}/{account}, /api/users/{id}/wallet): one account's
// transfers newest first, filtered by kind and time, with the posted balance after each when unfiltered.
import { Link } from 'react-router'
import { Chips, FilterBy, RangeFilter, useRange } from '@/components/list'
import { Choice, Copyable, DataTable, ExportButton, Faint, Mono, Pill, Signed, Toolbar, When, downloadCSV, useMoney, usePaged, useParam } from '../ui'

export type Entry = {
  id: string
  at: string
  kind: string
  game?: number
  game_name?: string
  op: string
  stage: 'posted' | 'held' | 'settled' | 'released'
  amount: number
  posted: number
  other: { user?: number; system?: string }
  balance?: number
}

// System accounts in the order a treasurer reads them, with what each is for.
export const ACCOUNTS: { name: string; label: string; about: string }[] = [
  { name: 'house', label: 'House', about: 'Game charges in, payouts out: what the house has won net.' },
  { name: 'pot', label: 'Pots', about: 'PvP entries waiting for a winner.' },
  { name: 'rewards', label: 'Rewards', about: 'Promotions and prizes paid to players (goes negative).' },
  { name: 'sales', label: 'Sales', about: 'What players spent in the shop: extra lives, items.' },
  { name: 'adjust', label: 'Adjustments', about: 'Console credits out and debits in.' },
  { name: 'inflow', label: 'Deposits', about: 'Confirmed deposits enter the ledger from here (goes negative).' },
  { name: 'exchange', label: 'Exchange', about: 'Currency swaps pass through here.' },
  { name: 'grant', label: 'Welcome grants', about: 'Welcome amounts given on sign-up (goes negative).' },
  { name: 'floor', label: 'Play-only floor', about: 'Welcome amounts pass through and straight back: keeps grants play-only.' },
  { name: 'faucet', label: 'Faucet', about: 'Development top-ups (goes negative).' },
]
export const accountLabel = (n: string) => ACCOUNTS.find((a) => a.name === n)?.label ?? n[0].toUpperCase() + n.slice(1)

const KIND_LABEL: Record<string, string> = { top_up: 'Top-up', adjustment: 'Adjustment' }
export const kindLabel = (k: string) => KIND_LABEL[k] ?? k[0].toUpperCase() + k.slice(1).replace(/_/g, ' ')

const STAGE: Record<Entry['stage'], { label: string; tone: 'ok' | 'warn' | 'neutral' | 'brand' }> = {
  posted: { label: 'Posted', tone: 'ok' },
  held: { label: 'Held', tone: 'warn' },
  settled: { label: 'Settled hold', tone: 'brand' },
  released: { label: 'Released hold', tone: 'neutral' },
}

function Other({ o }: { o: Entry['other'] }) {
  if (o.user)
    return (
      <Link to={`/players/${o.user}`} className="text-brand hover:underline" onClick={(e) => e.stopPropagation()}>
        Player #{o.user}
      </Link>
    )
  if (o.system)
    return (
      <Link to={`/treasury?account=${o.system}`} className="text-muted-foreground hover:text-foreground hover:underline" onClick={(e) => e.stopPropagation()}>
        {accountLabel(o.system)}
      </Link>
    )
  return <Faint />
}

// path: the API resource (treasury/usd/house, users/5/wallet); params: extra query (currency).
export function LedgerHistory({ path, currency, kinds, params = {}, csv }: { path: string; currency: string; kinds: string[]; params?: Record<string, string>; csv: string }) {
  const { money } = useMoney()
  const [kind, setKind] = useParam('kind')
  const range = useRange()
  const { rows, loading, error, more } = usePaged<Entry>(path, { ...params, kind, since: range.since, until: range.until }, { quiet: true })
  const running = !kind && !range.until
  const chips = [
    kind && { key: 'kind', label: <>Kind: {kindLabel(kind)}</>, onRemove: () => setKind(null) },
    range.range && { key: 'range', label: range.label, onRemove: () => range.set('') },
  ].filter((c) => !!c) as { key: string; label: React.ReactNode; onRemove: () => void }[]

  if (error) return <p className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">{error.message}</p>
  return (
    <>
      <Toolbar>
        <Choice
          label="Kind"
          value={kind}
          onChange={(v) => setKind(v || null)}
          options={[{ value: '', label: 'Every kind' }, ...kinds.map((k) => ({ value: k, label: kindLabel(k) }))]}
        />
        <RangeFilter r={range} />
        <div className="ml-auto">
          <ExportButton
            disabled={!rows?.length}
            onExport={() =>
              downloadCSV(
                csv,
                ['at', 'kind', 'game', 'operation', 'stage', 'amount_minor', 'amount', 'balance', 'other', 'transfer_id'],
                (rows ?? []).map((e) => [
                  e.at,
                  e.kind,
                  e.game_name || e.game || '',
                  e.op,
                  e.stage,
                  e.amount,
                  money(e.amount, currency, false).replace(/,/g, ''),
                  e.balance == null ? '' : money(e.balance, currency, false).replace(/,/g, ''),
                  e.other.user ? `user:${e.other.user}` : (e.other.system ?? ''),
                  e.id,
                ]),
              )
            }
          />
        </div>
      </Toolbar>
      <Chips items={chips} onClear={() => (setKind(null), range.set(''))} />
      <DataTable
        rows={rows}
        loading={loading}
        rowKey={(e) => e.id}
        empty={chips.length ? 'No transfer matches these filters.' : 'No transfers yet.'}
        columns={[
          { key: 'at', title: 'When', render: (e) => <When at={e.at} /> },
          {
            key: 'kind',
            title: 'Kind',
            render: (e) => (
              <FilterBy on={() => setKind(e.kind)} title="Only this kind">
                <Pill>{kindLabel(e.kind)}</Pill>
              </FilterBy>
            ),
          },
          { key: 'game', title: 'Game', render: (e) => (e.game ? <span className="text-sm">{e.game_name || <Mono>#{e.game}</Mono>}</span> : <Faint />) },
          { key: 'other', title: 'Other side', render: (e) => <Other o={e.other} /> },
          { key: 'stage', title: 'Stage', render: (e) => <Pill tone={STAGE[e.stage].tone}>{STAGE[e.stage].label}</Pill> },
          {
            key: 'amount',
            title: 'Amount',
            align: 'right',
            render: (e) => (
              <span className={e.posted === 0 ? 'opacity-60' : ''} title={e.posted === 0 ? 'A hold or its release: the posted balance is unchanged' : undefined}>
                <Signed amount={e.amount}>
                  {e.amount > 0 ? '+' : ''}
                  {money(e.amount, currency, false)}
                </Signed>
              </span>
            ),
          },
          ...(running ? [{ key: 'bal', title: 'Balance', align: 'right' as const, render: (e: Entry) => <span className="num text-muted-foreground">{e.balance == null ? '—' : money(e.balance, currency, false)}</span> }] : []),
          { key: 'op', title: 'Operation', render: (e) => <Copyable value={e.op} label={e.op.length > 10 ? `${e.op.slice(0, 4)}…${e.op.slice(-4)}` : e.op} /> },
        ]}
      />
      {more}
      {!running && rows && rows.length > 0 && <p className="mt-2 text-xs text-muted-foreground">Running balance shows on unfiltered lists only.</p>}
    </>
  )
}
