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
import { Note, PageHeader, notificationProvider, useMe } from './ui'

// meta.perm: what the resource's pages need (the access provider checks it).
const RESOURCES = [
  { name: 'dashboard', list: '/dashboard', meta: { label: 'Dashboard', perm: 'view' } },
  { name: 'games', list: '/games', show: '/games/:id', meta: { label: 'Games', perm: 'view' } },
  { name: 'users', list: '/players', show: '/players/:id', meta: { label: 'Players', perm: 'view' } },
  { name: 'adjustments', list: '/adjustments', meta: { label: 'Adjustments', perm: 'view' } },
  { name: 'audit', list: '/audit', meta: { label: 'Audit log', perm: 'view' } },
  { name: 'admins', list: '/admins', meta: { label: 'Console users', perm: 'admins' } },
]

// Pages still to come (Tasks 14–15): the shell's links land here meanwhile.
const Placeholder = ({ title }: { title: string }) => (
  <>
    <PageHeader title={title} />
    <Note>Not built yet.</Note>
  </>
)

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
            <Route path="/dashboard" element={<Placeholder title="Dashboard" />} />
            <Route path="/games" element={<Placeholder title="Games" />} />
            <Route path="/players" element={<Placeholder title="Players" />} />
            <Route path="/players/:id" element={<Placeholder title="Player" />} />
            <Route path="/adjustments" element={<Placeholder title="Adjustments" />} />
            <Route path="/audit" element={<Placeholder title="Audit log" />} />
            <Route path="/admins" element={<Placeholder title="Console users" />} />
            <Route path="/account" element={<Placeholder title="Account" />} />
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
