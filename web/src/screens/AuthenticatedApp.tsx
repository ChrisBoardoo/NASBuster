// SPDX-License-Identifier: AGPL-3.0-or-later
// Everything behind login once onboarding is complete: shell (with the alerts bell) +
// lazily loaded screens. Alerts are polled once here and shared by every screen.
import { Suspense, lazy } from 'react'
import { Route, Switch } from 'wouter'
import { AppShell } from '../components/AppShell'
import { InlineLoading } from '../components/Loading'
import { Container } from '../components/Typography'
import { AlertsProvider } from '../session/AlertsContext'

const Dashboard = lazy(() => import('./Dashboard'))
const Storage = lazy(() => import('./Storage'))
const Shares = lazy(() => import('./Shares'))
const Users = lazy(() => import('./Users'))
const Apps = lazy(() => import('./Apps'))
const System = lazy(() => import('./System'))
const Activity = lazy(() => import('./Activity'))
const Journal = lazy(() => import('./Journal'))
const Settings = lazy(() => import('./Settings'))
const Alerts = lazy(() => import('./Alerts'))
const NotFound = lazy(() => import('./NotFound'))

export default function AuthenticatedApp() {
  return (
    <AlertsProvider>
      <AppShell>
        <Suspense
          fallback={
            <Container>
              <InlineLoading className="pt-20" />
            </Container>
          }
        >
          <Switch>
            <Route path="/">
              <Dashboard />
            </Route>
            <Route path="/storage">
              <Storage />
            </Route>
            <Route path="/shares">
              <Shares />
            </Route>
            <Route path="/users">
              <Users />
            </Route>
            <Route path="/apps">
              <Apps />
            </Route>
            <Route path="/system">
              <System />
            </Route>
            <Route path="/activity">
              <Activity />
            </Route>
            <Route path="/journal">
              <Journal />
            </Route>
            <Route path="/alerts">
              <Alerts />
            </Route>
            <Route path="/settings">
              <Settings />
            </Route>
            <Route>
              <NotFound />
            </Route>
          </Switch>
        </Suspense>
      </AppShell>
    </AlertsProvider>
  )
}
