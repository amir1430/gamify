import { useGo } from '@refinedev/core'
import { Search } from 'lucide-react'
import { useState } from 'react'
import { Input } from '@/components/ui/input'
import { Copyable, DataTable, PageHeader, Sub, Toolbar, When, useParam, usePaged } from '../ui'

export type Player = { id: number; username: string; display_name: string; created_at: string; last_login_at: string | null }

export const playerName = (u: Player) => u.display_name || u.username || `Player #${u.id}`

// Players, newest first; ?q= searches (auth.Users.Search: ID, username, display name). A row opens
// the player.
export function Players() {
  const go = useGo()
  const [q, setQ] = useParam('q')
  const [text, setText] = useState(q)
  const { rows, loading, more } = usePaged<Player>('users', { q })

  return (
    <>
      <PageHeader title="Players" description="Newest first. Open one for identities, balances, bets and seeds." />
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
            placeholder="ID, username or name"
            value={text}
            onChange={(e) => (setText(e.target.value), !e.target.value && setQ(null))}
            aria-label="Search"
          />
        </form>
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
              <div className="font-medium">
                {u.display_name || '—'}
                {u.username && <Sub>@{u.username}</Sub>}
              </div>
            ),
          },
          { key: 'id', title: 'ID', render: (u) => <Copyable value={u.id} /> },
          { key: 'joined', title: 'Joined', render: (u) => <When at={u.created_at} /> },
          { key: 'seen', title: 'Last login', render: (u) => <When at={u.last_login_at} /> },
        ]}
      />
      {more}
    </>
  )
}
