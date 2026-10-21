import type { AuthProvider } from '@refinedev/core'
import { api, ApiError } from '@/lib/api'

// /api/me: who's logged in, what they may do, how the console looks, the currencies.
export type Role = 'owner' | 'operator' | 'support' | 'viewer'
export type Currency = { code: string; decimals: number }
export type Me = {
  user: { id: number; email: string; role: Role }
  permissions: string[]
  brand: { name: string; accent: string; logo: string | null }
  currencies: Currency[]
}

// One /api/me per page load (and after login): check, getIdentity, getPermissions and can share it.
let me: Promise<Me> | null = null

export function loadMe(fresh = false): Promise<Me> {
  if (fresh || !me) {
    const p = api<Me>('/api/me')
    p.catch(() => me === p && (me = null)) // a failure isn't cached
    me = p
  }
  return me
}

// The otpauth URI of an enrolment in progress: login hands it to /enrol (this tab only; a reload
// there means logging in again, which starts a new enrolment).
const ENROL = 'gamify.admin.enrol'

export function enrolURI(): string | null {
  try {
    return sessionStorage.getItem(ENROL)
  } catch {
    return null
  }
}

export function setEnrolURI(uri: string | null) {
  try {
    uri ? sessionStorage.setItem(ENROL, uri) : sessionStorage.removeItem(ENROL)
  } catch {
    // storage blocked: enrolment needs it, the page says to log in again
  }
}

export type LoginForm = { email: string; password: string; totp?: string }

const BUSY_TRIES = 5
const sleep = (s: number) => new Promise((r) => setTimeout(r, s * 1000))

// POST /api/login, retrying 429 busy (too many logins in flight) after its Retry-After.
async function postLogin(form: LoginForm) {
  for (let i = 1; ; i++) {
    try {
      return await api<{ enrol: boolean; otpauth_uri?: string }>('/api/login', { method: 'POST', body: form })
    } catch (e) {
      if (!(e instanceof ApiError && e.code === 'busy') || i === BUSY_TRIES) throw e
      await sleep(e.retryAfter ?? 1)
    }
  }
}

// Refine shows it as a toast: name the title, message the API's message.
const failure = (e: unknown) => ({ success: false, error: { name: 'Sign-in failed', message: (e as Error).message } })

export const authProvider: AuthProvider = {
  async login(form: LoginForm) {
    try {
      const r = await postLogin(form)
      me = null
      if (r.enrol && r.otpauth_uri) {
        setEnrolURI(r.otpauth_uri)
        return { success: true, redirectTo: '/enrol' }
      }
      setEnrolURI(null)
      return { success: true, redirectTo: '/' }
    } catch (e) {
      return failure(e)
    }
  },

  async logout() {
    await api('/api/logout', { method: 'POST' }).catch(() => {}) // already gone: fine
    me = null
    setEnrolURI(null)
    return { success: true, redirectTo: '/login' }
  },

  async check() {
    try {
      await loadMe(true)
      return { authenticated: true }
    } catch {
      // Including a half session (TOTP not set up yet: /enrol is outside the shell).
      return { authenticated: false, logout: true, redirectTo: '/login' }
    }
  },

  async onError(e) {
    return e?.statusCode === 401 ? { logout: true, redirectTo: '/login' } : {}
  },

  getIdentity: () => loadMe().catch(() => null),
  getPermissions: () => loadMe().then((m) => m.permissions, () => null),
}
