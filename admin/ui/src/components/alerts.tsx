// Alerts the modules register in Go (admin.Alert, GET /api/alerts): counts of what an operator
// should look at. The dashboard lists the ones that aren't zero; a count with a view badges that
// view in the menu.
import { useCustom } from '@refinedev/core'
import { AlertTriangle, ArrowRight, BellRing, CircleAlert } from 'lucide-react'
import { Link } from 'react-router'
import { count } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Card, Faint } from '../ui'

export type Alert = {
  name: string
  title: string
  hint: string
  tone: 'warn' | 'danger'
  view: string
  filters: Record<string, string>
  count: number | null
  error: string | null
}

const REFRESH_MS = 15_000

export function useAlerts(): Alert[] | undefined {
  const { query } = useCustom<{ items: Alert[] }>({ url: '/api/alerts', method: 'get', queryOptions: { refetchInterval: REFRESH_MS, retry: false }, errorNotification: false })
  return query.data?.data.items
}

// Where an alert opens: its view filtered as it says, or nowhere.
export function alertLink(a: Alert): string | undefined {
  if (!a.view) return undefined
  const q = new URLSearchParams(a.filters).toString()
  return `/views/${a.view}${q ? `?${q}` : ''}`
}

// The menu's count per view: the sum of its alerts' counts, a view without any left out.
export function viewBadges(alerts: Alert[] | undefined): Record<string, { count: number; tone: Alert['tone'] }> {
  const out: Record<string, { count: number; tone: Alert['tone'] }> = {}
  for (const a of alerts ?? []) {
    if (!a.view || !a.count) continue
    const b = (out[a.view] ??= { count: 0, tone: 'warn' })
    b.count += a.count
    if (a.tone === 'danger') b.tone = 'danger'
  }
  return out
}

// The alerts that need someone, worst first; none: the card isn't drawn.
export function AlertsCard({ className }: { className?: string }) {
  const open = (useAlerts() ?? []).filter((a) => a.error || a.count).sort((a, b) => Number(b.tone === 'danger') - Number(a.tone === 'danger'))
  if (!open.length) return null
  return (
    <Card className={className} title="Needs attention" icon={BellRing} bodyClassName="p-0">
      <ul className="divide-y">
        {open.map((a) => {
          const to = alertLink(a)
          const body = (
            <>
              {a.tone === 'danger' ? <CircleAlert className="size-4 shrink-0 text-danger" /> : <AlertTriangle className="size-4 shrink-0 text-warn" />}
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium">{a.title}</span>
                {a.hint && <span className="block truncate text-xs text-muted-foreground">{a.hint}</span>}
              </span>
              {a.error ? (
                <Faint>unavailable</Faint>
              ) : (
                <span className={cn('num text-lg font-semibold', a.tone === 'danger' ? 'text-danger' : 'text-warn')}>{count(a.count ?? 0)}</span>
              )}
              {to && <ArrowRight className="size-3.5 shrink-0 text-muted-foreground" />}
            </>
          )
          return (
            <li key={a.name}>
              {to ? (
                <Link to={to} className="flex items-center gap-3 px-4 py-3 transition-colors hover:bg-accent/50">
                  {body}
                </Link>
              ) : (
                <div className="flex items-center gap-3 px-4 py-3">{body}</div>
              )}
            </li>
          )
        })}
      </ul>
    </Card>
  )
}
