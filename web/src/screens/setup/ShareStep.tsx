// SPDX-License-Identifier: AGPL-3.0-or-later
// Onboarding step "share", three screens, one decision each:
//   1. the first NAS user (the account people type on their PC, not the admin);
//   2. the first share on the pool, open to that user;
//   3. the "aha" moment: \\host\share in poster type, how to open it on Windows and macOS,
//      and live detection of the first connection (GET /shares/sessions every 2 s).
import { CircleCheck, Monitor } from 'lucide-react'
import { type ReactNode, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../../api/client'
import { createShare, createUser, getPools, getSessions, getShares, getUsers } from '../../api/endpoints'
import type { Pool } from '../../api/types'
import { useApi } from '../../api/useApi'
import { usePolling } from '../../api/usePolling'
import { Button } from '../../components/Button'
import { Checkbox } from '../../components/Checkbox'
import { CopyText } from '../../components/CopyText'
import { Field } from '../../components/Field'
import { Icon } from '../../components/Icon'
import { InlineLoading, ScanLine } from '../../components/Loading'
import { Notice } from '../../components/Notice'
import { PoolSelect } from '../../components/PoolSelect'
import { RECYCLE_DEFAULT_DAYS } from '../../components/RecycleBin'
import { StatusLabel } from '../../components/StatusLabel'
import { cn } from '../../lib/cn'
import { useErrorMessage } from '../../lib/messages'
import { connectionsTo, dialectLabel, smbUrl, uncPath } from '../../lib/shares'
import {
  NAS_PASSWORD_MAX,
  NAS_PASSWORD_MIN,
  NAS_USER_MAX,
  SHARE_NAME_MAX,
  nasPasswordProblem,
  nasUserProblem,
  shareNameProblem,
} from '../../lib/validation'
import { useReadySession } from '../../session/SessionContext'
import { StepLayout } from './StepLayout'
import { type ShareDraft, useWizard } from './wizard'

const SESSIONS_POLL_MS = 2000

/** Pools a share can go on: mounted ones first. */
function usablePools(pools: readonly Pool[]): Pool[] {
  return [...pools].sort((a, b) => Number(b.status?.mounted ?? false) - Number(a.status?.mounted ?? false))
}

export function ShareStep() {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const { drafts, updateDrafts, setLocalBack, persistQuietly, goNext } = useWizard()
  const draft = drafts.share
  const pools = useApi((signal) => getPools({ signal }), [])
  const [busy, setBusy] = useState(false)

  const update = useCallback(
    (patch: Partial<ShareDraft>) => updateDrafts((d) => ({ ...d, share: { ...d.share, ...patch } })),
    [updateDrafts],
  )

  // Back walks the sub-screens before leaving the step.
  useEffect(() => {
    const previous = draft.screen === 'connect' ? 'share' : draft.screen === 'share' ? 'user' : null
    setLocalBack(previous ? () => update({ screen: previous }) : null)
    return () => setLocalBack(null)
  }, [draft.screen, setLocalBack, update])

  const leave = async (completed: boolean) => {
    setBusy(true)
    await persistQuietly({ currentStep: 'apps', completedStep: completed ? 'share' : undefined })
    goNext()
  }

  if (pools.loading && !pools.data) {
    return (
      <StepLayout
        eyebrow={t('onboarding.share.eyebrow')}
        title={t('onboarding.share.user.title')}
        onContinue={() => undefined}
        continueLabel={t('common.continue')}
        continueDisabled
      >
        <InlineLoading />
      </StepLayout>
    )
  }

  const list = usablePools(pools.data?.pools ?? [])
  if (list.length === 0) {
    return (
      <StepLayout
        key="no-pool"
        eyebrow={t('onboarding.share.eyebrow')}
        title={t('onboarding.share.noPool.title')}
        intro={t('onboarding.share.noPool.body')}
        onContinue={() => void leave(false)}
        continueLabel={t('onboarding.share.noPool.continue')}
        busy={busy}
      >
        {pools.error !== undefined && !pools.data && (
          <Notice status="warn" className="max-w-3xl">
            {errorMessage(pools.error)}
          </Notice>
        )}
      </StepLayout>
    )
  }

  const skipButton = (
    <Button variant="ghost" onClick={() => void leave(false)} disabled={busy}>
      {t('common.skip')}
    </Button>
  )

  if (draft.screen === 'connect' && draft.share && draft.user) {
    return <ConnectScreen share={draft.share} user={draft.user} onDone={() => void leave(true)} busy={busy} />
  }
  if (draft.screen === 'share' && draft.user) {
    return (
      <ShareScreen
        draft={draft}
        pools={list}
        update={update}
        skipButton={skipButton}
        onCreated={(share) => update({ share, screen: 'connect' })}
      />
    )
  }
  return (
    <UserScreen
      draft={draft}
      update={update}
      skipButton={skipButton}
      onReady={(user) => update({ user, password: '', confirm: '', screen: 'share' })}
    />
  )
}

// ---- 1. User ---------------------------------------------------------------

type UserField = 'name' | 'password' | 'confirm'

const USER_FIELD_ERRORS: Record<string, UserField> = {
  'validation.user_name': 'name',
  'users.exists': 'name',
  'users.name_taken': 'name',
  'validation.user_password': 'password',
}

interface UserScreenProps {
  draft: ShareDraft
  update: (patch: Partial<ShareDraft>) => void
  skipButton: ReactNode
  onReady: (user: string) => void
}

function UserScreen({ draft, update, skipButton, onReady }: UserScreenProps) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const users = useApi((signal) => getUsers({ signal }), [])
  const [touched, setTouched] = useState<Record<UserField, boolean>>({ name: false, password: false, confirm: false })
  const [busy, setBusy] = useState(false)
  const [serverError, setServerError] = useState<ApiError | null>(null)
  const refs = {
    name: useRef<HTMLInputElement>(null),
    password: useRef<HTMLInputElement>(null),
    confirm: useRef<HTMLInputElement>(null),
  }

  const nameProblem = nasUserProblem(draft.userName)
  const passwordProblem = nasPasswordProblem(draft.password)
  const problems: Record<UserField, string | null> = {
    name: nameProblem ? t(`users.nameErrors.${nameProblem}`) : null,
    password: passwordProblem
      ? t(`users.passwordErrors.${passwordProblem}`, { min: NAS_PASSWORD_MIN, max: NAS_PASSWORD_MAX })
      : null,
    confirm: draft.confirm === draft.password ? null : t('users.mismatch'),
  }
  const fieldError = (field: UserField) => {
    if (serverError && USER_FIELD_ERRORS[serverError.code] === field) return errorMessage(serverError)
    return touched[field] ? problems[field] : null
  }

  const set = (field: 'userName' | 'password' | 'confirm', value: string) => {
    update({ [field]: value })
    setServerError(null)
  }

  const submit = async () => {
    setTouched({ name: true, password: true, confirm: true })
    const firstInvalid = (Object.keys(problems) as UserField[]).find((field) => problems[field])
    if (firstInvalid) {
      refs[firstInvalid].current?.focus()
      return
    }
    setBusy(true)
    setServerError(null)
    try {
      const user = await createUser({ name: draft.userName, password: draft.password })
      onReady(user.name)
    } catch (error) {
      const apiError = error instanceof ApiError ? error : new ApiError(0, 'unknown', String(error))
      setServerError(apiError)
      const field = USER_FIELD_ERRORS[apiError.code]
      if (field) refs[field].current?.focus()
      setBusy(false)
    }
  }

  const existing = users.data?.users ?? []
  const generalError = serverError && !USER_FIELD_ERRORS[serverError.code] ? serverError : null
  const passwordOk = draft.password.length > 0 && !passwordProblem
  const confirmOk = draft.confirm.length > 0 && draft.confirm === draft.password

  return (
    <StepLayout
      key="user"
      eyebrow={t('onboarding.share.eyebrow')}
      title={t('onboarding.share.user.title')}
      intro={t('onboarding.share.user.intro')}
      onContinue={() => void submit()}
      continueLabel={busy ? t('users.creating') : t('onboarding.share.user.submit')}
      busy={busy}
      secondaryAction={skipButton}
    >
      <div className="grid max-w-5xl gap-x-16 gap-y-8 lg:grid-cols-2">
        <Field
          data-autofocus=""
          inputRef={refs.name}
          label={t('users.name')}
          name="nas-user"
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          maxLength={NAS_USER_MAX + 8}
          className="[&_input]:font-mono [&_input]:text-xl"
          value={draft.userName}
          onChange={(event) => set('userName', event.target.value.toLowerCase())}
          onBlur={() => setTouched((s) => ({ ...s, name: draft.userName.length > 0 || s.name }))}
          hint={t('users.nameHint')}
          error={fieldError('name')}
        />
        <div className="flex flex-col gap-6">
          <Field
            inputRef={refs.password}
            label={t('users.password')}
            name="nas-password"
            type="password"
            autoComplete="new-password"
            value={draft.password}
            onChange={(event) => set('password', event.target.value)}
            hint={
              passwordOk || fieldError('password') ? undefined : (
                <span className="tabular-nums">
                  {t('users.passwordLength', { count: draft.password.length, min: NAS_PASSWORD_MIN })}
                </span>
              )
            }
            success={passwordOk ? t('users.passwordOk') : null}
            error={fieldError('password')}
          />
          <Field
            inputRef={refs.confirm}
            label={t('users.confirm')}
            name="nas-password-confirm"
            type="password"
            autoComplete="new-password"
            value={draft.confirm}
            onChange={(event) => set('confirm', event.target.value)}
            onBlur={() => setTouched((s) => ({ ...s, confirm: draft.confirm.length > 0 || s.confirm }))}
            success={confirmOk ? t('users.match') : null}
            error={fieldError('confirm')}
          />
        </div>
      </div>

      {existing.length > 0 && (
        <Notice
          status="info"
          className="max-w-3xl"
          title={t('onboarding.share.user.existing', { count: existing.length })}
          actions={existing.slice(0, 3).map((user) => (
            <Button key={user.name} variant="ghost" size="sm" className="-ml-3" onClick={() => onReady(user.name)}>
              {t('onboarding.share.user.useExisting', { name: user.name })}
            </Button>
          ))}
        />
      )}
      {generalError && (
        <Notice status="crit" assertive className="max-w-3xl">
          <span className="text-foreground">{errorMessage(generalError)}</span>
        </Notice>
      )}
    </StepLayout>
  )
}

