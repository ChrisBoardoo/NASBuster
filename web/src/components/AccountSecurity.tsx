// SPDX-License-Identifier: AGPL-3.0-or-later
// Settings → Account (ADR-0037): change one's own password (the current one is asked, the
// other sessions end), and the "Forgot password?" door of the login screen, open by default.
import { type FormEvent, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../api/client'
import { changePassword, getAuthSettings, putAuthSettings } from '../api/endpoints'
import type { AuthSettings } from '../api/types'
import { useApi } from '../api/useApi'
import { useErrorMessage } from '../lib/messages'
import { PASSWORD_MIN } from '../lib/validation'
import { Button } from './Button'
import { Field } from './Field'
import { ConsoleReset } from './ForgotPassword'
import { InlineLoading } from './Loading'
import { Notice } from './Notice'
import { Switch } from './Switch'

type FieldName = 'current' | 'password' | 'confirm'

const FIELD_ERRORS: Record<string, FieldName> = {
  'auth.wrong_password': 'current',
  'validation.password_too_short': 'password',
}

export function ChangePassword() {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [values, setValues] = useState<Record<FieldName, string>>({ current: '', password: '', confirm: '' })
  const [touched, setTouched] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  const [closed, setClosed] = useState<number | null>(null)
  const refs = {
    current: useRef<HTMLInputElement>(null),
    password: useRef<HTMLInputElement>(null),
    confirm: useRef<HTMLInputElement>(null),
  }

  const problems: Record<FieldName, string | null> = {
    current: values.current.length > 0 ? null : t('settings.password.currentRequired'),
    password: values.password.length >= PASSWORD_MIN ? null : t('errors.validation.password_too_short', { min: PASSWORD_MIN }),
    confirm: values.confirm === values.password ? null : t('onboarding.account.confirmMismatch'),
  }
  const fieldError = (field: FieldName): string | null => {
    if (error && FIELD_ERRORS[error.code] === field) return errorMessage(error)
    return touched ? problems[field] : null
  }
  const set = (field: FieldName, value: string) => {
    setValues((v) => ({ ...v, [field]: value }))
    setClosed(null)
    if (error && FIELD_ERRORS[error.code] === field) setError(null)
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (busy) return
    setTouched(true)
    const firstInvalid = (Object.keys(problems) as FieldName[]).find((field) => problems[field])
    if (firstInvalid) {
      refs[firstInvalid].current?.focus()
      return
    }
    setBusy(true)
    setError(null)
    try {
      const res = await changePassword({ currentPassword: values.current, password: values.password })
      setValues({ current: '', password: '', confirm: '' })
      setTouched(false)
      setClosed(res.closedSessions)
    } catch (caught) {
      const apiError = caught instanceof ApiError ? caught : new ApiError(0, 'unknown', String(caught))
      setError(apiError)
      const field = FIELD_ERRORS[apiError.code]
      if (field) refs[field].current?.focus()
    } finally {
      setBusy(false)
    }
  }

  const generalError = error && !FIELD_ERRORS[error.code] ? error : null
  return (
    <form onSubmit={submit} noValidate className="flex max-w-xl flex-col gap-6">
      <h3 className="text-2xl font-semibold tracking-tight md:text-3xl">{t('settings.password.title')}</h3>
      <Field
        inputRef={refs.current}
        label={t('settings.password.current')}
        name="current-password"
        type="password"
        autoComplete="current-password"
        value={values.current}
        onChange={(event) => set('current', event.target.value)}
        error={fieldError('current')}
      />
      <Field
        inputRef={refs.password}
        label={t('settings.password.new')}
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
        label={t('settings.password.confirm')}
        name="confirm-password"
        type="password"
        autoComplete="new-password"
        value={values.confirm}
        onChange={(event) => set('confirm', event.target.value)}
        error={fieldError('confirm')}
      />
      <div aria-live="polite" className="empty:-mt-6">
        {generalError && <Notice status="crit">{errorMessage(generalError)}</Notice>}
        {closed !== null && (
          <Notice status="ok">
            <span className="text-foreground">{t('settings.password.done', { count: closed })}</span>
          </Notice>
        )}
      </div>
      <div>
        <Button type="submit" busy={busy}>
          {busy ? t('settings.password.submitting') : t('settings.password.submit')}
        </Button>
      </div>
    </form>
  )
}

export function PasswordResetSetting() {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const view = useApi((signal) => getAuthSettings({ signal }), [])
  const [saved, setSaved] = useState<AuthSettings | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)

  if (!view.data) {
    if (view.error) return <Notice status="crit">{errorMessage(view.error)}</Notice>
    return <InlineLoading />
  }
  const settings = saved ?? view.data

  const change = async (machineReset: boolean) => {
    setBusy(true)
    setError(null)
    try {
      setSaved(await putAuthSettings({ machineReset }))
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex max-w-2xl flex-col gap-4">
      <h3 className="text-2xl font-semibold tracking-tight md:text-3xl">{t('settings.resetDoor.title')}</h3>
      <Switch
        checked={settings.machineReset}
        onChange={(on) => void change(on)}
        label={t('settings.resetDoor.switch')}
        disabled={busy}
      />
      <p className="text-muted-foreground">{t(settings.machineReset ? 'settings.resetDoor.on' : 'settings.resetDoor.off')}</p>
      {error !== null && <Notice status="crit">{errorMessage(error)}</Notice>}
      <ConsoleReset />
    </div>
  )
}
