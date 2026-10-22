import { useCustom } from '@refinedev/core'
import { DataTable, Note, PageHeader, Section, Stats, useMe, useMoney } from '../ui'

type Dashboard = { online: number; currencies: Record<string, { system: Record<string, number>; rewards_paid: number }> }

const REFRESH_MS = 15_000
// System accounts in the order an operator reads them; any other the ledger adds goes after.
const ACCOUNTS = ['house', 'rewards', 'sales', 'adjust', 'pot', 'exchange', 'inflow', 'faucet', 'grant', 'floor']
const LABEL: Record<string, string> = { house: 'House', rewards: 'Rewards', sales: 'Sales', adjust: 'Adjustments' }
const label = (a: string) => LABEL[a] ?? a[0].toUpperCase() + a.slice(1)

// The platform at a glance: who's online and, per currency, the system accounts' balances (the
// ledger's own numbers: house = what the house has won net, adjust = what admins credited net
// with the sign flipped).
export function Dashboard() {
  const currencies = useMe()?.currencies ?? []
  const { money } = useMoney()
  const { query } = useCustom<Dashboard>({ url: '/api/dashboard', method: 'get', queryOptions: { refetchInterval: REFRESH_MS } })
  const d = query.data?.data
  const updated = query.dataUpdatedAt

  return (
    <>
      <PageHeader
        title="Dashboard"
        description={
          <span className="flex items-center gap-2">
            <span className="size-1.5 animate-pulse rounded-full bg-ok" />
            Live · refreshes every {REFRESH_MS / 1000} s{updated ? ` · updated ${new Date(updated).toLocaleTimeString()}` : ''}
          </span>
        }
      />
      <Stats
        items={[
          { label: 'Players online', value: d ? d.online.toLocaleString() : '—', tone: 'ok' },
          ...currencies.flatMap(({ code }) => {
            const c = d?.currencies[code]
            return [
              { label: `House · ${code.toUpperCase()}`, value: c ? money(c.system.house ?? 0, code, false) : '—', tone: (c?.system.house ?? 0) < 0 ? ('danger' as const) : undefined },
              { label: `Rewards paid · ${code.toUpperCase()}`, value: c ? money(c.rewards_paid, code, false) : '—' },
              { label: `Adjust · ${code.toUpperCase()}`, value: c ? money(c.system.adjust ?? 0, code, false) : '—' },
            ]
          }),
        ]}
      />
      {currencies.map(({ code }) => {
        const sys = d?.currencies[code]?.system
        const names = sys ? [...ACCOUNTS.filter((a) => a in sys), ...Object.keys(sys).filter((a) => !ACCOUNTS.includes(a)).sort()] : undefined
        return (
          <Section key={code} title={`System balances · ${code.toUpperCase()}`}>
            <DataTable
              rows={names}
              loading={query.isLoading}
              rowKey={(a) => a}
              columns={[
                { key: 'account', title: 'Account', render: (a) => <span className="font-medium">{label(a)}</span> },
                { key: 'balance', title: 'Balance', align: 'right', render: (a) => <span className="num">{money(sys![a], code)}</span> },
              ]}
            />
          </Section>
        )
      })}
      <Note>Balances are credits minus debits of each system account, from the ledger.</Note>
    </>
  )
}