// ---- 2. Share --------------------------------------------------------------

const SHARE_FIELD_ERRORS = new Set(['validation.share_name', 'shares.exists'])

interface ShareScreenProps {
  draft: ShareDraft
  pools: Pool[]
  update: (patch: Partial<ShareDraft>) => void
  skipButton: ReactNode
  onCreated: (share: string) => void
}

function ShareScreen({ draft, pools, update, skipButton, onCreated }: ShareScreenProps) {
  const { t } = useTranslation()
  const { ready } = useReadySession()
  const errorMessage = useErrorMessage()
  const shares = useApi((signal) => getShares({ signal }), [])
  const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState(false)
  const [serverError, setServerError] = useState<ApiError | null>(null)
  const nameRef = useRef<HTMLInputElement>(null)
  const modeLabelId = useId()
  const user = draft.user ?? ''

  const poolName = pools.some((p) => p.name === draft.pool) ? draft.pool : (pools[0]?.name ?? '')
  const existing = shares.data?.shares ?? []
  const problem = shareNameProblem(draft.shareName)
  const taken = existing.some((s) => s.name === draft.shareName)
  const nameError =
    serverError && SHARE_FIELD_ERRORS.has(serverError.code)
      ? errorMessage(serverError)
      : problem && (touched || draft.shareName.length > 0)
        ? t(`shares.nameErrors.${problem}`)
        : taken
          ? t('shares.nameErrors.exists', { name: draft.shareName })
          : null

  const submit = async () => {
    setTouched(true)
    if (problem || taken) {
      nameRef.current?.focus()
      return
    }
    setBusy(true)
    setServerError(null)
    try {
      const share = await createShare({
        name: draft.shareName,
        pool: poolName,
        access: [{ user, mode: draft.mode }],
        recycle: draft.recycle,
        recycleDays: draft.recycle ? RECYCLE_DEFAULT_DAYS : 0,
      })
      onCreated(share.name)
    } catch (error) {
      const apiError = error instanceof ApiError ? error : new ApiError(0, 'unknown', String(error))
      setServerError(apiError)
      if (SHARE_FIELD_ERRORS.has(apiError.code)) nameRef.current?.focus()
      setBusy(false)
    }
  }

  const generalError = serverError && !SHARE_FIELD_ERRORS.has(serverError.code) ? serverError : null
  const reachable = existing.filter((s) => s.access.some((a) => a.user === user))

  return (
    <StepLayout
      key="share"
      eyebrow={t('onboarding.share.eyebrow')}
      title={t('onboarding.share.share.title')}
      intro={t('onboarding.share.share.intro')}
      onContinue={() => void submit()}
      continueLabel={busy ? t('shares.creating') : t('onboarding.share.share.submit')}
      busy={busy}
      secondaryAction={skipButton}
    >
      <div className="grid max-w-5xl gap-x-16 gap-y-10 lg:grid-cols-2">
        <Field
          data-autofocus=""
          inputRef={nameRef}
          label={t('shares.name')}
          name="share-name"
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          maxLength={SHARE_NAME_MAX + 8}
          className="[&_input]:font-mono [&_input]:text-xl"
          value={draft.shareName}
          onChange={(event) => {
            update({ shareName: event.target.value.toLowerCase() })
            setServerError(null)
          }}
          hint={t('shares.nameHint')}
          error={nameError}
        >
          <div className="mt-4 flex flex-col gap-1 border-l-2 border-accent pl-4">
            <span className="label-mono text-muted-foreground">{t('onboarding.share.share.preview')}</span>
            <span className="font-mono text-2xl break-all md:text-3xl" aria-live="polite">
              {uncPath(ready.boot.hostname, draft.shareName || '…')}
            </span>
          </div>
        </Field>

        <div className="flex flex-col gap-8">
          {pools.length > 1 ? (
            <PoolSelect pools={pools} value={poolName} onChange={(pool) => update({ pool })} />
          ) : (
            <div className="flex flex-col gap-2">
              <span className="label-mono text-muted-foreground">{t('shares.pool')}</span>
              <span className="text-2xl font-semibold tracking-tight">{poolName}</span>
            </div>
          )}
          <div className="flex flex-col gap-3">
            <span id={modeLabelId} className="label-mono text-muted-foreground">
              {t('onboarding.share.share.access', { user })}
            </span>
            <ModeChoice value={draft.mode} onChange={(mode) => update({ mode })} labelledBy={modeLabelId} />
          </div>
          <Checkbox
            checked={draft.recycle}
            onChange={(recycle) => update({ recycle })}
            label={t('recycle.onboarding', { count: RECYCLE_DEFAULT_DAYS })}
            description={t('recycle.onboardingHint', { count: RECYCLE_DEFAULT_DAYS })}
          />
        </div>
      </div>

      {reachable.length > 0 && (
        <Notice
          status="info"
          className="max-w-3xl"
          title={t('onboarding.share.share.existing', { user })}
          actions={reachable.slice(0, 3).map((share) => (
            <Button key={share.name} variant="ghost" size="sm" className="-ml-3" onClick={() => onCreated(share.name)}>
              {t('onboarding.share.share.useExisting', { name: share.name })}
            </Button>
          ))}
        />
      )}
      {generalError && (
        <Notice status="crit" assertive className="max-w-3xl">
          <span className="text-foreground">{errorMessage(generalError)}</span>
        </Notice>
      )}
    </StepLayout>
  )
}

