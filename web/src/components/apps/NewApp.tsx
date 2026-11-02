// SPDX-License-Identifier: AGPL-3.0-or-later
// Installing an app: from the catalog (name + pool, with the expert warnings) or from a pasted
// compose file (name + pool + file, with the "as powerful as root" warning).
// POST /api/v1/apps → 202 { app }; the deployment then runs as job app-<name>.
// Not <form>s: Enter in a field installs, explicitly, and never submits an enclosing step.
import { Plus, Rocket } from 'lucide-react'
import { type KeyboardEvent, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../../api/client'
import { createApp } from '../../api/endpoints'
import type { App, AppTemplate, Pool } from '../../api/types'
import { APP_NAME_MAX, COMPOSE_MAX_BYTES, appNameProblem, freeAppName, utf8Bytes } from '../../lib/apps'
import { useErrorMessage } from '../../lib/messages'
import { Button } from '../Button'
import { Field } from '../Field'
import { Icon } from '../Icon'
import { Notice } from '../Notice'
import { PoolSelect } from '../PoolSelect'
import { ComposeField } from './ComposeField'
import { PortainerWarning } from './PortainerWarning'

const NAME_ERRORS = new Set(['validation.app_name', 'apps.exists'])
const COMPOSE_ERRORS = new Set(['apps.compose_invalid'])

function firstPool(pools: readonly Pool[], preferred: string): string {
  const mounted = pools.filter((p) => p.status?.mounted)
  return mounted.find((p) => p.name === preferred)?.name ?? mounted[0]?.name ?? ''
}

function asApiError(caught: unknown): ApiError {
  return caught instanceof ApiError ? caught : new ApiError(0, 'unknown', String(caught))
}

/** Name and pool, shared by both ways of installing. */
function useNameAndPool(initialName: string, pools: readonly Pool[], preferredPool: string) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [name, setName] = useState(initialName)
  const [pool, setPool] = useState(() => firstPool(pools, preferredPool))
  const [touched, setTouched] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  const problem = appNameProblem(name)
  const nameError =
    error && NAME_ERRORS.has(error.code)
      ? errorMessage(error)
      : problem && (touched || name.length > 0)
        ? t(`apps.nameErrors.${problem}`, { max: APP_NAME_MAX })
        : null
  return { name, setName, pool: pool || firstPool(pools, preferredPool), setPool, problem, nameError, setTouched, error, setError }
}

/** Enter in a field runs the action (the panels aren't forms). */
function onEnter(action: () => void) {
  return (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === 'Enter' && event.target instanceof HTMLInputElement) {
      event.preventDefault()
      action()
    }
  }
}

// ---- From the catalog -------------------------------------------------------------------

interface InstallTemplateProps {
  template: AppTemplate
  pools: readonly Pool[]
  preferredPool: string
  takenNames: readonly string[]
  onInstalled: (app: App) => void
  onCancel: () => void
}

export function InstallTemplate({ template, pools, preferredPool, takenNames, onInstalled, onCancel }: InstallTemplateProps) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const form = useNameAndPool(freeAppName(template.name, takenNames), pools, preferredPool)
  const [busy, setBusy] = useState(false)
  const nameRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    nameRef.current?.focus()
  }, [])

  const install = async () => {
    form.setTouched(true)
    if (form.problem) {
      nameRef.current?.focus()
      return
    }
    if (busy || !form.pool) return
    setBusy(true)
    form.setError(null)
    try {
      onInstalled(await createApp({ name: form.name, pool: form.pool, template: template.name }))
    } catch (caught) {
      form.setError(asApiError(caught))
      setBusy(false)
    }
  }

  const generalError = form.error && !NAME_ERRORS.has(form.error.code) ? form.error : null

  return (
    <div role="group" aria-label={t('apps.install.groupLabel', { title: template.title })} onKeyDown={onEnter(() => void install())} className="flex flex-col gap-6 border-l-2 border-accent pl-5">
      <Field
        inputRef={nameRef}
        label={t('apps.name')}
        name={`app-name-${template.name}`}
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        maxLength={APP_NAME_MAX + 8}
        className="[&_input]:font-mono"
        value={form.name}
        onChange={(event) => {
          form.setName(event.target.value.toLowerCase())
          form.setError(null)
        }}
        hint={t('apps.nameHint', { max: APP_NAME_MAX })}
        error={form.nameError}
      />
      <PoolSelect pools={pools} value={form.pool} onChange={form.setPool} disabled={busy} showFree hint={t('apps.poolHint')} />
      {template.expert && <PortainerWarning template={template} />}
      {generalError && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(generalError)}</span>
        </Notice>
      )}
      <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
        <Button icon={<Icon icon={Rocket} />} busy={busy} disabled={!form.pool} onClick={() => void install()}>
          {busy ? t('apps.install.installing') : t('apps.install.confirm', { title: template.title })}
        </Button>
        <Button variant="ghost" className="-ml-4" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </Button>
      </div>
    </div>
  )
}

