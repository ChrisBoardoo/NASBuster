// SPDX-License-Identifier: AGPL-3.0-or-later
// NAS users: the people who open shares from their computers (not admins of this UI).
// Create, change password, delete (typed-name confirmation; their files stay in the shares).
import { KeyRound, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../api/client'
import { createUser, deleteUser, getUsers, setUserPassword } from '../api/endpoints'
import type { NasUser } from '../api/types'
import { useApi } from '../api/useApi'
import { Button } from '../components/Button'
import { Field } from '../components/Field'
import { Icon } from '../components/Icon'
import { InlineLoading } from '../components/Loading'
import { ModeLabel } from '../components/ModeLabel'
import { Notice } from '../components/Notice'
import { Container, PageHeader, SectionTitle } from '../components/Typography'
import { useDocumentTitle } from '../lib/hooks'
import { useErrorMessage } from '../lib/messages'
import { useFormat } from '../lib/useFormat'
import { NAS_PASSWORD_MAX, NAS_PASSWORD_MIN, NAS_USER_MAX, nasPasswordProblem, nasUserProblem } from '../lib/validation'

export default function Users() {
  const { t } = useTranslation()
  useDocumentTitle(t('nav.users'))
  const errorMessage = useErrorMessage()
  const users = useApi((signal) => getUsers({ signal }), [])
  const [creating, setCreating] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  const list = users.data?.users ?? []

  return (
    <Container>
      <PageHeader
        eyebrow={t('users.eyebrow')}
        title={t('users.title')}
        intro={t('users.intro')}
        aside={
          <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
            <Button variant="secondary" size="sm" icon={<Icon icon={RefreshCw} />} onClick={users.reload} busy={users.loading}>
              {t('common.refresh')}
            </Button>
            {!creating && (
              <Button size="sm" icon={<Icon icon={Plus} />} onClick={() => setCreating(true)}>
                {t('users.newUser')}
              </Button>
            )}
          </div>
        }
      />

      {creating && (
        <NewUser
          onCancel={() => setCreating(false)}
          onCreated={(user) => {
            setCreating(false)
            setNotice(t('users.created', { name: user.name }))
            users.reload()
          }}
        />
      )}
      {notice && (
        <Notice status="ok" className="mb-10">
          {notice}
        </Notice>
      )}

      <section aria-labelledby="users-list" className="flex flex-col gap-6">
        <SectionTitle id="users-list" title={t('users.listTitle')} />
        {users.loading && !users.data ? (
          <InlineLoading />
        ) : users.error && !users.data ? (
          <Notice
            status="warn"
            title={t('users.loadFailed')}
            actions={
              <Button variant="ghost" size="sm" className="-ml-3" onClick={users.reload}>
                {t('common.retry')}
              </Button>
            }
          >
            {errorMessage(users.error)}
          </Notice>
        ) : list.length === 0 ? (
          <div className="flex flex-col items-start gap-5 py-6">
            <p className="text-4xl font-semibold tracking-tight md:text-5xl">{t('users.empty.title')}</p>
            <p className="max-w-xl text-lg text-muted-foreground">{t('users.empty.body')}</p>
            {!creating && (
              <Button size="lg" icon={<Icon icon={Plus} />} onClick={() => setCreating(true)}>
                {t('users.newUser')}
              </Button>
            )}
          </div>
        ) : (
          <ul className="border-b border-border">
            {list.map((user) => (
              <UserRow
                key={user.name}
                user={user}
                onChanged={(message) => {
                  setNotice(message)
                  users.reload()
                }}
              />
            ))}
          </ul>
        )}
      </section>
    </Container>
  )
}

// ---- Shared password fields ------------------------------------------------------

function usePasswordPair() {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [touched, setTouched] = useState(false)
  const problem = nasPasswordProblem(password)
  const passwordError =
    touched && problem ? t(`users.passwordErrors.${problem}`, { min: NAS_PASSWORD_MIN, max: NAS_PASSWORD_MAX }) : null
  const confirmError = touched && confirm !== password ? t('users.mismatch') : null
  return {
    password,
    confirm,
    setPassword,
    setConfirm,
    touch: () => setTouched(true),
    valid: !problem && confirm === password,
    passwordError,
    confirmError,
    passwordOk: password.length > 0 && !problem,
    confirmOk: confirm.length > 0 && confirm === password,
    reset: () => {
      setPassword('')
      setConfirm('')
      setTouched(false)
    },
  }
}

// ---- New user ----------------------------------------------------------------------

function NewUser({ onCancel, onCreated }: { onCancel: () => void; onCreated: (user: NasUser) => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const headingId = useId()
  const [name, setName] = useState('')
  const [touched, setTouched] = useState(false)
  const pair = usePasswordPair()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  const nameRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    nameRef.current?.focus()
  }, [])

  const problem = nasUserProblem(name)
  const nameServerError = error && ['validation.user_name', 'users.exists', 'users.name_taken'].includes(error.code)
  const passwordServerError = error?.code === 'validation.user_password'
  const nameError = nameServerError
    ? errorMessage(error)
    : problem && (touched || name.length > 0)
      ? t(`users.nameErrors.${problem}`)
      : null

  const submit = async () => {
    setTouched(true)
    pair.touch()
    if (problem) {
      nameRef.current?.focus()
      return
    }
    if (!pair.valid) return
    setBusy(true)
    setError(null)
    try {
      onCreated(await createUser({ name, password: pair.password }))
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : new ApiError(0, 'unknown', String(caught)))
      setBusy(false)
    }
  }

  const generalError = error && !nameServerError && !passwordServerError ? error : null

  return (
    <section aria-labelledby={headingId} className="mb-16 flex flex-col gap-8 border border-border p-6 md:p-8">
      <div className="flex flex-col gap-2">
        <h2 id={headingId} className="text-3xl font-semibold tracking-tight">
          {t('users.createTitle')}
        </h2>
        <p className="max-w-2xl text-muted-foreground">{t('users.createIntro')}</p>
      </div>
      <form
        noValidate
        onSubmit={(event) => {
          event.preventDefault()
          void submit()
        }}
        className="flex flex-col gap-8"
      >
        <div className="grid gap-x-12 gap-y-6 lg:grid-cols-3">
          <Field
            inputRef={nameRef}
            label={t('users.name')}
            name="new-nas-user"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            maxLength={NAS_USER_MAX + 8}
            className="[&_input]:font-mono"
            value={name}
            onChange={(event) => {
              setName(event.target.value.toLowerCase())
              setError(null)
            }}
            hint={t('users.nameHint')}
            error={nameError}
          />
          <Field
            label={t('users.password')}
            name="new-nas-password"
            type="password"
            autoComplete="new-password"
            value={pair.password}
            onChange={(event) => {
              pair.setPassword(event.target.value)
              setError(null)
            }}
            hint={t('users.passwordHint', { min: NAS_PASSWORD_MIN })}
            success={pair.passwordOk ? t('users.passwordOk') : null}
            error={passwordServerError ? errorMessage(error) : pair.passwordError}
          />
          <Field
            label={t('users.confirm')}
            name="new-nas-password-confirm"
            type="password"
            autoComplete="new-password"
            value={pair.confirm}
            onChange={(event) => pair.setConfirm(event.target.value)}
            success={pair.confirmOk ? t('users.match') : null}
            error={pair.confirmError}
          />
        </div>
        {generalError && (
          <Notice status="crit" assertive>
            <span className="text-foreground">{errorMessage(generalError)}</span>
          </Notice>
        )}
        <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
          <Button type="submit" busy={busy}>
            {busy ? t('users.creating') : t('users.create')}
          </Button>
          <Button variant="ghost" className="-ml-4" onClick={onCancel} disabled={busy}>
            {t('common.cancel')}
          </Button>
        </div>
      </form>
    </section>
  )
}

