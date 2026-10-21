import { useLogin } from '@refinedev/core'
import { useState, type ReactNode } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Mark } from '../layout'
import type { LoginForm } from '../providers/auth'
import { Field } from '../ui'

// A centred card with the brand mark (the logo is public; the ring mark without one).
export function AuthCard({ title, description, children }: { title: string; description: ReactNode; children: ReactNode }) {
  return (
    <div className="grid min-h-svh place-items-center bg-background p-4">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center gap-2.5">
          <Mark logo="/api/brand/logo" />
          <span className="text-sm font-semibold tracking-tight">Admin</span>
        </div>
        <div className="rounded-xl border bg-card p-6">
          <h1 className="text-lg font-semibold tracking-tight">{title}</h1>
          <p className="mt-1 text-sm text-muted-foreground">{description}</p>
          {children}
        </div>
      </div>
    </div>
  )
}

// Email, password and the authenticator code. The first login has no code yet: it leads to /enrol.
// Errors show as toasts; 429 busy is retried by the auth provider.
export function Login() {
  const { mutate: login, isPending } = useLogin<LoginForm>()
  const navigate = useNavigate()
  const { search } = useLocation()
  const [form, setForm] = useState<LoginForm>({ email: '', password: '', totp: '' })
  const set = (k: keyof LoginForm) => (e: React.ChangeEvent<HTMLInputElement>) => setForm((f) => ({ ...f, [k]: e.target.value }))
  return (
    <AuthCard title="Sign in" description="Console users only. First time? Leave the code empty: you'll set up your authenticator next.">
      <form
        className="mt-6 grid gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          login(
            { ...form, email: form.email.trim(), totp: form.totp?.replace(/\s/g, '') },
            // Refine sends a login with ?to= straight there, which a half session can't open: enrol
            // first, keeping ?to= for after.
            { onSuccess: (r) => r.redirectTo === '/enrol' && navigate({ pathname: '/enrol', search }, { replace: true }) },
          )
        }}
      >
        <Field label="Email">
          <Input type="email" autoComplete="username" required autoFocus value={form.email} onChange={set('email')} />
        </Field>
        <Field label="Password">
          <Input type="password" autoComplete="current-password" required value={form.password} onChange={set('password')} />
        </Field>
        <Field label="Authenticator code">
          <Input
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9 ]{6,7}"
            maxLength={7}
            placeholder="123 456"
            className="font-mono tracking-widest"
            value={form.totp}
            onChange={set('totp')}
          />
        </Field>
        <Button type="submit" disabled={isPending} className="mt-2">
          {isPending ? 'Signing in…' : 'Sign in'}
        </Button>
      </form>
    </AuthCard>
  )
}
