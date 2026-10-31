import { useCustom, useGo } from '@refinedev/core'
import { Plug, Radio, RefreshCw } from 'lucide-react'
import { Chips, FilterMenu, Pager, SearchBox, SortMenu, listChips, useClientList } from '@/components/list'
import { Button } from '@/components/ui/button'
import { count } from '@/lib/format'
import { by, type FilterGroup, type Sort } from '@/lib/list'
import { Avatar, Copyable, DataTable, ExportButton, Kpi, PageHeader, Presence, Toolbar, When, downloadCSV } from '../ui'
import { playerName, type Player } from './players'

type Online = { total: number; connections: number; items: (Player & { connections: number })[] }

type Row = Player & { connections: number }
const HOUR = 3600_000
const GROUPS: FilterGroup<Row>[] = [
  { key: 'conns', label: 'Connections', options: [{ value: '1', label: 'One' }, { value: 'many', label: 'Two or more' }], match: (u, v) => (v === '1' ? u.connections === 1 : u.connections > 1) },
  {
    key: 'joined',
    label: 'Account',
    options: [
      { value: 'day', label: 'Joined in the last 24 hours' },
      { value: 'week', label: 'Joined in the last 7 days' },
    ],
    match: (u, v) => Date.now() - Date.parse(u.created_at) < (v === 'day' ? 24 : 168) * HOUR,
  },
  { key: 'named', label: 'Profile', options: [{ value: 'username', label: 'Has a username' }, { value: 'none', label: 'No username' }], match: (u, v) => !!u.username === (v === 'username') },
]
const SORTS: Sort<Row>[] = [
  { value: 'new', label: 'Newest account', compare: by((u) => u.id, true) },
  { value: 'conns', label: 'Most connections', compare: by((u) => u.connections, true) },
  { value: 'login', label: 'Latest login', compare: by((u) => u.last_login_at ?? '', true) },
  { value: 'name', label: 'Name A–Z', compare: by((u) => playerName(u)) },
]

const REFRESH_MS = 10_000
const LIMIT = 500

// Who's connected right now (Nakama's sessions view): players with at least one open realtime
// connection, newest account first, refreshed every 10 s. The filter searches the loaded rows.
export function OnlinePage() {
  const go = useGo()
  const { query } = useCustom<Online>({ url: '/api/online', method: 'get', config: { query: { limit: LIMIT } }, queryOptions: { refetchInterval: REFRESH_MS } })
  const d = query.data?.data
  const list = useClientList('online', d?.items, { text: (u) => [u.id, u.username, u.display_name], groups: GROUPS, sorts: SORTS }, 48)
  const rows = list.filtered

  return (
    <>
      <PageHeader
        title="Online now"
        description={`Players with an open realtime connection. Refreshes every ${REFRESH_MS / 1000} s.`}
        actions={
          <Button variant="outline" size="sm" onClick={() => void query.refetch()} disabled={query.isFetching}>
            <RefreshCw className={query.isFetching ? 'animate-spin' : ''} /> Refresh
          </Button>
        }
      />
      <div className="mb-6 grid grid-cols-2 gap-3 md:max-w-xl">
        <Kpi label="Players online" icon={Radio} tone="ok" loading={query.isLoading} value={d ? count(d.total) : '—'} />
        <Kpi
          label="Open connections"
          icon={Plug}
          tone="brand"
          loading={query.isLoading}
          value={d ? count(d.connections) : '—'}
          hint={d && d.total ? `${(d.connections / d.total).toFixed(2)} per player` : undefined}
        />
      </div>
      <Toolbar>
        <SearchBox value={list.text} onChange={list.setText} placeholder="Filter by ID, username or name" />
        <FilterMenu groups={GROUPS} values={list.filters} onChange={list.setFilter} onClear={list.clear} />
        <SortMenu sorts={SORTS} value={list.sort} onChange={list.setSort} />
        <div className="ml-auto">
          <ExportButton
            disabled={!rows?.length}
            onExport={() =>
              downloadCSV(
                'online',
                ['id', 'username', 'display_name', 'connections', 'joined', 'last_login'],
                (rows ?? []).map((u) => [u.id, u.username, u.display_name, u.connections, u.created_at, u.last_login_at]),
              )
            }
          />
        </div>
      </Toolbar>
      <Chips items={listChips(list, GROUPS)} onClear={list.clear} />
      <DataTable
        rows={list.page.rows}
        loading={query.isLoading}
        rowKey={(u) => u.id}
        empty={list.filtering ? 'No online player matches.' : 'Nobody is online.'}
        onRowClick={(u) => go({ to: `/players/${u.id}`, type: 'push' })}
        columns={[
          {
            key: 'player',
            title: 'Player',
            render: (u) => (
              <span className="flex items-center gap-3">
                <Avatar name={playerName(u)} id={u.id} size="sm" />
                <span className="min-w-0">
                  <span className="block truncate font-medium">{playerName(u)}</span>
                  {u.username && <span className="block truncate text-xs text-muted-foreground">@{u.username}</span>}
                </span>
              </span>
            ),
          },
          { key: 'id', title: 'ID', render: (u) => <Copyable value={u.id} /> },
          { key: 'conn', title: 'Status', render: (u) => <Presence connections={u.connections} /> },
          { key: 'login', title: 'Last login', render: (u) => <When at={u.last_login_at} /> },
          { key: 'joined', title: 'Joined', render: (u) => <When at={u.created_at} /> },
        ]}
      />
      <Pager page={list.page} total={list.filtered.length} noun={['player', 'players']} size={list.size} onPage={list.setPage} onSize={list.setSize} />
      {d && d.total > d.items.length && (
        <p className="mt-3 text-xs text-muted-foreground">
          Showing the {d.items.length.toLocaleString()} newest accounts of {d.total.toLocaleString()} online.
        </p>
      )}
    </>
  )
}
