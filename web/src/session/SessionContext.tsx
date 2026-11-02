// SPDX-License-Identifier: AGPL-3.0-or-later
// App-wide session: bootstrap info, current user, onboarding progress, language sync.
import { type ReactNode, createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { setUnauthorizedHandler } from '../api/client'
import { getBootstrap, getOnboarding, logout as apiLogout, patchMe } from '../api/endpoints'
import type { Bootstrap, Language, Onboarding, User } from '../api/types'
import { applyLanguage } from '../i18n'

export type SessionState =
  | { phase: 'loading' }
  | { phase: 'error'; error: unknown }
  | {
      phase: 'ready'
      boot: Bootstrap
      /** null until authenticated */
      onboarding: Onboarding | null
      /** true when the session ended on its own (401), to explain it on the login screen */
      expired: boolean
    }

export type ReadySession = Extract<SessionState, { phase: 'ready' }>

interface SessionApi {
  state: SessionState
  reload: () => void
  /** After login or admin creation: the user is authenticated from now on. */
  signedIn: (user: User, onboarding: Onboarding) => void
  setOnboarding: (onboarding: Onboarding) => void
  setHostname: (hostname: string) => void
  logout: () => Promise<void>
  /** Switches the UI language; when logged in, also saves it to the account. Resolves false if that save failed. */
  changeLanguage: (lang: Language) => Promise<boolean>
}

const SessionContext = createContext<SessionApi | null>(null)

export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SessionState>({ phase: 'loading' })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    const { signal } = controller
    setState({ phase: 'loading' })
    ;(async () => {
      try {
        const boot = await getBootstrap({ signal })
        const onboarding = boot.authenticated && !boot.needsSetup ? await getOnboarding({ signal }) : null
        if (boot.user) await applyLanguage(boot.user.language)
        if (!signal.aborted) setState({ phase: 'ready', boot, onboarding, expired: false })
      } catch (error) {
        if (!signal.aborted) setState({ phase: 'error', error })
      }
    })()
    return () => controller.abort()
  }, [attempt])

  // Any 401 auth.required (expired or revoked session) sends the user back to login.
  useEffect(() => {
    setUnauthorizedHandler(() => {
      setState((prev) =>
        prev.phase === 'ready' && prev.boot.authenticated
          ? {
              ...prev,
              boot: { ...prev.boot, authenticated: false, user: null },
              onboarding: null,
              expired: true,
            }
          : prev,
      )
    })
    return () => setUnauthorizedHandler(null)
  }, [])

  const reload = useCallback(() => setAttempt((n) => n + 1), [])

  const signedIn = useCallback((user: User, onboarding: Onboarding) => {
    setState((prev) =>
      prev.phase === 'ready'
        ? {
            phase: 'ready',
            boot: { ...prev.boot, needsSetup: false, authenticated: true, user },
            onboarding,
            expired: false,
          }
        : prev,
    )
    void applyLanguage(user.language)
  }, [])

  const setOnboarding = useCallback((onboarding: Onboarding) => {
    setState((prev) => (prev.phase === 'ready' ? { ...prev, onboarding } : prev))
  }, [])

  const setHostname = useCallback((hostname: string) => {
    setState((prev) => (prev.phase === 'ready' ? { ...prev, boot: { ...prev.boot, hostname } } : prev))
  }, [])

  const logout = useCallback(async () => {
    try {
      await apiLogout()
    } catch {
      /* the local session is dropped anyway */
    }
    setState((prev) =>
      prev.phase === 'ready'
        ? { ...prev, boot: { ...prev.boot, authenticated: false, user: null }, onboarding: null, expired: false }
        : prev,
    )
  }, [])

  const authenticated = state.phase === 'ready' && state.boot.authenticated

  const changeLanguage = useCallback(
    async (lang: Language) => {
      await applyLanguage(lang)
      if (!authenticated) return true
      try {
        const { user } = await patchMe({ language: lang })
        setState((prev) => (prev.phase === 'ready' ? { ...prev, boot: { ...prev.boot, user } } : prev))
        return true
      } catch {
        return false
      }
    },
    [authenticated],
  )

  const api = useMemo<SessionApi>(
    () => ({ state, reload, signedIn, setOnboarding, setHostname, logout, changeLanguage }),
    [state, reload, signedIn, setOnboarding, setHostname, logout, changeLanguage],
  )

  return <SessionContext.Provider value={api}>{children}</SessionContext.Provider>
}

export function useSession(): SessionApi {
  const context = useContext(SessionContext)
  if (!context) throw new Error('useSession must be used inside <SessionProvider>')
  return context
}

/** For screens rendered only once the session is ready (all routes). */
export function useReadySession(): SessionApi & { ready: ReadySession } {
  const session = useSession()
  if (session.state.phase !== 'ready') throw new Error('Session is not ready')
  return { ...session, ready: session.state }
}
