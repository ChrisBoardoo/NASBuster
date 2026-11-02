// SPDX-License-Identifier: AGPL-3.0-or-later
import { ArrowRight } from 'lucide-react'
import { m } from 'motion/react'
import { type FormEvent, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../api/client'
import { getOnboarding, login } from '../api/endpoints'
import { Button } from '../components/Button'
import { Field } from '../components/Field'
import { ForgotPassword } from '../components/ForgotPassword'
import { Icon } from '../components/Icon'
import { Notice } from '../components/Notice'
import { ConflictList } from '../components/RestoreConflicts'
import { LanguageToggle, ThemeToggle } from '../components/Toggles'
import { Container, Eyebrow } from '../components/Typography'
import { Wordmark } from '../components/Wordmark'
import { useCountdown, useDocumentTitle } from '../lib/hooks'
import { useErrorMessage } from '../lib/messages'
import { useReveal } from '../lib/motion'
import { clearRestoreNotice, readRestoreNotice } from '../lib/restore'
import { useReadySession } from '../session/SessionContext'

const DEFAULT_RETRY_SECONDS = 30

export default function Login() {
  const { t } = useTranslation()
  const { ready, signedIn } = useReadySession()
  const errorMessage = useErrorMessage()
  const { container, item } = useReveal()
  useDocumentTitle(t('login.eyebrow'))

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [lockedUntil, setLockedUntil] = useState<number | null>(null)
  const secondsLeft = useCountdown(lockedUntil)
  const usernameRef = useRef<HTMLInputElement>(null)
  const passwordRef = useRef<HTMLInputElement>(null)
  // Just after a configuration restore: say which password counts now, and what was skipped.
  const [restored] = useState(readRestoreNotice)
  const [forgot, setForgot] = useState(false)

  useEffect(() => {
    usernameRef.current?.focus()
  }, [])

  const rateLimited = error instanceof ApiError && error.code === 'auth.rate_limited'
  const locked = lockedUntil !== null && secondsLeft > 0

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (busy || locked || !username || !password) return
    setBusy(true)
    setError(null)
    try {
      const { user } = await login({ username: username.trim(), password })
      const onboarding = await getOnboarding()
      clearRestoreNotice()
      signedIn(user, onboarding) // the router then moves on to /setup or the dashboard
    } catch (caught) {
      setError(caught)
      setPassword('')
      if (caught instanceof ApiError && caught.code === 'auth.rate_limited') {
        const retryAfter = Number(caught.params.retryAfter)
        const seconds = Number.isFinite(retryAfter) && retryAfter > 0 ? retryAfter : DEFAULT_RETRY_SECONDS
        setLockedUntil(Date.now() + seconds * 1000)
      } else {
        passwordRef.current?.focus()
      }
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-dvh flex-col">
      <Container className="flex min-h-16 items-center justify-between gap-4 md:min-h-20">
        <Wordmark />
        <div className="flex items-center gap-1">
          <LanguageToggle />
          <ThemeToggle />
        </div>
      </Container>

      <Container className="flex flex-1 items-center py-12">
        <m.div
          variants={container}
          initial="hidden"
          animate="show"
          className="grid w-full gap-12 lg:grid-cols-12 lg:gap-x-16"
        >
          <m.div variants={item} className="flex flex-col gap-6 lg:col-span-12">
            <Eyebrow>
              <span className="font-mono normal-case">\\{ready.boot.hostname}</span>
            </Eyebrow>
            <h1 className="text-6xl leading-none font-semibold tracking-tighter md:text-8xl lg:text-9xl">
              {t('login.title')}
            </h1>
          </m.div>

          {forgot ? (
            <m.div variants={item} className="lg:col-span-5 lg:col-start-8">
              <ForgotPassword
                onBack={() => {
                  setForgot(false)
                  requestAnimationFrame(() => usernameRef.current?.focus())
                }}
              />
            </m.div>
          ) : (
          <m.form variants={item} onSubmit={submit} noValidate className="flex flex-col gap-6 lg:col-span-5 lg:col-start-8">
            {restored ? (
              <>
                <Notice status="ok" title={t('backup.restoredTitle')}>
                  {t('backup.restoredBody')}
                </Notice>
                {restored.conflicts.length > 0 && <ConflictList conflicts={restored.conflicts} />}
              </>
            ) : (
              ready.expired && !error && <Notice status="info">{t('login.sessionExpired')}</Notice>
            )}
            <Field
              inputRef={usernameRef}
              label={t('login.username')}
              name="username"
              autoComplete="username"
              autoCapitalize="none"
              spellCheck={false}
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              required
            />
            <Field
              inputRef={passwordRef}
              label={t('login.password')}
              name="password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              required
            />
            <div aria-live="assertive" className="empty:-mt-6">
              {error !== null &&
                (rateLimited ? (
                  <Notice status={locked ? 'warn' : 'info'}>
                    {locked ? t('login.rateLimited', { seconds: secondsLeft }) : t('login.rateLimitOver')}
                  </Notice>
                ) : (
                  <Notice status="crit">{errorMessage(error)}</Notice>
                ))}
            </div>
            <div>
              <Button
                type="submit"
                size="lg"
                busy={busy}
                disabled={locked || !username || !password}
                iconEnd={<Icon icon={ArrowRight} />}
              >
                {busy ? t('login.submitting') : t('login.submit')}
              </Button>
            </div>
            <Button variant="ghost" size="sm" className="-ml-3 self-start" onClick={() => setForgot(true)}>
              {t('login.forgot.link')}
            </Button>
          </m.form>
          )}
        </m.div>
      </Container>
    </div>
  )
}
