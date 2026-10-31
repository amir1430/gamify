import { useGo } from '@refinedev/core'
import { SearchBox, useLiveParam } from '@/components/list'
import { Avatar, Copyable, DataTable, ExportButton, PageHeader, Presence, Toolbar, When, downloadCSV, usePaged } from '../ui'

export type Player = { id: number; username: string; display_name: string; created_at: string; last_login_at: string | null; connections?: number | null }

export const playerName = (u: Player) => u.display_name || u.username || `Player #${u.id}`

// Players, newest first; ?q= searches as you type (auth.Users.Search: ID, username, display name, a login's
// subject). A row opens the player.
export function Players() {
  const go = useGo()
  const [text, setText, q] = useLiveParam('q', 300)
  const { rows, loading, more } = usePaged<Player>('users', { q })

  return (
    <>
      <PageHeader title="Players" description="Newest first. Open one for logins, wallet, activity, seeds and history." />
      <Toolbar>
        <SearchBox className="sm:max-w-sm" value={text} onChange={setText} placeholder="ID, username, name or Telegram ID" />
        <div className="ml-auto">
          <ExportButton
            disabled={!rows?.length}
            onExport={() =>
              downloadCSV(
                'players',
                ['id', 'username', 'display_name', 'online_connections', 'joined', 'last_login'],
                (rows ?? []).map((u) => [u.id, u.username, u.display_name, u.connections ?? '', u.created_at, u.last_login_at]),
              )
            }
          />
        </div>
      </Toolbar>
      <DataTable
        rows={rows}
        loading={loading}
        rowKey={(u) => u.id}
        empty={q ? 'No player matches.' : 'No players yet.'}
        onRowClick={(u) => go({ to: `/players/${u.id}`, type: 'push' })}
        columns={[
          {
            key: 'player',
            title: 'Player',
            render: (u) => (
              <span className="flex items-center gap-3">
                <Avatar name={playerName(u)} id={u.id} size="sm" />
                <span className="min-w-0">
                  <span className="block truncate font-medium">{u.display_name || '—'}</span>
                  {u.username && <span className="block truncate text-xs text-muted-foreground">@{u.username}</span>}
                </span>
              </span>
            ),
          },
          { key: 'id', title: 'ID', render: (u) => <Copyable value={u.id} /> },
          { key: 'status', title: 'Status', render: (u) => <Presence connections={u.connections} /> },
          { key: 'joined', title: 'Joined', render: (u) => <When at={u.created_at} /> },
          { key: 'seen', title: 'Last login', render: (u) => <When at={u.last_login_at} /> },
        ]}
      />
      {more}
    </>
  )
}
