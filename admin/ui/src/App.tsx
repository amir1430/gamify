import { Authenticated, Refine } from '@refinedev/core'
import routerProvider, { CatchAllNavigate, DocumentTitleHandler, NavigateToResource } from '@refinedev/react-router'
import { BrowserRouter, Link, Outlet, Route, Routes } from 'react-router'
import { Button } from '@/components/ui/button'
import { Toaster } from '@/components/ui/sonner'
import { Layout } from './layout'
import { Enrol } from './pages/enrol'
import { Login } from './pages/login'
import { accessControlProvider } from './providers/access'
import { authProvider } from './providers/auth'
import { dataProvider } from './providers/data'
import { Account } from './pages/account'
import { Adjustments } from './pages/adjustments'
import { Admins } from './pages/admins'
import { Audit } from './pages/audit'
import { Dashboard } from './pages/dashboard'
import { Games } from './pages/games'
import { Player } from './pages/player'
import { Players } from './pages/players'
import { ViewPage } from './pages/views'
import { notificationProvider, useMe } from './ui'

// meta.perm: what the resource's pages need (the access provider checks it).
const RESOURCES = [
  { name: 'dashboard', list: '/dashboard', meta: { label: 'Dashboard', perm: 'view' } },
  { name: 'games', list: '/games', meta: { label: 'Games', perm: 'view' } },
  { name: 'users', list: '/players', show: '/players/:id', meta: { label: 'Players', perm: 'view' } },
  { name: 'adjustments', list: '/adjustments', meta: { label: 'Adjustments', perm: 'view' } },
  { name: 'audit', list: '/audit', meta: { label: 'Audit log', perm: 'view' } },
  { name: 'admins', list: '/admins', meta: { label: 'Console users', perm: 'admins' } },
  // The brand's views: the API checks each view's own perm (GET /api/views lists what the role may open).
  { name: 'views', list: '/views/:name', meta: { label: 'Views' } },
]

function NotFound() {
  return (
    <div className="grid place-items-center gap-3 py-24 text-center">
      <p className="text-sm text-muted-foreground">Page not found.</p>
      <Button asChild variant="outline" size="sm">
        <Link to="/dashboard">Back to the dashboard</Link>
      </Button>
    </div>
  )
}

function Title() {
  const brand = useMe()?.brand.name || 'Admin'
  // It sets the title on navigation only: keyed by the brand so /api/me arriving sets it too.
  return <DocumentTitleHandler key={brand} handler={({ resource }) => (resource?.meta?.label ? `${resource.meta.label} · ${brand}` : brand)} />
}

export function App() {
  return (
    <BrowserRouter>
      <Refine
        dataProvider={dataProvider}
        authProvider={authProvider}
        accessControlProvider={accessControlProvider}
        routerProvider={routerProvider}
        notificationProvider={notificationProvider}
        resources={RESOURCES}
        options={{ disableTelemetry: true, mutationMode: 'pessimistic' }}
      >
        <Routes>
          <Route
            element={
              <Authenticated key="in" fallback={<CatchAllNavigate to="/login" />}>
                <Layout />
              </Authenticated>
            }
          >
            <Route index element={<NavigateToResource resource="dashboard" />} />
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/games" element={<Games />} />
            <Route path="/players" element={<Players />} />
            <Route path="/players/:id" element={<Player />} />
            <Route path="/adjustments" element={<Adjustments />} />
            <Route path="/audit" element={<Audit />} />
            <Route path="/admins" element={<Admins />} />
            <Route path="/views/:name" element={<ViewPage />} />
            <Route path="/account" element={<Account />} />
            <Route path="*" element={<NotFound />} />
          </Route>
          <Route
            element={
              <Authenticated key="out" fallback={<Outlet />}>
                <NavigateToResource resource="dashboard" />
              </Authenticated>
            }
          >
            <Route path="/login" element={<Login />} />
            <Route path="/enrol" element={<Enrol />} />
          </Route>
        </Routes>
        <Toaster position="bottom-right" />
        <Title />
      </Refine>
    </BrowserRouter>
  )
}
