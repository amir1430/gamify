import { CornerDownLeft, Gamepad2, Monitor, Moon, Search, Sun, UserRound, type LucideIcon } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { api, qs } from '@/lib/api'
import { setTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'

export type CommandPage = { to: string; label: string; icon: LucideIcon; group: string }

type Player = { id: number; username: string; display_name: string }
type Entry = { key: string; group: string; label: string; hint?: string; icon: LucideIcon; run: () => void }

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
export const MOD = isMac ? '⌘' : 'Ctrl'

// ⌘K / Ctrl+K (and "/" outside a field) open the palette.
export function useCommandShortcut(open: () => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const field = e.target instanceof HTMLElement && (e.target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName))
      if ((e.key === 'k' && (e.metaKey || e.ctrlKey)) || (e.key === '/' && !field && !e.metaKey && !e.ctrlKey)) {
        e.preventDefault()
        open()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])
}

// Players matching q (the players list's search), debounced.
function usePlayerSearch(q: string) {
  const [rows, setRows] = useState<Player[]>([])
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    const text = q.trim()
    if (text.length < 2 && !/^\d+$/.test(text)) {
      setRows([])
      return
    }
    const ctl = new AbortController()
    const t = setTimeout(() => {
      setBusy(true)
      api<{ items: Player[] }>('/api/users' + qs({ q: text, limit: 6 }), { signal: ctl.signal })
        .then((r) => setRows(r.items))
        .catch(() => setRows([]))
        .finally(() => setBusy(false))
    }, 180)
    return () => (clearTimeout(t), ctl.abort())
  }, [q])
  return { rows, busy }
}

// Game names, fetched when the palette first opens (a viewer's).
function useGameNames(on: boolean) {
  const [names, setNames] = useState<string[] | null>(null)
  useEffect(() => {
    if (!on || names) return
    api<{ items: { name: string }[] }>('/api/games')
      .then((r) => setNames(r.items.map((g) => g.name)))
      .catch(() => setNames([]))
  }, [on, names])
  return names ?? []
}

