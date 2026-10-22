import { useState } from 'react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Facts, Field, PageHeader, Section, useAction, useMe } from '../ui'

const MIN = 12 // the API's minimum

// Who you are, and a password change (signs out your other sessions).
export function Account() {
  const me = useMe()
  const [form, setForm] = useState({ current: '', next: '', again: '' })
  const { busy, run } = useAction()
  const short = form.next.length > 0 && [...form.next].length < MIN
  const mismatch = form.again.length > 0 && form.again !== form.next
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm((f) => ({ ...f, [k]: e.target.value }))

  return (
    <>
      <PageHeader title="Account" />
      {me && (
        <Facts
          cols={2}
          items={[
            ['Email', me.user.email],
            ['Role', me.user.role],
          ]}
        />
      )}
      <Section title="Change password" description="Your other sessions are signed out; this one stays.">
        <form
          className="grid max-w-sm gap-4"
          onSubmit={async (e) => {
            e.preventDefault()
            if ((await run('/api/me/password', { method: 'POST', body: { current: form.current, new: form.next } }, 'Password not changed')).ok) {
              toast.success('Password changed')
              setForm({ current: '', next: '', again: '' })
            }
          }}
        >
          <Field label="Current password">
            <Input type="password" autoComplete="current-password" required value={form.current} onChange={set('current')} />
          </Field>
          <Field label="New password" hint={`At least ${MIN} characters.`} error={short && `At least ${MIN} characters.`}>
            <Input type="password" autoComplete="new-password" required value={form.next} onChange={set('next')} />
          </Field>
          <Field label="New password again" error={mismatch && "Doesn't match."}>
            <Input type="password" autoComplete="new-password" required value={form.again} onChange={set('again')} />
          </Field>
          <Button type="submit" disabled={busy || short || mismatch || !form.current || !form.next || form.again !== form.next}>
            {busy ? 'Saving…' : 'Change password'}
          </Button>
        </form>
      </Section>
    </>
  )
}
