// SPDX-License-Identifier: AGPL-3.0-or-later
// Step 3: first admin account. Ownership of the Pi is proven with the Pi's own
// account, the one chosen in Raspberry Pi Imager (ADR-0018), or with the setup
// code printed by the installer as a fallback. On success the user is signed in
// and onboarding progress becomes server-side.
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../../api/client'
import { createAdmin, putOnboarding } from '../../api/endpoints'
import type { CreateAdminRequest, Onboarding } from '../../api/types'
import { Button } from '../../components/Button'
import { Field } from '../../components/Field'
import { Notice } from '../../components/Notice'
import { currentLanguage } from '../../i18n'
import { SETUP_CODE_COMMANDS } from '../../lib/constants'
import { useErrorMessage } from '../../lib/messages'
import { MACHINE_USER_RE, PASSWORD_MIN, USERNAME_RE, formatSetupCode, isSetupCodeComplete } from '../../lib/validation'
import { useReadySession } from '../../session/SessionContext'
import { StepLayout } from './StepLayout'
import { type AccountDraft, useWizard } from './wizard'

type FieldName = 'machineUser' | 'machinePassword' | 'setupCode' | 'username' | 'password' | 'confirm'

/** Server error codes that belong to a specific field. */
const FIELD_ERRORS: Record<string, FieldName> = {
  'setup.invalid_machine_account': 'machinePassword',
  'setup.invalid_code': 'setupCode',
  'validation.username': 'username',
  'validation.password_too_short': 'password',
}

const NO_TOUCH: Record<FieldName, boolean> = {
  machineUser: false,
  machinePassword: false,
  setupCode: false,
  username: false,
  password: false,
  confirm: false,
}