// Jump anywhere: the pages this admin may open, players by ID, name or Telegram ID, the theme.
export function CommandPalette({ open, onOpenChange, pages, canPlayers }: { open: boolean; onOpenChange: (o: boolean) => void; pages: CommandPage[]; canPlayers: boolean }) {
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const [active, setActive] = useState(0)
  const list = useRef<HTMLDivElement>(null)
  const players = usePlayerSearch(canPlayers && open ? q : '')
  const games = useGameNames(canPlayers && open)

  useEffect(() => {
    if (open) setQ('')
  }, [open])

  const entries = useMemo(() => {
    const go = (to: string) => () => (onOpenChange(false), navigate(to))
    const theme = (t: 'light' | 'dark' | 'system') => () => (setTheme(t), onOpenChange(false))
    const text = q.trim().toLowerCase()
    const match = (s: string) => !text || s.toLowerCase().includes(text)
    const out: Entry[] = []
    for (const p of pages) if (match(p.label) || match(p.group)) out.push({ key: p.to, group: 'Pages', label: p.label, hint: p.group, icon: p.icon, run: go(p.to) })
    if (text) {
      const hits = games.filter((g) => match(g)).slice(0, 6)
      for (const g of hits) out.push({ key: `g-${g}`, group: 'Games', label: g, hint: 'game limits', icon: Gamepad2, run: go(`/games/${encodeURIComponent(g)}`) })
    }
    if (canPlayers) {
      const id = /^#?(\d+)$/.exec(q.trim())?.[1]
      if (id && !players.rows.some((r) => String(r.id) === id))
        out.push({ key: `id-${id}`, group: 'Players', label: `Open player #${id}`, icon: UserRound, run: go(`/players/${id}`) })
      for (const r of players.rows)
        out.push({
          key: `p-${r.id}`,
          group: 'Players',
          label: r.display_name || r.username || `Player #${r.id}`,
          hint: `#${r.id}${r.username ? ` · @${r.username}` : ''}`,
          icon: UserRound,
          run: go(`/players/${r.id}`),
        })
      if (text && !id) out.push({ key: 'search', group: 'Players', label: `Search players for “${q.trim()}”`, icon: Search, run: go(`/players?q=${encodeURIComponent(q.trim())}`) })
    }
    const themes: [string, LucideIcon, 'light' | 'dark' | 'system'][] = [
      ['Light theme', Sun, 'light'],
      ['Dark theme', Moon, 'dark'],
      ['System theme', Monitor, 'system'],
    ]
    for (const [label, icon, t] of themes) if (match(label) || match('theme')) out.push({ key: t, group: 'Preferences', label, icon, run: theme(t) })
    return out
  }, [q, pages, games, players.rows, canPlayers, navigate, onOpenChange])

  useEffect(() => setActive(0), [q, entries.length])
  useEffect(() => {
    list.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [active])

  const groups = [...new Set(entries.map((e) => e.group))]
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent showCloseButton={false} className="top-[15%] translate-y-0 gap-0 overflow-hidden p-0 sm:max-w-xl">
        <DialogTitle className="sr-only">Go to</DialogTitle>
        <DialogDescription className="sr-only">Search pages and players</DialogDescription>
        <div className="flex items-center gap-2 border-b px-3">
          <Search className="size-4 shrink-0 text-muted-foreground" />
          <input
            autoFocus
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'ArrowDown') (e.preventDefault(), setActive((a) => Math.min(a + 1, entries.length - 1)))
              else if (e.key === 'ArrowUp') (e.preventDefault(), setActive((a) => Math.max(a - 1, 0)))
              else if (e.key === 'Enter') (e.preventDefault(), entries[active]?.run())
            }}
            placeholder={canPlayers ? 'Search pages, games, players by ID, name or Telegram ID…' : 'Search pages…'}
            className="h-12 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            aria-label="Search"
            role="combobox"
            aria-expanded
            aria-controls="command-list"
            aria-activedescendant={entries[active] ? `cmd-${active}` : undefined}
          />
          {players.busy && <span className="size-3.5 shrink-0 animate-spin rounded-full border-2 border-muted-foreground/30 border-t-muted-foreground" />}
        </div>
        <div ref={list} id="command-list" role="listbox" className="max-h-[min(60vh,420px)] overflow-y-auto p-2">
          {!entries.length && <div className="py-10 text-center text-sm text-muted-foreground">No results.</div>}
          {groups.map((g) => (
            <div key={g} className="mb-1">
              <div className="px-2 pt-2 pb-1 text-[11px] font-medium tracking-wide text-muted-foreground/70 uppercase">{g}</div>
              {entries.map((e, i) =>
                e.group !== g ? null : (
                  <button
                    key={e.key}
                    id={`cmd-${i}`}
                    data-index={i}
                    role="option"
                    aria-selected={i === active}
                    onMouseMove={() => setActive(i)}
                    onClick={e.run}
                    className={cn('flex w-full items-center gap-3 rounded-md px-2 py-2 text-left text-sm', i === active ? 'bg-accent text-foreground' : 'text-muted-foreground')}
                  >
                    <e.icon className="size-4 shrink-0" />
                    <span className="truncate text-foreground">{e.label}</span>
                    {e.hint && <span className="truncate text-xs text-muted-foreground">{e.hint}</span>}
                    {i === active && <CornerDownLeft className="ml-auto size-3.5 shrink-0" />}
                  </button>
                ),
              )}
            </div>
          ))}
        </div>
        <div className="flex items-center gap-4 border-t bg-muted/30 px-3 py-2 text-[11px] text-muted-foreground">
          <span>
            <Kbd>↑</Kbd> <Kbd>↓</Kbd> to move
          </span>
          <span>
            <Kbd>↵</Kbd> to open
          </span>
          <span>
            <Kbd>esc</Kbd> to close
          </span>
        </div>
      </DialogContent>
    </Dialog>
  )
}

export const Kbd = ({ children, className }: { children: React.ReactNode; className?: string }) => (
  <kbd className={cn('inline-grid h-5 min-w-5 place-items-center rounded border bg-background px-1 font-mono text-[10px] font-medium text-muted-foreground', className)}>{children}</kbd>
)
