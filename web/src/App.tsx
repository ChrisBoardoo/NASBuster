// SPDX-License-Identifier: AGPL-3.0-or-later
// Boot sequence and routing.
//   GET /bootstrap → needsSetup → /setup
//                  → not authenticated → /login
//                  → GET /onboarding not completed → /setup (resumes at currentStep)
//                  → dashboard
// /storage/pools/new, /storage/pools/:name/replace and /import (copy data from a disk) are
// full-screen flows (outside the app shell), like the onboarding.
import { LazyMotion, MotionConfig, domAnimation } from 'motion/react'
import { Suspense, lazy, useEffect } from 'react'
import { Route, Switch, useLocation } from 'wouter'
import { InlineLoading, LoadingScreen } from './components/Loading'
import { BootError } from './screens/BootError'
import { type ReadySession, SessionProvider, useSession } from './session/SessionContext'

const SetupWizard = lazy(() => import('./screens/setup/SetupWizard'))
const Login = lazy(() => import('./screens/Login'))
const AuthenticatedApp = lazy(() => import('./screens/AuthenticatedApp'))
const PoolWizard = lazy(() => import('./screens/pools/PoolWizard'))
const ReplaceWizard = lazy(() => import('./screens/pools/ReplaceWizard'))
const CopyWizard = lazy(() => import('./screens/CopyWizard'))

export function App() {
  return (
    <SessionProvider>
      <LazyMotion features={domAnimation} strict>
        <MotionConfig reducedMotion="user">
          <Boot />
        </MotionConfig>
      </LazyMotion>
    </SessionProvider>
  )
}

function Boot() {
  const { state, reload } = useSession()
  if (state.phase === 'loading') return <LoadingScreen />
  if (state.phase === 'error') return <BootError error={state.error} onRetry={reload} />
  return <Routes session={state} />
}

/** Where the user must be, given the session; null when the current location is fine. */
function redirectFor(session: ReadySession, location: string): string | null {
  const { boot, onboarding } = session
  if (boot.needsSetup) return location === '/setup' ? null : '/setup'
  if (!boot.authenticated) return location === '/login' ? null : '/login'
  if (!onboarding?.completed) return location === '/setup' ? null : '/setup'
  if (location === '/setup' || location === '/login') return '/'
  return null
}

function Routes({ session }: { session: ReadySession }) {
  const [location, navigate] = useLocation()
  const redirect = redirectFor(session, location)

  useEffect(() => {
    if (redirect) navigate(redirect, { replace: true })
  }, [redirect, navigate])

  if (redirect) return <LoadingScreen />

  return (
    <Suspense fallback={<LoadingScreen />}>
      <Switch>
        <Route path="/setup">
          <SetupWizard />
        </Route>
        <Route path="/login">
          <Login />
        </Route>
        <Route path="/storage/pools/new">
          <PoolWizard />
        </Route>
        <Route path="/storage/pools/:name/replace">{(params) => <ReplaceWizard name={params.name} />}</Route>
        <Route path="/import">
          <CopyWizard />
        </Route>
        <Route>
          <Suspense fallback={<InlineLoading className="px-6" />}>
            <AuthenticatedApp />
          </Suspense>
        </Route>
      </Switch>
    </Suspense>
  )
}
