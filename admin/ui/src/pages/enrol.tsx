import { useState } from 'react'
import { Link, Navigate, useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { Qr } from '@/components/qr'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { api, ApiError } from '@/lib/api'
import { enrolURI, setEnrolURI } from '../providers/auth'
import { Copyable, Field } from '../ui'
import { AuthCard } from './login'

// First login: scan the otpauth URI (or type the secret), then confirm with a code. The API then
// replaces the half session with a full one.
export function Enrol() {
  const [uri] = useState(enrolURI)
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [expired, setExpired] = useState(false)
  const to = useSearchParams()[0].get('to') ?? ''
  if (!uri) return <Navigate to="/login" replace />
  const secret = new URL(uri).searchParams.get('secret') ?? ''

  if (expired)
    return (
      <AuthCard title="Set up two-factor authentication" description="This setup has ended. Sign in again to start a new one.">
        <Button asChild className="mt-6 w-full">
          <Link to="/login">Back to sign in</Link>
        </Button>
      </AuthCard>
    )

  return (
    <AuthCard
      title="Set up two-factor authentication"
      description="Scan the code with an authenticator app (or enter the secret), then type the 6-digit code it shows."
    >
      <div className="mt-6 grid justify-items-center gap-3">
        <Qr value={uri} size={184} label="Authenticator setup QR code" />
        <div className="text-center text-xs text-muted-foreground">
          Secret: <Copyable value={secret} label={secret.replace(/(.{4})/g, '$1 ').trim()} />
        </div>
      </div>
      <form
        className="mt-6 grid gap-4"
        onSubmit={async (e) => {
          e.preventDefault()
          setBusy(true)
          try {
            await api('/api/totp/enrol', { method: 'POST', body: { code: code.replace(/\s/g, '') } })
            setEnrolURI(null)
            // A fresh start with the full session, at ?to= (the page asked for before login) if any.
            location.replace(to.startsWith('/') && !to.startsWith('//') ? to : '/')
          } catch (err) {
            if (err instanceof ApiError && (err.code === 'not_enrolling' || err.status === 401)) {
              setEnrolURI(null)
              setExpired(true)
            } else toast.error('Setup failed', { description: (err as Error).message })
            setBusy(false)
          }
        }}
      >
        <Field label="Authenticator code">
          <Input
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9 ]{6,7}"
            maxLength={7}
            required
            autoFocus
            placeholder="123 456"
            className="font-mono tracking-widest"
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
        </Field>
        <Button type="submit" disabled={busy}>
          {busy ? 'Checking…' : 'Confirm'}
        </Button>
      </form>
    </AuthCard>
  )
}
