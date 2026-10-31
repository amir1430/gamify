import { useList } from '@refinedev/core'
import { useState } from 'react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Chips, FilterBy, FilterMenu, Pager, SearchBox, SortMenu, listChips, useClientList } from '@/components/list'
import { by, type FilterGroup, type Sort } from '@/lib/list'
import type { Role } from '../providers/auth'
import { ChangeDialog, Choice, Copyable, DataTable, Field, Mono, Note, PageHeader, Pill, Toolbar, When, useAction, useMe } from '../ui'

type ConsoleUser = { id: number; email: string; role: Role; telegram_id: number | null; disabled: boolean; totp_enrolled: boolean; created_at: string }

const ROLES: { value: Role; label: string; hint: string }[] = [
  { value: 'owner', label: 'Owner', hint: 'Everything: also approves adjustments and manages console users.' },
  { value: 'operator', label: 'Operator', hint: 'Game limits, identities and adjustment requests.' },
  { value: 'support', label: 'Support', hint: 'Unlinks identities and disconnects players.' },
  { value: 'viewer', label: 'Viewer', hint: 'Read only.' },
]

// Console users: create (the one-time password is shown once), change role, disable, reset TOTP
// or password. Every change signs the user out everywhere.
const statusOf = (u: ConsoleUser) => (u.disabled ? 'disabled' : u.totp_enrolled ? 'active' : 'pending')

const GROUPS: FilterGroup<ConsoleUser>[] = [
  { key: 'role', label: 'Role', options: ROLES.map((r) => ({ value: r.value, label: r.label })), match: (u, v) => u.role === v },
  {
    key: 'status',
    label: 'Status',
    options: [
      { value: 'active', label: 'Active' },
      { value: 'pending', label: 'Not enrolled yet' },
      { value: 'disabled', label: 'Disabled' },
    ],
    match: (u, v) => statusOf(u) === v,
  },
]
const SORTS: Sort<ConsoleUser>[] = [
  { value: 'email', label: 'Email A–Z', compare: by((u) => u.email) },
  { value: 'new', label: 'Newest first', compare: by((u) => u.created_at, true) },
  { value: 'old', label: 'Oldest first', compare: by((u) => u.created_at) },
  { value: 'role', label: 'Role', compare: by((u) => ROLES.findIndex((r) => r.value === u.role)) },
]

