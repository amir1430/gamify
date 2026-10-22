import { useState } from 'react'
import { Link } from 'react-router'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { parseAmount } from '@/lib/money'
import {
  ChangeDialog,
  Choice,
  DataTable,
  Facts,
  Field,
  Mono,
  PageHeader,
  Pill,
  Signed,
  Toolbar,
  When,
  useAction,
  useMe,
  useMoney,
  usePaged,
  useParam,
  usePerm,
} from '../ui'

export type Adjustment = {
  id: number
  user_id: number
  currency: string
  amount: number
  reason: string
  requested_by: number
  approved_by: number | null
  status: 'pending_approval' | 'approved' | 'done' | 'failed' | 'rejected'
  error: string | null
  created_at: string
  done_at: string | null
}

const STATUS: Record<Adjustment['status'], { label: string; tone: 'ok' | 'danger' | 'warn' | 'brand' | 'neutral' }> = {
  pending_approval: { label: 'Waiting for approval', tone: 'warn' },
  approved: { label: 'Approved, applying', tone: 'brand' },
  done: { label: 'Done', tone: 'ok' },
  failed: { label: 'Failed', tone: 'danger' },
  rejected: { label: 'Rejected', tone: 'neutral' },
}

// Balance adjustments, newest first, filtered by ?status=. Approvers decide others' requests here.
export function Adjustments() {
  const [status, setStatus] = useParam('status')
  const [creating, setCreating] = useState(false)
  const canAdjust = usePerm('adjust')
  return (
    <>
      <PageHeader
        title="Adjustments"
        description="Credits and debits made from the console. Each one is a ledger transfer from or to the adjust account."
        actions={canAdjust && <Button onClick={() => setCreating(true)}>New adjustment</Button>}
      />
      <Toolbar>
        <Choice
          label="Status"
          value={status}
          onChange={(v) => setStatus(v || null)}
          options={[{ value: '', label: 'Any status' }, ...Object.entries(STATUS).map(([value, s]) => ({ value, label: s.label }))]}
        />
      </Toolbar>
      <AdjustmentsTable status={status} />
      {creating && <AdjustDialog onClose={() => setCreating(false)} />}
    </>
  )
}

