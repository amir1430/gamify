import { useCustom, useGo } from '@refinedev/core'
import { Plug, Radio, RefreshCw, Search } from 'lucide-react'
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { count } from '@/lib/format'
import { Avatar, Copyable, DataTable, ExportButton, Kpi, PageHeader, Presence, Toolbar, When, downloadCSV, useParam } from '../ui'
import { playerName, type Player } from './players'

type Online = { total: number; connections: number; items: (Player & { connections: number })[] }

const REFRESH_MS = 10_000
const LIMIT = 500

// Who's connected right now (Nakama's sessions view): players with at least one open realtime
// connection, newest account first, refreshed every 10 s. The filter searches the loaded rows.
export function OnlinePage() {
  const go = useGo()
  const [q, setQ] = useParam('q')
  const [text, setText] = useState(q)
  const { query } = useCustom<Online>({ url: '/api/online', method: 'get', config: { query: { limit: LIMIT } }, queryOptions: { refetchInterval: REFRESH_MS } })
  const d = query.data?.data
  const needle = q.trim().toLowerCase().replace(/^@/, '')
  const rows = d?.items.filter((u) => !needle || String(u.id) === needle || u.username.toLowerCase().includes(needle) || u.display_name.toLowerCase().includes(needle))

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
        <form
          className="relative w-full max-w-sm"
          onSubmit={(e) => {
            e.preventDefault()
            setQ(text.trim() || null)
          }}
        >
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="pl-8"
            type="search"
            placeholder="Filter by ID, username or name"
            value={text}
            onChange={(e) => (setText(e.target.value), setQ(e.target.value.trim() || null))}
            aria-label="Filter"
          />
        </form>
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
      <DataTable
        rows={rows}
        loading={query.isLoading}
        rowKey={(u) => u.id}
        empty={needle ? 'No online player matches.' : 'Nobody is online.'}
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
      {d && d.total > d.items.length && (
        <p className="mt-3 text-xs text-muted-foreground">
          Showing the {d.items.length.toLocaleString()} newest accounts of {d.total.toLocaleString()} online.
        </p>
      )}
    </>
  )
}
