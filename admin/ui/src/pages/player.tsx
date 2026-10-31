import { useCustom, useShow } from '@refinedev/core'
import { Plug, Scale } from 'lucide-react'
import { useState } from 'react'
import { useParams } from 'react-router'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  ChangeDialog,
  Copyable,
  DataTable,
  Facts,
  Faint,
  Mono,
  Note,
  PageHeader,
  Pill,
  Section,
  Segmented,
  Signed,
  Stats,
  TabBar,
  When,
  dateTime,
  useAction,
  useMe,
  useMoney,
  usePaged,
  useParam,
  usePerm,
  useTab,
} from '../ui'
import { AdjustDialog, AdjustmentsTable } from './adjustments'
import { playerName, type Player as PlayerRow } from './players'
import { ViewTable, useViews } from './views'

type Identity = { provider: string; subject: string; linked_at: string }
type PlayerView = { user: PlayerRow; identities: Identity[]; balances: Record<string, number> }
type Activity = { game: string; id: number; currency: string; amount: number; payout: number; status: string; detail?: Record<string, unknown>; at: string }
type Seed = {
  id: number
  status: 'active' | 'next' | 'revealed'
  server_seed_hash: string
  client_seed: string
  nonce: number
  server_seed?: string
  created_at: string
  revealed_at: string | null
}

const TABS = ['profile', 'auth', 'wallet', 'activity', 'seeds'] as const

// One player (Nakama's account page): header with actions, then Profile, Authentication (linked
// identities, unlink), Wallet (balances, adjustments), Activity (per game) and Seeds (with fairness),
// then a tab per brand view with a UserFilter (e.g. Restrictions), filtered to this player.
export function Player() {
  const id = useParams().id!
  const playerViews = (useViews() ?? []).filter((v) => v.user_filter)
  const [tab, setTab] = useTab<string>([...TABS, ...playerViews.map((v) => `view-${v.name}`)])
  const shown = playerViews.find((v) => `view-${v.name}` === tab)
  const canIdentity = usePerm('identity')
  const canAdjust = usePerm('adjust')
  const { result: p, query } = useShow<PlayerView>({ resource: 'users', id })
  // 404 = the console runs without fairness: no Seeds tab.
  const seeds = usePaged<Seed>(`users/${id}/seeds`, {}, { quiet: true })
  const hasSeeds = !seeds.error
  const [dialog, setDialog] = useState<'disconnect' | 'adjust' | null>(null)
  const u = p?.user

  return (
    <>
      <PageHeader
        crumbs={[{ label: 'Players', to: '/players' }, { label: u ? playerName(u) : `#${id}` }]}
        title={u ? playerName(u) : `Player #${id}`}
        description={
          u && (
            <span className="flex flex-wrap items-center gap-x-4 gap-y-1">
              <Copyable value={u.id} label={`ID ${u.id}`} />
              {u.username && <span className="text-xs">@{u.username}</span>}
              <span className="text-xs">Joined {dateTime(u.created_at)}</span>
              <span className="text-xs">Last login {dateTime(u.last_login_at)}</span>
            </span>
          )
        }
        actions={
          u && (
            <>
              {canIdentity && (
                <Button variant="outline" onClick={() => setDialog('disconnect')}>
                  <Plug /> Disconnect
                </Button>
              )}
              {canAdjust && (
                <Button onClick={() => setDialog('adjust')}>
                  <Scale /> New adjustment
                </Button>
              )}
            </>
          )
        }
      />
      {query.isLoading && <Skeleton className="h-48 w-full rounded-lg" />}
      {query.isError && <Note className="text-danger">{query.error.message}</Note>}

      {p && u && (
        <>
          <TabBar
            value={tab}
            onChange={setTab}
            tabs={[
              { value: 'profile', label: 'Profile' },
              { value: 'auth', label: 'Authentication', count: p.identities.length },
              { value: 'wallet', label: 'Wallet' },
              { value: 'activity', label: 'Activity' },
              ...(hasSeeds ? [{ value: 'seeds', label: 'Seeds' }] : []),
              ...playerViews.map((v) => ({ value: `view-${v.name}`, label: v.title })),
            ]}
          />
          {tab === 'profile' && (
            <Facts
              items={[
                ['User ID', <Copyable value={u.id} />],
                ['Username', u.username ? `@${u.username}` : '—'],
                ['Display name', u.display_name || '—'],
                ['Joined', dateTime(u.created_at)],
                ['Last login', dateTime(u.last_login_at)],
              ]}
            />
          )}
          {shown && <ViewTable key={shown.name} view={shown} filters={{ [shown.user_filter]: String(u.id) }} hide={[shown.user_filter]} />}
          {tab === 'auth' && <Identities id={u.id} identities={p.identities} canUnlink={canIdentity} />}
          {tab === 'wallet' && <Wallet id={u.id} balances={p.balances} />}
          {tab === 'activity' && <ActivityTab id={u.id} />}
          {tab === 'seeds' && hasSeeds && (
            <>
              <SeedsTable rows={seeds.rows} loading={seeds.loading} />
              {seeds.more}
            </>
          )}
        </>
      )}

      {u && dialog === 'disconnect' && <Disconnect player={u} onClose={() => setDialog(null)} />}
      {u && dialog === 'adjust' && <AdjustDialog user={u.id} onClose={() => setDialog(null)} />}
    </>
  )
}

