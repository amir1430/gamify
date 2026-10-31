import { useCustom, useLogout } from '@refinedev/core'
import {
  ChevronsUpDown,
  Gamepad2,
  LayoutDashboard,
  LayoutList,
  LogOut,
  Menu,
  Monitor,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Radio,
  Scale,
  ScrollText,
  Search,
  Server,
  Sun,
  UserCog,
  UserRound,
  Users,
} from 'lucide-react'
import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { Link, NavLink, Outlet, useLocation } from 'react-router'
import { CommandPalette, Kbd, MOD, useCommandShortcut, type CommandPage } from '@/components/command'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { setAccent, setTheme, useTheme, type Theme } from '@/lib/theme'
import { cn } from '@/lib/utils'
import { groupViews } from '@/lib/views'
import { useViews } from './pages/views'
import { useMe, usePaged, usePerm } from './ui'

type Item = { to: string; label: string; icon: typeof Users; perm: string; count?: number; tone?: 'danger' | 'ok' }

// The platform's pages; the brand's views (GET /api/views) come between Operations and Admin, a
// section per Group.
const GROUPS: { label: string; items: Item[] }[] = [
  {
    label: 'Overview',
    items: [
      { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard, perm: 'view' },
      { to: '/online', label: 'Online now', icon: Radio, perm: 'view' },
    ],
  },
  {
    label: 'Operations',
    items: [
      { to: '/players', label: 'Players', icon: Users, perm: 'view' },
      { to: '/games', label: 'Games', icon: Gamepad2, perm: 'view' },
      { to: '/adjustments', label: 'Adjustments', icon: Scale, perm: 'view' },
    ],
  },
  {
    label: 'Admin',
    items: [
      { to: '/audit', label: 'Audit log', icon: ScrollText, perm: 'view' },
      { to: '/admins', label: 'Console users', icon: UserCog, perm: 'admins' },
      { to: '/system', label: 'System', icon: Server, perm: 'view' },
    ],
  },
]

// The brand's logo (Brand.Logo, served by the API), or the casino's ring mark in the accent.
export function Mark({ logo }: { logo?: string | null }) {
  const [broken, setBroken] = useState(false)
  if (logo && !broken) return <img src={logo} alt="" className="size-7 rounded-md object-contain" onError={() => setBroken(true)} />
  return (
    <span className="grid size-7 place-items-center rounded-md bg-brand/10 ring-1 ring-brand/25 ring-inset">
      <span className="size-3 rounded-full border-2 border-brand" />
    </span>
  )
}

function NavItem({ to, label, icon: Icon, count, tone = 'danger', collapsed }: Item & { collapsed?: boolean }) {
  return (
    <NavLink
      to={to}
      title={collapsed ? label : undefined}
      className={({ isActive }) =>
        cn(
          'group relative flex h-8 shrink-0 items-center gap-2.5 rounded-md px-2.5 text-sm text-muted-foreground transition-colors hover:bg-accent/60 hover:text-foreground',
          isActive && 'bg-accent font-medium text-foreground',
          collapsed && 'justify-center px-0',
        )
      }
    >
      {({ isActive }) => (
        <>
          {isActive && <span className="absolute top-1.5 bottom-1.5 left-0 w-0.5 rounded-full bg-brand" />}
          <Icon className={cn('size-4 shrink-0', isActive && 'text-brand')} />
          {!collapsed && <span className="truncate">{label}</span>}
          {!!count &&
            (collapsed ? (
              <span className={cn('absolute top-1 right-1 size-2 rounded-full', tone === 'ok' ? 'bg-ok' : 'bg-danger')} />
            ) : (
              <span
                className={cn(
                  'ml-auto rounded-full px-1.5 text-[11px] font-medium tabular-nums',
                  tone === 'ok' ? 'bg-ok/15 text-ok' : 'bg-danger/15 text-danger',
                )}
              >
                {count.toLocaleString()}
              </span>
            ))}
        </>
      )}
    </NavLink>
  )
}

const ROLE: Record<string, string> = { owner: 'Owner', operator: 'Operator', support: 'Support', viewer: 'Viewer · read only' }
const THEMES: { value: Theme; label: string; icon: typeof Sun }[] = [
  { value: 'system', label: 'System', icon: Monitor },
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
]