// ---- From a pasted compose file ------------------------------------------------------------

const PLACEHOLDER = `services:
  web:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "8080:80"
    volumes:
      - ./html:/usr/share/nginx/html:ro`

interface CustomAppProps {
  pools: readonly Pool[]
  preferredPool: string
  onInstalled: (app: App) => void
  onCancel: () => void
}

export function CustomApp({ pools, preferredPool, onInstalled, onCancel }: CustomAppProps) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const form = useNameAndPool('', pools, preferredPool)
  const [compose, setCompose] = useState('')
  const [busy, setBusy] = useState(false)
  const nameRef = useRef<HTMLInputElement>(null)
  const composeRef = useRef<HTMLTextAreaElement>(null)
  const bytes = utf8Bytes(compose)
  const composeProblem = compose.trim() === '' ? 'empty' : bytes > COMPOSE_MAX_BYTES ? 'tooBig' : null

  useEffect(() => {
    nameRef.current?.focus()
  }, [])

  const deploy = async () => {
    form.setTouched(true)
    if (form.problem) {
      nameRef.current?.focus()
      return
    }
    if (composeProblem) {
      composeRef.current?.focus()
      return
    }
    if (busy || !form.pool) return
    setBusy(true)
    form.setError(null)
    try {
      onInstalled(await createApp({ name: form.name, pool: form.pool, compose }))
    } catch (caught) {
      form.setError(asApiError(caught))
      setBusy(false)
    }
  }

  const composeError =
    form.error && COMPOSE_ERRORS.has(form.error.code)
      ? typeof form.error.params.detail === 'string' && form.error.params.detail
        ? form.error.params.detail
        : form.error.message
      : null
  const generalError = form.error && !NAME_ERRORS.has(form.error.code) && !COMPOSE_ERRORS.has(form.error.code) ? form.error : null

  return (
    <div
      role="group"
      aria-label={t('apps.custom.title')}
      onKeyDown={onEnter(() => void deploy())}
      className="flex flex-col gap-8 border border-border p-6 md:p-8"
    >
      <div className="flex flex-col gap-2">
        <h3 className="text-3xl font-semibold tracking-tight">{t('apps.custom.title')}</h3>
        <p className="max-w-2xl text-muted-foreground">{t('apps.custom.intro')}</p>
      </div>
      <div className="grid gap-x-12 gap-y-8 lg:grid-cols-2">
        <Field
          inputRef={nameRef}
          label={t('apps.name')}
          name="app-name-custom"
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          maxLength={APP_NAME_MAX + 8}
          className="[&_input]:font-mono"
          value={form.name}
          onChange={(event) => {
            form.setName(event.target.value.toLowerCase())
            form.setError(null)
          }}
          hint={t('apps.nameHint', { max: APP_NAME_MAX })}
          error={form.nameError}
        />
        <PoolSelect pools={pools} value={form.pool} onChange={form.setPool} disabled={busy} showFree hint={t('apps.poolHint')} />
      </div>
      <ComposeField
        label={t('apps.custom.composeLabel')}
        value={compose}
        onChange={(value) => {
          setCompose(value)
          if (form.error && COMPOSE_ERRORS.has(form.error.code)) form.setError(null)
        }}
        textareaRef={composeRef}
        placeholder={PLACEHOLDER}
        disabled={busy}
        error={composeError}
        errorTitle={form.error && COMPOSE_ERRORS.has(form.error.code) ? errorMessage(form.error) : null}
      />
      {generalError && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(generalError)}</span>
        </Notice>
      )}
      <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
        <Button icon={<Icon icon={Plus} />} busy={busy} disabled={!form.pool || composeProblem !== null} onClick={() => void deploy()}>
          {busy ? t('apps.custom.deploying') : t('apps.custom.deploy')}
        </Button>
        <Button variant="ghost" className="-ml-4" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </Button>
      </div>
    </div>
  )
}
