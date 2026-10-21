import type { BaseKey, CrudFilter, DataProvider } from '@refinedev/core'
import { api, qs } from '@/lib/api'

// Resources are API paths under /api: users, games, adjustments, audit, admins, and any other
// path (views/<name>) as itself.
const path = (resource: string) => `/api/${resource}`
const one = (resource: string, id: BaseKey) => `${path(resource)}/${encodeURIComponent(id)}`

// Filters are plain query parameters (eq; empty = unset).
function params(filters: CrudFilter[] = []) {
  const p: Record<string, string> = {}
  for (const f of filters) if ('field' in f && f.value != null && f.value !== '') p[f.field] = String(f.value)
  return p
}

// Lists page by keyset, never offsets: meta.before in (an id; '' = the first page), meta.next out
// (null = the last page). total is -1: the API doesn't count. cursor.next is the same value, so
// useInfiniteList's fetchNextPage ("Load more") works too: it comes back as the page param.
// Unpaged lists (games, views) have no next.
export const dataProvider: DataProvider = {
  getApiUrl: () => '/api',

  async getList({ resource, filters, pagination, meta }) {
    const page = pagination?.currentPage as unknown
    const before = meta?.before ?? (typeof page === 'string' ? page : undefined)
    const limit = pagination?.mode === 'server' ? pagination.pageSize : undefined
    const r = await api<{ items: never[]; next?: number | string | null }>(path(resource) + qs({ ...params(filters), before, limit }), {
      signal: meta?.signal,
    })
    const next = r.next == null ? null : String(r.next)
    return { data: r.items, total: -1, meta: { next }, cursor: { next: next ?? undefined } }
  },

  getOne: async ({ resource, id, meta }) => ({ data: await api(one(resource, id), { signal: meta?.signal }) }),

  create: async ({ resource, variables }) => ({ data: await api(path(resource), { method: 'POST', body: variables }) }),

  // meta.action: a sub-path ('/approve'); meta.method: other than PUT (PATCH for admins).
  update: async ({ resource, id, variables, meta }) => ({
    data: await api(one(resource, id) + (meta?.action ?? ''), { method: meta?.method ?? 'PUT', body: variables }),
  }),

  deleteOne: async ({ resource, id, variables }) => ({ data: await api(one(resource, id), { method: 'DELETE', body: variables }) }),

  custom: async ({ url, method, payload, query }) => ({
    data: await api(url + qs((query ?? {}) as Record<string, string>), { method, body: payload }),
  }),
}
