import { useLogout } from '@refinedev/core'
import { ChevronsUpDown, Gamepad2, LayoutDashboard, LogOut, Monitor, Moon, ScrollText, Scale, Sun, UserCog, UserRound, Users } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { Link, NavLink, Outlet } from 'react-router'
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
import { useMe, usePerm } from './ui'

type Item = { to: string; label: string; icon: typeof Users; perm: string }

// Brand views join Operations by their Group (Task 15).
const GROUPS: { label: string; items: Item[] }[] = [
  { label: 'Overview', items: [{ to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard, perm: 'view' }] },
  {
    label: 'Operations',
    items: [
      { to: '/games', label: 'Games', icon: Gamepad2, perm: 'view' },
      { to: '/players', label: 'Players', icon: Users, perm: 'view' },
      { to: '/adjustments', label: 'Adjustments', icon: Scale, perm: 'view' },
    ],
  },
  {
    label: 'Admin',
    items: [
      { to: '/audit', label: 'Audit log', icon: ScrollText, perm: 'view' },
      { to: '/admins', label: 'Console users', icon: UserCog, perm: 'admins' },
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

function NavItem({ to, label, icon: Icon, count }: Item & { count?: number }) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        cn(
          'flex h-8 shrink-0 items-center gap-2.5 rounded-md px-2.5 text-sm text-muted-foreground transition-colors hover:bg-accent/60 hover:text-foreground',
          isActive && 'bg-accent text-foreground',
        )
      }
    >
      <Icon className="size-4" />
      {label}
      {!!count && <span className="ml-auto rounded-full bg-danger/15 px-1.5 text-[11px] font-medium text-danger tabular-nums">{count}</span>}
    </NavLink>
  )
}

const ROLE: Record<string, string> = { owner: 'Owner', operator: 'Operator', support: 'Support', viewer: 'Viewer · read only' }
const THEMES: { value: Theme; label: string; icon: typeof Sun }[] = [
  { value: 'system', label: 'System', icon: Monitor },
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
]

// The account menu: the sidebar's footer, or (compact) just the avatar in the narrow top bar.
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
          compact ? 'shrink-0 rounded-full' : 'w-full p-2',
        )}
        aria-label="Account"
      >
        <span className="grid size-8 place-items-center rounded-full bg-secondary text-xs font-semibold uppercase">{email.slice(0, 2)}</span>
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

// The nav items this admin may open.
function useGroups() {
  const can = { view: usePerm('view'), admins: usePerm('admins') } as Record<string, boolean>
  return GROUPS.map((g) => ({ ...g, items: g.items.filter((i) => can[i.perm]) })).filter((g) => g.items.length)
}

// Sidebar on desktop; a top bar with the same links on narrow screens.
export function Layout({ children }: { children?: ReactNode }) {
  const me = useMe()
  const groups = useGroups()
  useEffect(() => setAccent(me?.brand.accent ?? ''), [me?.brand.accent])
  const title = me?.brand.name || 'Admin'
  return (
    <div className="min-h-svh md:grid md:grid-cols-[232px_1fr]">
      <aside className="sticky top-0 hidden h-svh flex-col border-r bg-card/40 md:flex">
        <div className="flex h-14 items-center gap-2.5 px-4">
          <Mark logo={me?.brand.logo} />
          <span className="truncate text-sm font-semibold tracking-tight">{title}</span>
        </div>
        <nav className="flex flex-1 flex-col gap-5 overflow-y-auto px-3 py-3" aria-label="Sections">
          {groups.map((g) => (
            <div key={g.label} className="flex flex-col gap-0.5">
              <div className="px-2.5 pb-1.5 text-[11px] font-medium tracking-wide text-muted-foreground/70 uppercase">{g.label}</div>
              {g.items.map((n) => (
                <NavItem key={n.to} {...n} />
              ))}
            </div>
          ))}
        </nav>
        <div className="border-t p-2">
          <Account />
        </div>
      </aside>

      <div className="min-w-0">
        <div className="sticky top-0 z-10 flex items-center gap-3 border-b bg-background/95 px-4 py-2 backdrop-blur md:hidden">
          <Mark logo={me?.brand.logo} />
          <nav className="flex flex-1 gap-1 overflow-x-auto" aria-label="Sections">
            {groups.flatMap((g) => g.items).map((n) => (
              <NavItem key={n.to} {...n} />
            ))}
          </nav>
          <Account compact />
        </div>
        <main className="mx-auto w-full max-w-[1400px] px-4 py-6 md:px-8 md:py-8">{children ?? <Outlet />}</main>
      </div>
    </div>
  )
}