// ---- One user ----------------------------------------------------------------------

function UserRow({ user, onChanged }: { user: NasUser; onChanged: (message: string) => void }) {
  const { t } = useTranslation()
  const format = useFormat()
  const [panel, setPanel] = useState<'none' | 'password' | 'delete'>('none')

  return (
    <li className="grid gap-x-12 gap-y-6 border-t border-border py-8 md:py-10 lg:grid-cols-12">
      <div className="flex min-w-0 flex-col gap-2 lg:col-span-4">
        <h3 className="text-4xl leading-none font-semibold tracking-tighter break-all lg:text-5xl">{user.name}</h3>
        <p className="label-mono text-muted-foreground">{t('users.since', { date: format.dateTime(user.createdAt) })}</p>
      </div>
      <div className="flex min-w-0 flex-col gap-6 lg:col-span-8">
        <section className="flex flex-col gap-2">
          <h4 className="label-mono text-muted-foreground">{t('users.shares')}</h4>
          {user.shares.length === 0 ? (
            <p className="text-muted-foreground">{t('users.noShare')}</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {user.shares.map((entry) => (
                <li key={entry.share} className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1 border-t border-border pt-2">
                  <span className="font-mono text-lg">{entry.share}</span>
                  <ModeLabel mode={entry.mode} />
                </li>
              ))}
            </ul>
          )}
        </section>
        {panel === 'none' && (
          <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
            <Button variant="ghost" size="sm" className="-ml-3" icon={<Icon icon={KeyRound} />} onClick={() => setPanel('password')}>
              {t('users.changePassword')}
            </Button>
            <Button variant="ghost" size="sm" icon={<Icon icon={Trash2} />} onClick={() => setPanel('delete')}>
              {t('users.delete')}
            </Button>
          </div>
        )}
        {panel === 'password' && (
          <PasswordForm
            user={user.name}
            onCancel={() => setPanel('none')}
            onSaved={() => {
              setPanel('none')
              onChanged(t('users.passwordSaved', { name: user.name }))
            }}
          />
        )}
        {panel === 'delete' && (
          <DeleteForm
            user={user}
            onCancel={() => setPanel('none')}
            onDeleted={() => onChanged(t('users.deleted', { name: user.name }))}
          />
        )}
      </div>
    </li>
  )
}