export function Admins() {
  const me = useMe()
  const { result, query } = useList<ConsoleUser>({ resource: 'admins', pagination: { mode: 'off' } })
  const [editing, setEditing] = useState<ConsoleUser | 'new' | null>(null)
  const [password, setPassword] = useState<{ email: string; password: string } | null>(null)
  const list = useClientList('admins', result.data, { text: (u) => [u.email, u.id, `#${u.id}`, u.telegram_id], groups: GROUPS, sorts: SORTS }, 48)

  return (
    <>
      <PageHeader
        title="Console users"
        meta={result.data.length > 0 && <Pill>{result.data.length}</Pill>}
        description="Who can sign in to this console and what their role allows."
        actions={<Button onClick={() => setEditing('new')}>New console user</Button>}
      />
      <Toolbar>
        <SearchBox value={list.text} onChange={list.setText} placeholder="Search email, ID or Telegram ID" />
        <FilterMenu groups={GROUPS} values={list.filters} onChange={list.setFilter} onClear={list.clear} />
        <SortMenu sorts={SORTS} value={list.sort} onChange={list.setSort} />
      </Toolbar>
      <Chips items={listChips(list, GROUPS)} onClear={list.clear} />
      <DataTable
        rows={list.page.rows}
        loading={query.isLoading}
        rowKey={(u) => u.id}
        empty={list.filtering ? 'No console user matches.' : 'No console users.'}
        onRowClick={(u) => setEditing(u)}
        columns={[
          {
            key: 'email',
            title: 'Email',
            render: (u) => (
              <span className="font-medium">
                {u.email} {u.id === me?.user.id && <Pill tone="brand">you</Pill>}
              </span>
            ),
          },
          { key: 'id', title: 'ID', render: (u) => <Mono>#{u.id}</Mono> },
          {
            key: 'role',
            title: 'Role',
            render: (u) => <FilterBy on={() => list.toggle('role', u.role)}>{ROLES.find((r) => r.value === u.role)?.label ?? u.role}</FilterBy>,
          },
          {
            key: 'status',
            title: 'Status',
            render: (u) => (
              <FilterBy on={() => list.toggle('status', statusOf(u))}>
                {u.disabled ? <Pill tone="danger">Disabled</Pill> : u.totp_enrolled ? <Pill tone="ok">Active</Pill> : <Pill tone="warn">Not enrolled yet</Pill>}
              </FilterBy>
            ),
          },
          { key: 'created', title: 'Created', render: (u) => <When at={u.created_at} /> },
        ]}
      />
      <Pager page={list.page} total={list.filtered.length} noun={['console user', 'console users']} size={list.size} onPage={list.setPage} onSize={list.setSize} />
      {editing === 'new' && <Create onClose={() => setEditing(null)} onPassword={setPassword} />}
      {editing && editing !== 'new' && <Edit user={editing} self={editing.id === me?.user.id} onClose={() => setEditing(null)} onPassword={setPassword} />}
      {password && <OneTime {...password} onClose={() => setPassword(null)} />}
    </>
  )
}

type Done = (p: { email: string; password: string }) => void

function Create({ onClose, onPassword }: { onClose: () => void; onPassword: Done }) {
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Role>('viewer')
  const { busy, run } = useAction()
  return (
    <ChangeDialog
      title="New console user"
      description="They sign in with the one-time password shown next, then set up their authenticator."
      okText="Create"
      busy={busy}
      disabled={!email.trim()}
      onClose={onClose}
      onSubmit={async () => {
        const r = await run<{ password: string }>('/api/admins', { method: 'POST', body: { email: email.trim(), role } }, 'Not created')
        if (!r.ok || !r.data) return
        onClose()
        onPassword({ email: email.trim(), password: r.data.password })
      }}
    >
      <Field label="Email">
        <Input type="email" autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
      </Field>
      <RoleField value={role} onChange={setRole} />
    </ChangeDialog>
  )
}

const RoleField = ({ value, onChange }: { value: Role; onChange: (r: Role) => void }) => (
  <Field label="Role" hint={ROLES.find((r) => r.value === value)?.hint}>
    <Choice className="w-full" value={value} onChange={(v) => onChange(v as Role)} options={ROLES.map((r) => ({ value: r.value, label: r.label }))} />
  </Field>
)

function Edit({ user, self, onClose, onPassword }: { user: ConsoleUser; self: boolean; onClose: () => void; onPassword: Done }) {
  const [role, setRole] = useState(user.role)
  const [disabled, setDisabled] = useState(user.disabled)
  const { busy, run } = useAction()
  const patch = async (body: object, then: (r: { password?: string }) => void) => {
    const r = await run<{ password?: string }>(`/api/admins/${user.id}`, { method: 'PATCH', body })
    if (r.ok) then(r.data ?? {})
  }
  const changed = role !== user.role || disabled !== user.disabled
  return (
    <ChangeDialog
      title={user.email}
      description={`Console user #${user.id}. Any change here signs them out everywhere.`}
      busy={busy}
      disabled={!changed}
      onClose={onClose}
      onSubmit={() =>
        patch({ ...(role !== user.role && { role }), ...(disabled !== user.disabled && { disabled }) }, () => {
          toast.success('Saved')
          onClose()
        })
      }
    >
      <RoleField value={role} onChange={setRole} />
      <label className="flex items-center justify-between gap-4 rounded-lg border px-4 py-3">
        <span>
          <span className="block text-sm font-medium">Disabled</span>
          <span className="block text-xs text-muted-foreground">Can't sign in until enabled again.</span>
        </span>
        <Switch checked={disabled} onCheckedChange={setDisabled} />
      </label>
      {self && <Note className="mt-0">You can't demote or disable yourself as an owner; another owner can.</Note>}
      <div className="flex flex-wrap gap-2 border-t pt-4">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={busy}
          onClick={() =>
            patch({ reset_totp: true }, () => {
              toast.success('Authenticator reset', { description: 'They set up a new one at their next sign-in.' })
              onClose()
            })
          }
        >
          Reset authenticator
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={busy}
          onClick={() =>
            patch({ reset_password: true }, (r) => {
              onClose()
              if (r.password) onPassword({ email: user.email, password: r.password })
            })
          }
        >
          Reset password
        </Button>
      </div>
    </ChangeDialog>
  )
}

// A one-time password: shown once, never again.
function OneTime({ email, password, onClose }: { email: string; password: string; onClose: () => void }) {
  return (
    <ChangeDialog
      title="One-time password"
      description={`Give it to ${email} over a safe channel. It isn't shown again.`}
      busy={false}
      onClose={onClose}
      footer={<Button onClick={onClose}>Done</Button>}
    >
      <div className="rounded-lg border bg-muted/30 px-4 py-3 text-center">
        <Copyable value={password} label={password} />
      </div>
    </ChangeDialog>
  )
}