export function AccountStep() {
  const { t } = useTranslation()
  const { ready, signedIn, setOnboarding, reload } = useReadySession()
  const { goNext, drafts, updateDrafts, persistQuietly } = useWizard()
  const errorMessage = useErrorMessage()
  const draft = drafts.account

  const [touched, setTouched] = useState<Record<FieldName, boolean>>(NO_TOUCH)
  const [busy, setBusy] = useState(false)
  const [serverError, setServerError] = useState<ApiError | null>(null)
  const refs = {
    machineUser: useRef<HTMLInputElement>(null),
    machinePassword: useRef<HTMLInputElement>(null),
    setupCode: useRef<HTMLInputElement>(null),
    username: useRef<HTMLInputElement>(null),
    password: useRef<HTMLInputElement>(null),
    confirm: useRef<HTMLInputElement>(null),
  }

  // Account already exists (resumed session, or Back from a later step).
  if (ready.boot.authenticated && ready.boot.user) {
    return (
      <StepLayout
        eyebrow={t('onboarding.account.eyebrow')}
        title={t('onboarding.account.title')}
        onContinue={goNext}
        continueLabel={t('common.continue')}
      >
        <Notice status="ok">
          <span className="text-foreground">{t('onboarding.account.done', { username: ready.boot.user.username })}</span>
        </Notice>
      </StepLayout>
    )
  }

  const byMachine = draft.proof === 'machine'

  const set = (field: Exclude<keyof AccountDraft, 'proof'>, value: string) => {
    updateDrafts((d) => ({ ...d, account: { ...d.account, [field]: value } }))
    if (serverError && FIELD_ERRORS[serverError.code] === field) setServerError(null)
  }

  const switchProof = (proof: AccountDraft['proof']) => {
    updateDrafts((d) => ({ ...d, account: { ...d.account, proof, machinePassword: '' } }))
    setServerError(null)
    setTouched((s) => ({ ...s, machineUser: false, machinePassword: false, setupCode: false }))
    // Focus the first field of the other method once it is rendered.
    requestAnimationFrame(() => (proof === 'machine' ? refs.machineUser : refs.setupCode).current?.focus())
  }

  const problems: Record<FieldName, string | null> = {
    machineUser: !byMachine || MACHINE_USER_RE.test(draft.machineUser) ? null : t('onboarding.account.machineUserInvalid'),
    machinePassword: !byMachine || draft.machinePassword.length > 0 ? null : t('onboarding.account.machinePasswordRequired'),
    setupCode: byMachine || isSetupCodeComplete(draft.setupCode) ? null : t('onboarding.account.setupCodeFormat'),
    username: USERNAME_RE.test(draft.username) ? null : t('onboarding.account.usernameInvalid'),
    password: draft.password.length >= PASSWORD_MIN ? null : t('errors.validation.password_too_short', { min: PASSWORD_MIN }),
    confirm: draft.confirm === draft.password ? null : t('onboarding.account.confirmMismatch'),
  }

  const fieldError = (field: FieldName): string | null => {
    if (serverError && FIELD_ERRORS[serverError.code] === field) return errorMessage(serverError)
    return touched[field] ? problems[field] : null
  }

  const submit = async () => {
    setTouched({ machineUser: true, machinePassword: true, setupCode: true, username: true, password: true, confirm: true })
    const firstInvalid = (Object.keys(problems) as FieldName[]).find((field) => problems[field])
    if (firstInvalid) {
      refs[firstInvalid].current?.focus()
      return
    }
    setBusy(true)
    setServerError(null)
    const proof: Pick<CreateAdminRequest, 'machineUser' | 'machinePassword' | 'setupCode'> = byMachine
      ? { machineUser: draft.machineUser, machinePassword: draft.machinePassword }
      : { setupCode: draft.setupCode }
    try {
      const { user } = await createAdmin({
        ...proof,
        username: draft.username,
        password: draft.password,
        language: currentLanguage(),
      })
      const progress: Onboarding = {
        completed: false,
        currentStep: 'identity',
        completedSteps: ['welcome', 'health', 'account'],
      }
      signedIn(user, progress)
      updateDrafts((d) => ({ ...d, account: { ...d.account, machinePassword: '', password: '', confirm: '' } }))
      try {
        setOnboarding(await putOnboarding(progress))
      } catch {
        await persistQuietly({ currentStep: 'identity', completedStep: 'account' })
      }
      goNext()
    } catch (error) {
      const apiError = error instanceof ApiError ? error : new ApiError(0, 'unknown', String(error))
      setServerError(apiError)
      const field = FIELD_ERRORS[apiError.code]
      if (field) refs[field].current?.focus()
      setBusy(false)
    }
  }

  const passwordOk = draft.password.length >= PASSWORD_MIN
  const confirmOk = draft.confirm.length > 0 && draft.confirm === draft.password
  const generalError = serverError && !FIELD_ERRORS[serverError.code] ? serverError : null

  return (
    <StepLayout
      eyebrow={t('onboarding.account.eyebrow')}
      title={t('onboarding.account.title')}
      intro={t(byMachine ? 'onboarding.account.introMachine' : 'onboarding.account.intro')}
      onContinue={() => void submit()}
      continueLabel={busy ? t('onboarding.account.submitting') : t('onboarding.account.submit')}
      busy={busy}
    >
      <div className="grid max-w-5xl gap-x-16 gap-y-10 lg:grid-cols-2">
        {byMachine ? (
          <section className="flex flex-col gap-6" aria-labelledby="proof-title">
            <h3 id="proof-title" className="label-mono text-muted-foreground">
              {t('onboarding.account.machineTitle')}
            </h3>
            <Field
              data-autofocus=""
              inputRef={refs.machineUser}
              label={t('onboarding.account.machineUser')}
              name="pi-username"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              maxLength={32}
              value={draft.machineUser}
              onChange={(event) => set('machineUser', event.target.value.trim().toLowerCase())}
              onBlur={() => setTouched((s) => ({ ...s, machineUser: draft.machineUser.length > 0 || s.machineUser }))}
              error={fieldError('machineUser')}
            />
            <Field
              inputRef={refs.machinePassword}
              label={t('onboarding.account.machinePassword')}
              name="pi-password"
              type="password"
              autoComplete="off"
              maxLength={511}
              value={draft.machinePassword}
              onChange={(event) => set('machinePassword', event.target.value)}
              error={fieldError('machinePassword')}
            />
            <p className="text-sm text-muted-foreground">{t('onboarding.account.machineHelp')}</p>
            {ready.boot.demo && <p className="text-sm text-foreground">{t('onboarding.account.machineDemo')}</p>}
            <Button variant="ghost" size="sm" className="-ml-3 self-start" onClick={() => switchProof('code')}>
              {t('onboarding.account.useCode')}
            </Button>
          </section>
        ) : (
          <section className="flex flex-col gap-4" aria-labelledby="proof-title">
            <h3 id="proof-title" className="label-mono text-muted-foreground">
              {t('onboarding.account.codeTitle')}
            </h3>
            <Field
              data-autofocus=""
              inputRef={refs.setupCode}
              label={t('onboarding.account.setupCode')}
              mono
              name="setup-code"
              placeholder="XXXX-XXXX"
              autoComplete="off"
              autoCapitalize="characters"
              spellCheck={false}
              maxLength={9}
              value={draft.setupCode}
              onChange={(event) => set('setupCode', formatSetupCode(event.target.value))}
              onBlur={() => setTouched((s) => ({ ...s, setupCode: true }))}
              error={fieldError('setupCode')}
              className="[&_input]:text-2xl [&_input]:uppercase md:[&_input]:text-3xl"
            />
            <div className="flex flex-col gap-2 text-sm text-muted-foreground">
              <p>{t('onboarding.account.setupCodeHelp')}</p>
              <ul className="flex flex-col gap-1">
                {SETUP_CODE_COMMANDS.map((command) => (
                  <li key={command}>
                    <code className="block overflow-x-auto border border-border bg-muted px-3 py-2 text-xs whitespace-nowrap text-foreground">
                      {command}
                    </code>
                  </li>
                ))}
              </ul>
            </div>
            <Button variant="ghost" size="sm" className="-ml-3 self-start" onClick={() => switchProof('machine')}>
              {t('onboarding.account.useMachine')}
            </Button>
          </section>
        )}

        <section className="flex flex-col gap-6" aria-labelledby="admin-title">
          <h3 id="admin-title" className="label-mono text-muted-foreground">
            {t('onboarding.account.adminTitle')}
          </h3>
          <Field
            inputRef={refs.username}
            label={t('onboarding.account.username')}
            name="username"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            maxLength={32}
            value={draft.username}
            onChange={(event) => set('username', event.target.value.toLowerCase())}
            onBlur={() => setTouched((s) => ({ ...s, username: draft.username.length > 0 || s.username }))}
            hint={t('onboarding.account.usernameHint')}
            error={fieldError('username')}
          />
          <Field
            inputRef={refs.password}
            label={t('onboarding.account.password')}
            name="new-password"
            type="password"
            autoComplete="new-password"
            value={draft.password}
            onChange={(event) => set('password', event.target.value)}
            hint={
              passwordOk || fieldError('password') ? undefined : (
                <span className="tabular-nums">
                  {t('onboarding.account.passwordLength', { count: draft.password.length, min: PASSWORD_MIN })}
                </span>
              )
            }
            success={passwordOk ? t('onboarding.account.passwordLong') : null}
            error={fieldError('password')}
          />
          <Field
            inputRef={refs.confirm}
            label={t('onboarding.account.confirm')}
            name="confirm-password"
            type="password"
            autoComplete="new-password"
            value={draft.confirm}
            onChange={(event) => set('confirm', event.target.value)}
            onBlur={() => setTouched((s) => ({ ...s, confirm: draft.confirm.length > 0 || s.confirm }))}
            success={confirmOk ? t('onboarding.account.confirmMatch') : null}
            error={fieldError('confirm')}
          />
        </section>
      </div>

      {generalError && (
        <Notice
          status="crit"
          assertive
          className="max-w-3xl"
          actions={
            generalError.code === 'setup.already_done' ? (
              <Button variant="ghost" size="sm" className="-ml-3" onClick={reload}>
                {t('common.continue')}
              </Button>
            ) : generalError.code === 'setup.machine_check_unavailable' && byMachine ? (
              <Button variant="ghost" size="sm" className="-ml-3" onClick={() => switchProof('code')}>
                {t('onboarding.account.useCode')}
              </Button>
            ) : undefined
          }
        >
          <span className="text-foreground">{errorMessage(generalError)}</span>
        </Notice>
      )}
    </StepLayout>
  )
}
