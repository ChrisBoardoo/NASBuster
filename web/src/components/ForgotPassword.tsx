// SPDX-License-Identifier: AGPL-3.0-or-later
// Login → "Forgot password?" (ADR-0037): the Pi's own account proves ownership, as for
// the first admin (ADR-0018), and the admin gets a new password. When the admin closed
// that door, or the check is unavailable, the console command is the way.
import { type FormEvent, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../api/client'
import { getBootstrap, getOnboarding, resetPassword } from '../api/endpoints'
import type { User } from '../api/types'
import { useApi } from '../api/useApi'
import { RESET_ADMIN_COMMAND } from '../lib/constants'
import { useCountdown } from '../lib/hooks'
import { useErrorMessage } from '../lib/messages'
import { MACHINE_USER_RE, PASSWORD_MIN } from '../lib/validation'
import { useReadySession } from '../session/SessionContext'
import { Button } from './Button'
import { Field } from './Field'
import { Notice } from './Notice'

type FieldName = 'machineUser' | 'machinePassword' | 'username' | 'password' | 'confirm'

/** Server error codes that belong to a specific field. */
const FIELD_ERRORS: Record<string, FieldName> = {
  'auth.invalid_machine_account': 'machinePassword',
  'auth.unknown_admin': 'username',
  'auth.choose_admin': 'username',
  'validation.password_too_short': 'password',
}

const DEFAULT_RETRY_SECONDS = 30

export function ConsoleReset() {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col gap-3">
      <p className="text-muted-foreground">{t('login.forgot.consoleBody')}</p>
      <code className="block overflow-x-auto border border-border bg-muted px-3 py-2 text-sm whitespace-nowrap text-foreground">
        {RESET_ADMIN_COMMAND}
      </code>
      <p className="text-sm text-muted-foreground">{t('login.forgot.consoleHint')}</p>
    </div>
  )
}