function PasswordForm({ user, onCancel, onSaved }: { user: string; onCancel: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const pair = usePasswordPair()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const firstRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    firstRef.current?.focus()
  }, [])

  const save = async () => {
    pair.touch()
    if (!pair.valid) return
    setBusy(true)
    setError(null)
    try {
      await setUserPassword(user, pair.password)
      pair.reset()
      onSaved()
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <form
      noValidate
      onSubmit={(event) => {
        event.preventDefault()
        void save()
      }}
      className="flex flex-col gap-6 border-l-2 border-accent pl-5"
    >
      <div className="grid gap-x-8 gap-y-6 md:grid-cols-2">
        <Field
          inputRef={firstRef}
          label={t('users.newPassword')}
          name={`password-${user}`}
          type="password"
          autoComplete="new-password"
          value={pair.password}
          onChange={(event) => pair.setPassword(event.target.value)}
          hint={t('users.passwordHint', { min: NAS_PASSWORD_MIN })}
          success={pair.passwordOk ? t('users.passwordOk') : null}
          error={pair.passwordError}
        />
        <Field
          label={t('users.confirm')}
          name={`password-confirm-${user}`}
          type="password"
          autoComplete="new-password"
          value={pair.confirm}
          onChange={(event) => pair.setConfirm(event.target.value)}
          success={pair.confirmOk ? t('users.match') : null}
          error={pair.confirmError}
        />
      </div>
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
      <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
        <Button type="submit" busy={busy}>
          {busy ? t('common.saving') : t('users.savePassword')}
        </Button>
        <Button variant="ghost" className="-ml-4" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </Button>
      </div>
    </form>
  )
}

function DeleteForm({ user, onCancel, onDeleted }: { user: NasUser; onCancel: () => void; onDeleted: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const MARK = '\u0001'
  const [before = '', ...after] = t('users.deleteConfirm', { name: MARK }).split(MARK)

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  const confirmed = typed === user.name

  const remove = async () => {
    if (!confirmed) return
    setBusy(true)
    setError(null)
    try {
      await deleteUser(user.name)
      onDeleted()
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <form
      noValidate
      onSubmit={(event) => {
        event.preventDefault()
        void remove()
      }}
      className="flex flex-col gap-5 border-l-2 border-status-crit pl-5"
    >
      <div className="flex flex-col gap-2">
        <p className="text-xl font-semibold tracking-tight">{t('users.deleteTitle', { name: user.name })}</p>
        <p className="max-w-2xl text-muted-foreground">
          {t('users.deleteBody', { name: user.name, count: user.shares.length })}
        </p>
      </div>
      <Field
        inputRef={inputRef}
        label={
          <>
            {before}
            <span className="font-semibold tracking-normal text-foreground normal-case">{user.name}</span>
            {after.join(user.name)}
          </>
        }
        name={`delete-${user.name}`}
        mono
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        className="max-w-md"
        value={typed}
        onChange={(event) => setTyped(event.target.value)}
        success={confirmed ? t('users.deleteMatch') : null}
      />
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
      <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
        <Button type="submit" busy={busy} disabled={!confirmed}>
          {busy ? t('users.deleting') : t('users.deleteButton', { name: user.name })}
        </Button>
        <Button variant="ghost" className="-ml-4" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </Button>
      </div>
    </form>
  )
}