/** Read & write / read only, for the one user of the onboarding share. */
function ModeChoice({
  value,
  onChange,
  labelledBy,
}: {
  value: 'rw' | 'ro'
  onChange: (mode: 'rw' | 'ro') => void
  labelledBy: string
}) {
  const { t } = useTranslation()
  return (
    <div role="radiogroup" aria-labelledby={labelledBy} className="flex flex-wrap gap-3">
      {(['rw', 'ro'] as const).map((mode) => {
        const checked = value === mode
        return (
          <button
            key={mode}
            type="button"
            role="radio"
            aria-checked={checked}
            onClick={() => onChange(mode)}
            className={cn(
              'flex min-h-11 items-center gap-3 border px-4 py-3 text-left transition-colors duration-150 ease-crisp',
              checked ? 'border-accent text-foreground' : 'border-border text-muted-foreground hover:border-border-hover',
            )}
          >
            <span
              aria-hidden="true"
              className={cn('flex h-4 w-4 items-center justify-center border', checked ? 'border-accent' : 'border-foreground')}
            >
              {checked && <span className="h-2 w-2 bg-accent" />}
            </span>
            <span className="font-semibold">{t(`shares.mode.${mode}`)}</span>
          </button>
        )
      })}
    </div>
  )
}

// ---- 3. Connect (the "aha" moment) -----------------------------------------