// The account menu: the sidebar's footer, or (compact) just the avatar.
function Account({ compact }: { compact?: boolean }) {
  const me = useMe()
  const { mutate: logout } = useLogout()
  const [theme] = useTheme()
  if (!me) return null
  const { email, role } = me.user
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className={cn(
          'flex items-center gap-2.5 rounded-md text-left outline-none hover:bg-accent/60 focus-visible:ring-2 focus-visible:ring-ring',
          compact ? 'shrink-0 justify-center rounded-full p-0.5' : 'w-full p-2',
        )}
        aria-label="Account"
      >
        <span className="grid size-8 shrink-0 place-items-center rounded-full bg-brand/15 text-xs font-semibold text-brand uppercase ring-1 ring-brand/25 ring-inset">
          {email.slice(0, 2)}
        </span>
        {!compact && (
          <>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm font-medium">{email}</span>
              <span className="block truncate text-xs text-muted-foreground">{ROLE[role] ?? role}</span>
            </span>
            <ChevronsUpDown className="size-4 text-muted-foreground" />
          </>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent side={compact ? 'bottom' : 'top'} align={compact ? 'end' : 'start'} className="w-56">
        {compact && (
          <>
            <DropdownMenuLabel className="font-normal">
              <span className="block truncate text-sm">{email}</span>
              <span className="block truncate text-xs text-muted-foreground">{ROLE[role] ?? role}</span>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
          </>
        )}
        <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">Theme</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as Theme)}>
          {THEMES.map((t) => (
            <DropdownMenuRadioItem key={t.value} value={t.value} onSelect={(e) => e.preventDefault()}>
              <t.icon /> {t.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/account">
            <UserRound /> Account
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => logout()}>
          <LogOut /> Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

const ONLINE_MS = 30_000

// Players connected right now (GET /api/online's total), refreshed every 30 s.
function useOnlineTotal(enabled: boolean) {
  const { query } = useCustom<{ total: number }>({
    url: '/api/online',
    method: 'get',
    config: { query: { limit: 1 } },
    queryOptions: { enabled, refetchInterval: ONLINE_MS, retry: false },
    errorNotification: false,
  })
  return query.data?.data.total
}

// The nav items this admin may open, with a count of adjustments waiting for an approver.
function useGroups(online?: number) {
  const can = { view: usePerm('view'), admins: usePerm('admins') } as Record<string, boolean>
  const approver = usePerm('approve')
  const me = useMe()
  const waiting = usePaged<{ requested_by: number }>('adjustments', { status: 'pending_approval' }, { enabled: approver, quiet: true })
  const count = waiting.rows?.filter((a) => a.requested_by !== me?.user.id).length
  // The API lists only the views this admin's role may open: no perm to check here.
  const brand = groupViews(useViews() ?? []).map((g) => ({
    label: g.label,
    items: g.views.map((v): Item => ({ to: `/views/${v.name}`, label: v.title, icon: LayoutList, perm: '' })),
  }))
  return [...GROUPS.slice(0, 2), ...brand, ...GROUPS.slice(2)]
    .map((g) => ({
      ...g,
      items: g.items
        .filter((i) => !i.perm || can[i.perm])
        .map((i) =>
          i.to === '/adjustments' && approver ? { ...i, count } : i.to === '/online' ? { ...i, count: online, tone: 'ok' as const } : i,
        ),
    }))
    .filter((g) => g.items.length)
}

const COLLAPSE_KEY = 'gamify.admin.sidebar'
function readCollapsed() {
  try {
    return localStorage.getItem(COLLAPSE_KEY) === 'collapsed'
  } catch {
    return false
  }
}

function Nav({ groups, collapsed }: { groups: ReturnType<typeof useGroups>; collapsed?: boolean }) {
  return (
    <nav className={cn('flex flex-1 flex-col gap-5 overflow-y-auto py-3', collapsed ? 'px-2' : 'px-3')} aria-label="Sections">
      {groups.map((g) => (
        <div key={g.label} className="flex flex-col gap-0.5">
          {collapsed ? (
            <div className="mx-2 mb-1.5 border-t first:hidden" />
          ) : (
            <div className="px-2.5 pb-1.5 text-[11px] font-medium tracking-wide text-muted-foreground/70 uppercase">{g.label}</div>
          )}
          {g.items.map((n) => (
            <NavItem key={n.to} {...n} collapsed={collapsed} />
          ))}
        </div>
      ))}
    </nav>
  )
}

function SearchButton({ onClick, collapsed }: { onClick: () => void; collapsed?: boolean }) {
  if (collapsed)
    return (
      <button onClick={onClick} title={`Search (${MOD}K)`} className="mx-auto grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-accent/60 hover:text-foreground">
        <Search className="size-4" />
      </button>
    )
  return (
    <button
      onClick={onClick}
      className="flex h-8 w-full items-center gap-2 rounded-md border bg-background/60 px-2.5 text-sm text-muted-foreground shadow-xs transition-colors hover:border-brand/40 hover:text-foreground"
    >
      <Search className="size-3.5" />
      <span>Search…</span>
      <span className="ml-auto flex gap-0.5">
        <Kbd>{MOD}</Kbd>
        <Kbd>K</Kbd>
      </span>
    </button>
  )
}

function ThemeToggle() {
  const [, resolved] = useTheme()
  const next = resolved === 'dark' ? 'light' : 'dark'
  return (
    <button
      onClick={() => setTheme(next)}
      title={`Switch to ${next} theme`}
      aria-label={`Switch to ${next} theme`}
      className="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-accent/60 hover:text-foreground"
    >
      {resolved === 'dark' ? <Sun className="size-4" /> : <Moon className="size-4" />}
    </button>
  )
}

// Sidebar on desktop (collapsible to icons, remembered); a top bar and a drawer on narrow screens.
// ⌘K opens the command palette everywhere.
export function Layout({ children }: { children?: ReactNode }) {
  const me = useMe()
  const canView = usePerm('view')
  const online = useOnlineTotal(canView)
  const groups = useGroups(online)
  const [collapsed, setCollapsed] = useState(readCollapsed)
  const [drawer, setDrawer] = useState(false)
  const [palette, setPalette] = useState(false)
  const { pathname } = useLocation()
  useEffect(() => setAccent(me?.brand.accent ?? ''), [me?.brand.accent])
  useEffect(() => setDrawer(false), [pathname])
  useCommandShortcut(useCallback(() => setPalette(true), []))
  const toggle = () =>
    setCollapsed((c) => {
      try {
        localStorage.setItem(COLLAPSE_KEY, c ? 'open' : 'collapsed')
      } catch {
        // storage blocked: this page load only
      }
      return !c
    })
  const title = me?.brand.name || 'Admin'
  const pages: CommandPage[] = [
    ...groups.flatMap((g) => g.items.map((i) => ({ to: i.to, label: i.label, icon: i.icon, group: g.label }))),
    { to: '/account', label: 'Account', icon: UserRound, group: 'You' },
  ]

  return (
    <div className={cn('min-h-svh md:grid', collapsed ? 'md:grid-cols-[60px_1fr]' : 'md:grid-cols-[248px_1fr]')}>
      <aside className="sticky top-0 hidden h-svh flex-col border-r bg-card/60 backdrop-blur md:flex">
        <div className={cn('flex h-14 items-center gap-2.5 border-b', collapsed ? 'justify-center px-2' : 'px-4')}>
          <Mark logo={me?.brand.logo} />
          {!collapsed && (
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm font-semibold tracking-tight">{title}</span>
              <span className="block text-[11px] text-muted-foreground">Admin console</span>
            </span>
          )}
        </div>
        <div className={cn('pt-3', collapsed ? 'px-2' : 'px-3')}>
          <SearchButton onClick={() => setPalette(true)} collapsed={collapsed} />
        </div>
        <Nav groups={groups} collapsed={collapsed} />
        <div className={cn('flex flex-col gap-1 border-t p-2', collapsed && 'items-center')}>
          <button
            onClick={toggle}
            className={cn(
              'flex h-8 items-center gap-2.5 rounded-md px-2.5 text-xs text-muted-foreground hover:bg-accent/60 hover:text-foreground',
              collapsed && 'justify-center px-0 size-8',
            )}
            title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          >
            {collapsed ? <PanelLeftOpen className="size-4" /> : <PanelLeftClose className="size-4" />}
            {!collapsed && 'Collapse'}
          </button>
          <Account compact={collapsed} />
        </div>
      </aside>

      <div className="flex min-w-0 flex-col">
        <header className="sticky top-0 z-20 flex h-14 items-center gap-2 border-b bg-background/80 px-4 backdrop-blur md:px-8">
          <button
            className="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-accent/60 md:hidden"
            onClick={() => setDrawer(true)}
            aria-label="Open menu"
          >
            <Menu className="size-5" />
          </button>
          <span className="flex items-center gap-2 md:hidden">
            <Mark logo={me?.brand.logo} />
            <span className="truncate text-sm font-semibold">{title}</span>
          </span>
          <div className="ml-auto flex items-center gap-1.5">
            {online != null && (
              <Link
                to="/online"
                className="hidden items-center gap-2 rounded-full border bg-card px-3 py-1 text-xs text-muted-foreground shadow-xs hover:text-foreground sm:flex"
                title="Players connected right now"
              >
                <span className="relative size-2 rounded-full bg-ok">
                  <span className="absolute inset-0 animate-ping rounded-full bg-ok/60" />
                </span>
                <span className="num font-medium text-foreground">{online.toLocaleString()}</span> online
              </Link>
            )}
            <button
              onClick={() => setPalette(true)}
              className="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-accent/60 hover:text-foreground md:hidden"
              aria-label="Search"
            >
              <Search className="size-4" />
            </button>
            <ThemeToggle />
            <span className="md:hidden">
              <Account compact />
            </span>
          </div>
        </header>
        <main className="mx-auto w-full max-w-[1400px] flex-1 px-4 py-6 md:px-8 md:py-8">{children ?? <Outlet />}</main>
      </div>

      <Dialog open={drawer} onOpenChange={setDrawer}>
        <DialogContent className="top-0 left-0 h-svh max-w-[280px] translate-x-0 translate-y-0 gap-0 rounded-none border-0 border-r p-0 data-[state=closed]:slide-out-to-left data-[state=open]:slide-in-from-left sm:max-w-[280px]">
          <DialogTitle className="sr-only">Menu</DialogTitle>
          <DialogDescription className="sr-only">Sections of the console</DialogDescription>
          <div className="flex h-14 items-center gap-2.5 border-b px-4">
            <Mark logo={me?.brand.logo} />
            <span className="truncate text-sm font-semibold">{title}</span>
          </div>
          <div className="flex min-h-0 flex-col">
            <Nav groups={groups} />
          </div>
        </DialogContent>
      </Dialog>
      <CommandPalette open={palette} onOpenChange={setPalette} pages={pages} canPlayers={canView} />
    </div>
  )
}