// Adjustments, optionally of one player (?user= on the API) or in one status.
export function AdjustmentsTable({ user = '', status = '' }: { user?: string; status?: string }) {
  const me = useMe()
  const { money } = useMoney()
  const canApprove = usePerm('approve')
  const { rows, loading, more } = usePaged<Adjustment>('adjustments', { user, status })
  const [deciding, setDeciding] = useState<{ a: Adjustment; approve: boolean } | null>(null)
  const who = (id: number | null) => (id == null ? '—' : id === me?.user.id ? 'You' : <Mono>admin #{id}</Mono>)

  return (
    <>
      <DataTable
        rows={rows}
        loading={loading}
        rowKey={(a) => a.id}
        empty={status ? 'No adjustment in this status.' : 'No adjustments yet.'}
        columns={[
          { key: 'id', title: 'ID', render: (a) => <Mono>#{a.id}</Mono> },
          { key: 'when', title: 'Requested', render: (a) => <When at={a.created_at} /> },
          ...(user
            ? []
            : [
                {
                  key: 'player',
                  title: 'Player',
                  render: (a: Adjustment) => (
                    <Link to={`/players/${a.user_id}`} className="text-brand hover:underline">
                      #{a.user_id}
                    </Link>
                  ),
                },
              ]),
          { key: 'amount', title: 'Amount', align: 'right', render: (a) => <Signed amount={a.amount}>{money(a.amount, a.currency)}</Signed> },
          {
            key: 'status',
            title: 'Status',
            render: (a) => (
              <>
                <Pill tone={STATUS[a.status].tone}>{STATUS[a.status].label}</Pill>
                {a.error && <div className="mt-0.5 text-xs text-danger">{a.error}</div>}
              </>
            ),
          },
          { key: 'reason', title: 'Reason', className: 'whitespace-normal min-w-48 text-muted-foreground', render: (a) => a.reason },
          { key: 'by', title: 'Requested by', render: (a) => who(a.requested_by) },
          { key: 'decided', title: 'Decided by', render: (a) => who(a.approved_by) },
          ...(canApprove
            ? [
                {
                  key: 'decide',
                  align: 'right' as const,
                  render: (a: Adjustment) =>
                    a.status === 'pending_approval' &&
                    a.requested_by !== me?.user.id && (
                      <div className="flex justify-end gap-1">
                        <Button size="sm" onClick={() => setDeciding({ a, approve: true })}>
                          Approve
                        </Button>
                        <Button size="sm" variant="ghost" className="text-danger" onClick={() => setDeciding({ a, approve: false })}>
                          Reject
                        </Button>
                      </div>
                    ),
                },
              ]
            : []),
        ]}
      />
      {more}
      {deciding && <Decide {...deciding} onClose={() => setDeciding(null)} />}
    </>
  )
}

// Approve (applies it now; reason optional) or reject (reason required).
function Decide({ a, approve, onClose }: { a: Adjustment; approve: boolean; onClose: () => void }) {
  const { money } = useMoney()
  const [reason, setReason] = useState('')
  const { busy, run } = useAction()
  return (
    <ChangeDialog
      title={approve ? `Approve adjustment #${a.id}` : `Reject adjustment #${a.id}`}
      description={approve ? 'The balance changes as soon as you approve.' : 'Nothing moves; the request is closed.'}
      okText={approve ? 'Approve' : 'Reject'}
      danger={!approve}
      busy={busy}
      reason={reason}
      onReason={approve ? undefined : setReason}
      onClose={onClose}
      onSubmit={async () => {
        const r = await run<Adjustment>(`/api/adjustments/${a.id}/${approve ? 'approve' : 'reject'}`, {
          method: 'POST',
          body: approve ? undefined : { reason: reason.trim() },
        })
        if (!r.ok) return
        if (r.data?.status === 'failed') toast.error('Approved, but the ledger refused it', { description: r.data.error ?? undefined })
        else toast.success(approve ? 'Approved' : 'Rejected')
        onClose()
      }}
    >
      <Summary user={a.user_id} currency={a.currency} amount={money(a.amount, a.currency)} reason={a.reason} />
    </ChangeDialog>
  )
}

const Summary = ({ user, currency, amount, reason, approval }: { user: number; currency: string; amount: string; reason: string; approval?: string }) => (
  <Facts
    cols={2}
    items={[
      ['Player', `#${user}`],
      ['Currency', currency.toUpperCase()],
      ['Amount', <span className="num font-medium">{amount}</span>],
      ...(approval ? [['Approval', approval] as [string, string]] : []),
      ['Reason', reason],
    ]}
  />
)

// A new adjustment: player, currency, signed amount and reason, then a confirmation repeating all
// four and whether it waits for approval. The idempotency key is made when the dialog opens and
// kept on retries, so a double click or a lost response never moves money twice.
export function AdjustDialog({ user, onClose }: { user?: number; onClose: () => void }) {
  const currencies = useMe()?.currencies ?? []
  const { money } = useMoney()
  const [key] = useState(() => crypto.randomUUID())
  const [form, setForm] = useState({ user: user ? String(user) : '', currency: currencies[0]?.code ?? '', amount: '', reason: '' })
  const [confirming, setConfirming] = useState(false)
  const { busy, run } = useAction()

  const cur = currencies.find((c) => c.code === form.currency)
  const userID = /^[1-9]\d{0,18}$/.test(form.user) ? BigInt(form.user) : null
  let amount: bigint | null = null
  let amountError = ''
  if (form.amount && cur) {
    try {
      amount = parseAmount(form.amount, cur.decimals)
      if (amount === 0n) amountError = 'Must not be zero.'
    } catch {
      amountError = cur.decimals ? `A number with at most ${cur.decimals} decimals, "-" for a debit.` : 'A whole number, "-" for a debit.'
    }
  }
  const ok = userID != null && cur && amount != null && !amountError && form.reason.trim()
  const abs = amount != null && amount < 0n ? -amount : amount
  const waits = cur?.approve_above != null && abs != null && abs > BigInt(cur.approve_above)
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm((f) => ({ ...f, [k]: e.target.value }))

  return (
    <ChangeDialog
      title={confirming ? 'Confirm the adjustment' : 'New adjustment'}
      description={
        confirming
          ? waits
            ? 'Above the approval limit: another admin with approve rights must approve it before the balance changes.'
            : 'The balance changes as soon as you confirm.'
          : 'A positive amount credits the player, a negative one debits them. A debit beyond their balance fails.'
      }
      okText={confirming ? (waits ? 'Request approval' : 'Confirm') : 'Review'}
      danger={confirming && amount != null && amount < 0n}
      busy={busy}
      disabled={!ok}
      onClose={onClose}
      footer={
        confirming ? (
          <>
            <Button type="button" variant="ghost" onClick={() => setConfirming(false)} disabled={busy}>
              Back
            </Button>
            <Button type="submit" variant={amount! < 0n ? 'destructive' : 'default'} disabled={busy || !ok}>
              {busy ? 'Sending…' : waits ? 'Request approval' : 'Confirm'}
            </Button>
          </>
        ) : undefined
      }
      onSubmit={async () => {
        if (!confirming) return setConfirming(true)
        const r = await run<Adjustment>('/api/adjustments', {
          method: 'POST',
          body: { user_id: userID, currency: form.currency, amount, reason: form.reason.trim(), idempotency_key: key },
        })
        if (!r.ok || !r.data) return
        const a = r.data
        if (a.status === 'failed') toast.error('The ledger refused the adjustment', { description: a.error ?? undefined })
        else toast.success(STATUS[a.status].label, { description: `#${a.id} · ${money(a.amount, a.currency)} for player #${a.user_id}` })
        onClose()
      }}
    >
      {confirming ? (
        <Summary
          user={Number(userID)}
          currency={form.currency}
          amount={`${amount! > 0n ? '+' : ''}${money(amount!, form.currency)}`}
          reason={form.reason.trim()}
          approval={waits ? 'Needed' : 'Not needed'}
        />
      ) : (
        <>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Player ID" error={form.user && userID == null && 'A player ID.'}>
              <Input inputMode="numeric" value={form.user} disabled={!!user} onChange={set('user')} autoFocus={!user} />
            </Field>
            <Field label="Currency">
              <Choice className="w-full" value={form.currency} onChange={(v) => setForm((f) => ({ ...f, currency: v }))} options={currencies.map((c) => ({ value: c.code, label: c.code.toUpperCase() }))} />
            </Field>
          </div>
          <Field
            label="Amount"
            error={amountError}
            hint={cur?.approve_above != null ? `Above ${money(cur.approve_above, cur.code)} either way needs approval.` : undefined}
          >
            <Input className="font-mono" inputMode="decimal" placeholder={cur?.decimals ? `e.g. 10.${'0'.repeat(cur.decimals)} or -5` : 'e.g. 100 or -5'} value={form.amount} onChange={set('amount')} autoFocus={!!user} />
          </Field>
          <Field label="Reason" hint="Goes to the audit log and the adjustment.">
            <Input value={form.reason} maxLength={500} placeholder="Why this adjustment?" onChange={set('reason')} />
          </Field>
        </>
      )}
    </ChangeDialog>
  )
}