function ConnectScreen({ share, user, onDone, busy }: { share: string; user: string; onDone: () => void; busy: boolean }) {
  const { t } = useTranslation()
  const { ready } = useReadySession()
  const host = ready.boot.hostname
  const path = uncPath(host, share)
  const url = smbUrl(host, share)

  // Poll until the first client shows up on this share, then keep that moment on screen.
  const [found, setFound] = useState<ReturnType<typeof connectionsTo>[number] | null>(null)
  const sessions = usePolling((signal) => getSessions({ signal }), SESSIONS_POLL_MS, found === null)
  useEffect(() => {
    if (found) return
    const first = connectionsTo(share, sessions.data)[0]
    if (first) setFound(first)
  }, [sessions.data, share, found])

  const session = found?.session ?? null
  const details = useMemo(
    () =>
      [session?.user, session?.dialect ? dialectLabel(session.dialect) : null, session?.address].filter(
        (part): part is string => Boolean(part),
      ),
    [session],
  )

  return (
    <StepLayout
      key="connect"
      eyebrow={t('onboarding.share.eyebrow')}
      title={found ? t('onboarding.share.connect.doneTitle') : t('onboarding.share.connect.title')}
      intro={found ? undefined : t('onboarding.share.connect.intro')}
      onContinue={onDone}
      continueLabel={t('common.continue')}
      continueDisabled={!found}
      busy={busy}
      secondaryAction={
        found ? undefined : (
          <Button variant="ghost" onClick={onDone} disabled={busy}>
            {t('onboarding.share.connect.skipTest')}
          </Button>
        )
      }
    >
      <CopyText value={path} size="poster" label={t('common.copyValue', { value: path })} className="max-w-6xl" />

      <div aria-live="polite" className="max-w-5xl">
        {found ? (
          <div className="flex flex-col gap-4 border-l-2 border-status-ok pl-5">
            <p className="flex flex-wrap items-center gap-x-4 gap-y-2 text-[length:clamp(2rem,5vw,3.5rem)] leading-none font-semibold tracking-tighter">
              <Icon icon={CircleCheck} size={44} className="shrink-0 text-status-ok" />
              <span>{t('onboarding.share.connect.connected', { machine: found.connection.machine })}</span>
            </p>
            <p className="flex flex-wrap items-center gap-x-4 gap-y-2 text-muted-foreground">
              {details.length > 0 && <span className="font-mono text-sm">{details.join(' · ')}</span>}
              {session && (
                <StatusLabel
                  status={session.encrypted ? 'ok' : 'warn'}
                  label={session.encrypted ? t('shares.sessions.encrypted') : t('shares.sessions.notEncrypted')}
                />
              )}
            </p>
          </div>
        ) : (
          <div className="flex flex-col gap-3" role="status">
            <span className="label-mono flex items-center gap-3 text-muted-foreground">
              <Icon icon={Monitor} size={14} />
              {t('onboarding.share.connect.waiting')}
            </span>
            <ScanLine className="w-48" />
          </div>
        )}
      </div>

      {!found && (
        <div className="grid max-w-5xl gap-x-16 gap-y-10 md:grid-cols-2">
          <Instructions
            title={t('onboarding.share.connect.windowsTitle')}
            steps={[
              t('onboarding.share.connect.windows1'),
              t('onboarding.share.connect.windows2', { path }),
              t('onboarding.share.connect.windows3', { user }),
            ]}
          />
          <Instructions
            title={t('onboarding.share.connect.macTitle')}
            steps={[
              t('onboarding.share.connect.mac1'),
              t('onboarding.share.connect.mac2', { url }),
              t('onboarding.share.connect.mac3', { user }),
            ]}
          />
        </div>
      )}
    </StepLayout>
  )
}

function Instructions({ title, steps }: { title: string; steps: string[] }) {
  return (
    <section className="flex flex-col gap-4">
      <h2 className="label-mono text-muted-foreground">{title}</h2>
      <ol className="flex flex-col gap-3">
        {steps.map((step, index) => (
          <li key={index} className="grid grid-cols-[2rem_1fr] items-baseline gap-2">
            <span className="font-mono text-sm text-accent tabular-nums">{String(index + 1).padStart(2, '0')}</span>
            <span className="break-words">{step}</span>
          </li>
        ))}
      </ol>
    </section>
  )
}
