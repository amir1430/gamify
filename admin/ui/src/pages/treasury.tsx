import { useCustom } from '@refinedev/core'
import { ArrowDownLeft, ArrowUpRight, Coins, Hourglass, Landmark, RefreshCw, Users, Wallet } from 'lucide-react'
import { ACCOUNTS, LedgerHistory, accountLabel } from '@/components/ledger'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { CurrencyTabs, Kpi, Note, PageHeader, Section, useCur, useMe, useMoney, useParam } from '../ui'

type Account = { name: string; debits_pending: number; debits_posted: number; credits_pending: number; credits_posted: number; net: number }
type Treasury = {
  history: boolean
  kinds: string[]
  currencies: Record<string, { accounts: Account[]; players_posted: number; held: number; rewards_paid: number }>
}

const REFRESH_MS = 30_000

// The ledger's own side, per currency: what players hold and have in play, each system account's
// totals, and any account's transfers (?account=) with a running balance. Totals refresh every 30 s.
export function TreasuryPage() {
  const currencies = useMe()?.currencies ?? []
  const [cur, setCur] = useCur()
  const [account, setAccount] = useParam('account')
  const { money } = useMoney()
  const { query } = useCustom<Treasury>({ url: '/api/treasury', method: 'get', queryOptions: { refetchInterval: REFRESH_MS } })
  const d = query.data?.data
  const c = d?.currencies[cur]
  const accounts = c ? [...ACCOUNTS.map((a) => c.accounts.find((x) => x.name === a.name)).filter((x) => !!x), ...c.accounts.filter((x) => !ACCOUNTS.some((a) => a.name === x.name))] : []
  const selected = accounts.find((a) => a.name === account) ?? accounts[0]
  const net = (n: string) => c?.accounts.find((a) => a.name === n)?.net ?? 0
  const m = (n: number) => money(n, cur, false)
  const loading = query.isLoading

  return (
    <>
      <PageHeader
        title="Treasury"
        description="The ledger's system accounts: every movement of money has one of them on the other side of a player. Figures are in minor units' display form and refresh every 30 s."
        actions={
          <>
            {currencies.length > 1 && <CurrencyTabs value={cur} onChange={setCur} currencies={currencies} />}
            <Button variant="outline" size="sm" onClick={() => void query.refetch()} disabled={query.isFetching}>
              <RefreshCw className={query.isFetching ? 'animate-spin' : ''} /> Refresh
            </Button>
          </>
        }
      />
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-3 2xl:grid-cols-6">
        <Kpi label={`Players hold · ${cur.toUpperCase()}`} icon={Users} tone="brand" loading={loading} value={c ? m(c.players_posted) : '—'} hint="Posted balances of every player" />
        <Kpi label="In play" icon={Hourglass} tone="warn" loading={loading} value={c ? m(c.held) : '—'} hint="On hold in open games and pots" />
        <Kpi label="House net" icon={Landmark} tone={net('house') < 0 ? 'danger' : 'ok'} loading={loading} value={c ? m(net('house')) : '—'} hint="Won from games, all time" />
        <Kpi label="Deposits in" icon={ArrowDownLeft} tone="ok" loading={loading} value={c ? m(-net('inflow')) : '—'} hint="Confirmed deposits, all time" />
        <Kpi label="Rewards paid" icon={ArrowUpRight} tone="neutral" loading={loading} value={c ? m(c.rewards_paid) : '—'} hint="Promotions and prizes, net" />
        <Kpi label="Sales" icon={Coins} tone="neutral" loading={loading} value={c ? m(net('sales')) : '—'} hint="Spent in the shop" />
      </div>

      <Section title="System accounts" description="Pick one for its transfers. Net is credits minus debits posted; pending is money on hold.">
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-5">
          {(loading ? ACCOUNTS.slice(0, 5).map((a) => ({ name: a.name }) as Account) : accounts).map((a) => {
            const about = ACCOUNTS.find((x) => x.name === a.name)?.about
            const on = selected?.name === a.name
            return (
              <button
                key={a.name}
                type="button"
                aria-pressed={on}
                onClick={() => setAccount(a.name === accounts[0]?.name ? null : a.name)}
                className={cn(
                  'flex flex-col rounded-xl border bg-card text-left shadow-xs transition-colors hover:border-brand/40 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none',
                  on && 'border-brand/60 ring-1 ring-brand/30',
                )}
              >
                <div className="flex items-center gap-3 px-4 pt-4 pb-3">
                  <span className="grid size-9 shrink-0 place-items-center rounded-lg border bg-muted/40">
                    <Wallet className="size-4 text-muted-foreground" />
                  </span>
                  <div className="min-w-0">
                    <div className="truncate font-semibold">{accountLabel(a.name)}</div>
                    <div className={cn('num truncate text-sm', a.net < 0 ? 'text-danger' : a.net > 0 ? 'text-ok' : 'text-muted-foreground')}>{a.net == null ? '…' : m(a.net)}</div>
                  </div>
                </div>
                <div className="mx-4 border-t py-2.5 text-xs text-muted-foreground">
                  {a.debits_posted == null ? (
                    about
                  ) : (
                    <>
                      <span className="num">in {m(a.credits_posted)}</span> · <span className="num">out {m(a.debits_posted)}</span>
                      {(a.credits_pending > 0 || a.debits_pending > 0) && (
                        <>
                          {' '}
                          · <span className="num text-warn">pending {m(a.credits_pending - a.debits_pending)}</span>
                        </>
                      )}
                      <div className="mt-1 line-clamp-2">{about}</div>
                    </>
                  )}
                </div>
              </button>
            )
          })}
        </div>
      </Section>

      {selected && d && (
        <Section title={`${accountLabel(selected.name)} · transfers`} description="Newest first. Click a kind to filter by it; the other side links to the player or account.">
          {d.history ? (
            <LedgerHistory key={`${cur}/${selected.name}`} path={`treasury/${cur}/${selected.name}`} currency={cur} kinds={d.kinds} csv={`treasury-${cur}-${selected.name}`} />
          ) : (
            <Note>This ledger driver can't read transfers (it has no ledger.TransferReader), so only the totals above show.</Note>
          )}
        </Section>
      )}
    </>
  )
}
