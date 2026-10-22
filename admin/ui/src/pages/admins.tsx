import { useList } from '@refinedev/core'
import { useState } from 'react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import type { Role } from '../providers/auth'
import { ChangeDialog, Choice, Copyable, DataTable, Field, Mono, Note, PageHeader, Pill, When, useAction, useMe } from '../ui'

type ConsoleUser = { id: number; email: string; role: Role; telegram_id: number | null; disabled: boolean; totp_enrolled: boolean; created_at: string }

const ROLES: { value: Role; label: string; hint: string }[] = [
  { value: 'owner', label: 'Owner', hint: 'Everything: also approves adjustments and manages console users.' },
  { value: 'operator', label: 'Operator', hint: 'Game limits, identities and adjustment requests.' },
  { value: 'support', label: 'Support', hint: 'Unlinks identities and disconnects players.' },
  { value: 'viewer', label: 'Viewer', hint: 'Read only.' },
]

// Console users: create (the one-time password is shown once), change role, disable, reset TOTP
// or password. Every change signs the user out everywhere.
export function Admins() {
  const me = useMe()
  const { result, query } = useList<ConsoleUser>({ resource: 'admins', pagination: { mode: 'off' } })
  const [editing, setEditing] = useState<ConsoleUser | 'new' | null>(null)
  const [password, setPassword] = useState<{ email: string; password: string } | null>(null)

  return (
    <>
      <PageHeader title="Console users" description="Who can sign in to this console and what their role allows." actions={<Button onClick={() => setEditing('new')}>New console user</Button>} />
      <DataTable
        rows={result.data}
        loading={query.isLoading}
        rowKey={(u) => u.id}
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
          { key: 'role', title: 'Role', render: (u) => ROLES.find((r) => r.value === u.role)?.label ?? u.role },
          {
            key: 'status',
            title: 'Status',
            render: (u) =>
              u.disabled ? <Pill tone="danger">Disabled</Pill> : u.totp_enrolled ? <Pill tone="ok">Active</Pill> : <Pill tone="warn">Not enrolled yet</Pill>,
          },
          { key: 'created', title: 'Created', render: (u) => <When at={u.created_at} /> },
        ]}
      />
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
