// The console's API (admin/*.go), same origin. The session is the gamify_admin cookie (HttpOnly),
// so nothing here holds a token. Writes carry X-Admin: 1; the browser adds Origin.

// A failed call: the API's {code, message} and the HTTP status (0 = unreachable). statusCode is
// the same status under the name Refine's HttpError reads (authProvider.onError, notifications).
export class ApiError extends Error {
  readonly statusCode: number
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly retryAfter?: number, // seconds, from Retry-After (429 busy)
  ) {
    super(message)
    this.statusCode = status
  }
}

export type ApiInit = Omit<RequestInit, 'body'> & { body?: unknown } // body: sent as JSON

export async function api<T = unknown>(path: string, init: ApiInit = {}): Promise<T> {
  const { body, headers, ...rest } = init
  const method = (init.method ?? 'GET').toUpperCase()
  let res: Response
  try {
    res = await fetch(path, {
      ...rest,
      method,
      credentials: 'same-origin',
      headers: {
        Accept: 'application/json',
        ...(body !== undefined && { 'Content-Type': 'application/json' }),
        ...(method !== 'GET' && method !== 'HEAD' && { 'X-Admin': '1' }),
        ...(headers as Record<string, string>),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e
    throw new ApiError(0, 'unreachable', 'The admin API is unreachable')
  }
  const text = await res.text()
  let data: unknown = undefined
  try {
    data = text ? JSON.parse(text) : undefined
  } catch {
    // not JSON (a proxy's error page): status only
  }
  if (!res.ok) {
    const e = (data ?? {}) as { code?: string; message?: string }
    const after = Number(res.headers.get('Retry-After'))
    throw new ApiError(res.status, e.code ?? 'http_' + res.status, e.message ?? `Error ${res.status}`, after > 0 ? after : undefined)
  }
  return data as T
}

// A query string from params, skipping empty ones ('' with none).
export function qs(params: Record<string, string | number | bigint | boolean | null | undefined>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v != null && v !== '') q.set(k, String(v))
  return q.size ? `?${q}` : ''
}