export function ForgotPassword({ onBack }: { onBack: () => void }) {
  const { t } = useTranslation()
  const { ready, signedIn } = useReadySession()
  const errorMessage = useErrorMessage()

  const [values, setValues] = useState<Record<FieldName, string>>({
    machineUser: '',
    machinePassword: '',
    username: '',
    password: '',
    confirm: '',
  })
  const [touched, setTouched] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  const [admins, setAdmins] = useState<string[] | null>(null)
  const [done, setDone] = useState<User | null>(null)
  const [lockedUntil, setLockedUntil] = useState<number | null>(null)
  const secondsLeft = useCountdown(lockedUntil)
  const refs = {
    machineUser: useRef<HTMLInputElement>(null),
    machinePassword: useRef<HTMLInputElement>(null),
    username: useRef<HTMLInputElement>(null),
    password: useRef<HTMLInputElement>(null),
    confirm: useRef<HTMLInputElement>(null),
  }

  // Read again on opening: the admin may have closed the door since this page loaded.
  const fresh = useApi((signal) => getBootstrap({ signal }), [])
  const machineReset = fresh.data?.machineReset ?? ready.boot.machineReset
  const closed = machineReset === false || error?.code === 'auth.reset_disabled'
  const locked = lockedUntil !== null && secondsLeft > 0

  const problems: Record<FieldName, string | null> = {
    machineUser: MACHINE_USER_RE.test(values.machineUser) ? null : t('onboarding.account.machineUserInvalid'),
    machinePassword: values.machinePassword.length > 0 ? null : t('onboarding.account.machinePasswordRequired'),
    username: admins === null || values.username.length > 0 ? null : t('login.forgot.adminRequired'),
    password: values.password.length >= PASSWORD_MIN ? null : t('errors.validation.password_too_short', { min: PASSWORD_MIN }),
    confirm: values.confirm === values.password ? null : t('onboarding.account.confirmMismatch'),
  }
  const fieldError = (field: FieldName): string | null => {
    if (error && FIELD_ERRORS[error.code] === field) return errorMessage(error)
    return touched ? problems[field] : null
  }
  const set = (field: FieldName, value: string) => {
    setValues((v) => ({ ...v, [field]: value }))
    if (error && FIELD_ERRORS[error.code] === field) setError(null)
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (busy || locked) return
    setTouched(true)
    const firstInvalid = (Object.keys(problems) as FieldName[]).find((field) => problems[field])
    if (firstInvalid) {
      refs[firstInvalid].current?.focus()
      return
    }
    setBusy(true)
    setError(null)
    try {
      const { user } = await resetPassword({
        machineUser: values.machineUser,
        machinePassword: values.machinePassword,
        username: admins === null ? undefined : values.username,
        password: values.password,
      })
      setValues({ machineUser: '', machinePassword: '', username: '', password: '', confirm: '' })
      setDone(user)
    } catch (caught) {
      const apiError = caught instanceof ApiError ? caught : new ApiError(0, 'unknown', String(caught))
      setError(apiError)
      setValues((v) => ({ ...v, machinePassword: '' }))
      if (apiError.code === 'auth.rate_limited') {
        const retryAfter = Number(apiError.params.retryAfter)
        setLockedUntil(Date.now() + (Number.isFinite(retryAfter) && retryAfter > 0 ? retryAfter : DEFAULT_RETRY_SECONDS) * 1000)
      }
      if (apiError.code === 'auth.choose_admin' || apiError.code === 'auth.unknown_admin') {
        const names = apiError.params.admins
        setAdmins(Array.isArray(names) ? names.map(String) : [])
      }
      const field = FIELD_ERRORS[apiError.code]
      if (field) requestAnimationFrame(() => refs[field].current?.focus())
    } finally {
      setBusy(false)
    }
  }

  // Signed in by the reset: say the username (often forgotten with the password), then go on.
  const proceed = async (user: User) => {
    setBusy(true)
    try {
      signedIn(user, await getOnboarding())
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : new ApiError(0, 'unknown', String(caught)))
      setBusy(false)
    }
  }

  if (done) {
    return (
      <div className="flex flex-col gap-6">
        <Notice status="ok" title={t('login.forgot.doneTitle')}>
          <span className="text-foreground">{t('login.forgot.doneBody', { username: done.username })}</span>
        </Notice>
        {error !== null && <Notice status="crit">{errorMessage(error)}</Notice>}
        <div>
          <Button size="lg" busy={busy} onClick={() => void proceed(done)}>
            {t('common.continue')}
          </Button>
        </div>
      </div>
    )
  }

  if (closed) {
    return (
      <div className="flex flex-col gap-6">
        <h2 className="text-2xl font-semibold tracking-tight">{t('login.forgot.title')}</h2>
        <p className="text-muted-foreground">{t('login.forgot.closed')}</p>
        <ConsoleReset />
        <Button variant="ghost" size="sm" className="-ml-3 self-start" onClick={onBack}>
          {t('login.forgot.back')}
        </Button>
      </div>
    )
  }

  const generalError = error && !FIELD_ERRORS[error.code] ? error : null
  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-6">
      <h2 className="text-2xl font-semibold tracking-tight">{t('login.forgot.title')}</h2>
      <p className="text-muted-foreground">{t('login.forgot.intro')}</p>
      <Field
        inputRef={refs.machineUser}
        label={t('onboarding.account.machineUser')}
        name="pi-username"
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        maxLength={32}
        value={values.machineUser}
        onChange={(event) => set('machineUser', event.target.value.trim().toLowerCase())}
        error={fieldError('machineUser')}
      />
      <Field
        inputRef={refs.machinePassword}
        label={t('onboarding.account.machinePassword')}
        name="pi-password"
        type="password"
        autoComplete="off"
        maxLength={511}
        value={values.machinePassword}
        onChange={(event) => set('machinePassword', event.target.value)}
        error={fieldError('machinePassword')}
        hint={ready.boot.demo ? t('onboarding.account.machineDemo') : t('login.forgot.machineHint')}
      />
      {admins !== null && (
        <Field
          inputRef={refs.username}
          label={t('login.forgot.admin')}
          name="username"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          value={values.username}
          onChange={(event) => set('username', event.target.value.trim().toLowerCase())}
          error={fieldError('username')}
          hint={admins.length > 0 ? t('login.forgot.admins', { names: admins.join(', ') }) : undefined}
        />
      )}
      <Field
        inputRef={refs.password}
        label={t('login.forgot.newPassword')}
        name="new-password"
        type="password"
        autoComplete="new-password"
        value={values.password}
        onChange={(event) => set('password', event.target.value)}
        error={fieldError('password')}
        hint={t('onboarding.account.passwordLength', { count: values.password.length, min: PASSWORD_MIN })}
      />
      <Field
        inputRef={refs.confirm}
        label={t('login.forgot.confirm')}
        name="confirm-password"
        type="password"
        autoComplete="new-password"
        value={values.confirm}
        onChange={(event) => set('confirm', event.target.value)}
        error={fieldError('confirm')}
      />
      <div aria-live="assertive" className="empty:-mt-6">
        {generalError &&
          (generalError.code === 'auth.rate_limited' ? (
            <Notice status={locked ? 'warn' : 'info'}>
              {locked ? t('login.rateLimited', { seconds: secondsLeft }) : t('login.rateLimitOver')}
            </Notice>
          ) : (
            <Notice status="crit">{errorMessage(generalError)}</Notice>
          ))}
        {generalError?.code === 'auth.machine_check_unavailable' && <ConsoleReset />}
      </div>
      <p className="text-sm text-muted-foreground">{t('login.forgot.sessionsEnd')}</p>
      <div className="flex flex-wrap items-center gap-4">
        <Button type="submit" size="lg" busy={busy} disabled={locked}>
          {busy ? t('login.forgot.submitting') : t('login.forgot.submit')}
        </Button>
        <Button variant="ghost" size="sm" onClick={onBack}>
          {t('login.forgot.back')}
        </Button>
      </div>
    </form>
  )
}