function Disconnect({ player, onClose }: { player: PlayerRow; onClose: () => void }) {
  const [reason, setReason] = useState('')
  const { busy, run } = useAction()
  return (
    <ChangeDialog
      title={`Disconnect ${playerName(player)}`}
      description="Closes every open connection of this player. They can connect again right away."
      okText="Disconnect"
      danger
      busy={busy}
      reason={reason}
      onReason={setReason}
      onClose={onClose}
      onSubmit={async () => {
        const r = await run(`/api/users/${player.id}/disconnect`, { method: 'POST', body: { reason: reason.trim() } }, 'Not disconnected')
        if (!r.ok) return
        toast.success('Disconnected')
        onClose()
      }}
    />
  )
}

function Identities({ id, identities, canUnlink }: { id: number; identities: Identity[]; canUnlink: boolean }) {
  const [unlinking, setUnlinking] = useState<Identity | null>(null)
  return (
    <>
      <DataTable
        rows={identities}
        rowKey={(i) => i.provider}
        empty="No linked identities."
        columns={[
          { key: 'provider', title: 'Provider', render: (i) => <span className="font-medium capitalize">{i.provider}</span> },
          { key: 'subject', title: 'Subject', render: (i) => <Copyable value={i.subject} /> },
          { key: 'linked', title: 'Linked', render: (i) => <When at={i.linked_at} /> },
          ...(canUnlink
            ? [
                {
                  key: 'unlink',
                  align: 'right' as const,
                  render: (i: Identity) => (
                    <Button variant="ghost" size="sm" className="text-danger" disabled={identities.length < 2} onClick={() => setUnlinking(i)}>
                      Unlink
                    </Button>
                  ),
                },
              ]
            : []),
        ]}
      />
      {canUnlink && identities.length < 2 && <Note>The only identity can't be unlinked: the player would have no way to log in.</Note>}
      {unlinking && <Unlink id={id} identity={unlinking} onClose={() => setUnlinking(null)} />}
    </>
  )
}

function Unlink({ id, identity, onClose }: { id: number; identity: Identity; onClose: () => void }) {
  const [reason, setReason] = useState('')
  const { busy, run } = useAction()
  return (
    <ChangeDialog
      title={`Unlink ${identity.provider}`}
      description={`Subject ${identity.subject}. The player can't log in with it any more, and it can be linked to another account.`}
      okText="Unlink"
      danger
      busy={busy}
      reason={reason}
      onReason={setReason}
      onClose={onClose}
      onSubmit={async () => {
        const path = `/api/users/${id}/identities/${encodeURIComponent(identity.provider)}`
        if (!(await run(path, { method: 'DELETE', body: { reason: reason.trim() } }, 'Not unlinked')).ok) return
        toast.success(`${identity.provider} unlinked`)
        onClose()
      }}
    />
  )
}

function Wallet({ id, balances }: { id: number; balances: Record<string, number> }) {
  const { money } = useMoney()
  const currencies = useMe()?.currencies ?? []
  return (
    <>
      <Stats items={currencies.map(({ code }) => ({ label: `${code.toUpperCase()} balance`, value: money(balances[code] ?? 0, code, false) }))} />
      <Section title="Adjustments" description="Balance changes made from the console for this player.">
        <AdjustmentsTable user={String(id)} />
      </Section>
    </>
  )
}

// One sub-tab per game that reports activity (app.History), kept in ?game=.
function ActivityTab({ id }: { id: number }) {
  const { query } = useCustom<{ games: string[] }>({ url: `/api/users/${id}/activity`, method: 'get' })
  const games = query.data?.data.games ?? []
  const [g, setG] = useParam('game')
  const game = games.includes(g) ? g : (games[0] ?? '')
  if (query.isLoading) return <Skeleton className="h-32 w-full rounded-lg" />
  if (!games.length) return <Note>No game reports activity.</Note>
  return (
    <>
      {games.length > 1 && (
        <div className="mb-3">
          <Segmented value={game} onChange={(v) => setG(v === games[0] ? null : v)} options={games.map((n) => ({ value: n, label: n }))} />
        </div>
      )}
      <ActivityTable id={id} game={game} key={game} />
    </>
  )
}

function ActivityTable({ id, game }: { id: number; game: string }) {
  const { money } = useMoney()
  const { rows, loading, more } = usePaged<Activity>(`users/${id}/activity`, { game })
  return (
    <>
      <DataTable
        rows={rows}
        loading={loading}
        rowKey={(b) => b.id}
        empty="No activity yet."
        columns={[
          { key: 'when', title: 'When', render: (b) => <When at={b.at} /> },
          { key: 'id', title: 'ID', render: (b) => <Mono>#{b.id}</Mono> },
          { key: 'amount', title: 'Amount', align: 'right', render: (b) => <span className="num">{money(b.amount, b.currency)}</span> },
          { key: 'payout', title: 'Payout', align: 'right', render: (b) => <span className="num">{money(b.payout, b.currency)}</span> },
          {
            key: 'res',
            title: 'Result',
            align: 'right',
            render: (b) => <Signed amount={b.payout - b.amount}>{money(b.payout - b.amount, b.currency)}</Signed>,
          },
          { key: 'status', title: 'Status', render: (b) => <Pill tone={b.status === 'settled' ? 'ok' : 'neutral'}>{b.status}</Pill> },
          { key: 'detail', title: 'Detail', className: 'whitespace-normal', render: (b) => <Detail detail={b.detail} /> },
        ]}
      />
      {more}
    </>
  )
}

// An activity row's game fields as key: value pairs.
function Detail({ detail }: { detail?: Record<string, unknown> }) {
  const entries = Object.entries(detail ?? {})
  if (!entries.length) return <Faint />
  return (
    <div className="flex flex-wrap gap-x-3 gap-y-0.5 text-xs">
      {entries.map(([k, v]) => (
        <span key={k}>
          <span className="text-muted-foreground">{k}</span> <span className="font-mono">{typeof v === 'object' ? JSON.stringify(v) : String(v)}</span>
        </span>
      ))}
    </div>
  )
}

const short = (h: string) => (h.length > 20 ? `${h.slice(0, 10)}…${h.slice(-6)}` : h)

function SeedsTable({ rows, loading }: { rows: Seed[] | undefined; loading: boolean }) {
  return (
    <DataTable
      rows={rows}
      loading={loading}
      rowKey={(s) => s.id}
      empty="No seed pairs yet."
      columns={[
        { key: 'id', title: 'Pair', render: (s) => <Mono>#{s.id}</Mono> },
        { key: 'status', title: 'Status', render: (s) => <Pill tone={s.status === 'active' ? 'ok' : s.status === 'next' ? 'brand' : 'neutral'}>{s.status}</Pill> },
        { key: 'hash', title: 'Server seed hash', render: (s) => <Copyable value={s.server_seed_hash} label={short(s.server_seed_hash)} /> },
        { key: 'seed', title: 'Server seed', render: (s) => (s.server_seed ? <Copyable value={s.server_seed} label={short(s.server_seed)} /> : <Faint>hidden until revealed</Faint>) },
        { key: 'client', title: 'Client seed', render: (s) => <Copyable value={s.client_seed} label={short(s.client_seed)} /> },
        { key: 'nonce', title: 'Nonce', align: 'right', render: (s) => <span className="num">{s.nonce}</span> },
        { key: 'created', title: 'Created', render: (s) => <When at={s.created_at} /> },
        { key: 'revealed', title: 'Revealed', render: (s) => <When at={s.revealed_at} /> },
      ]}
    />
  )
}
