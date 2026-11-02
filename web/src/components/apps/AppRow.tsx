// SPDX-License-Identifier: AGPL-3.0-or-later
// One application: its state in words, links to its web pages, its containers, and what can be
// done with it (start, stop, restart, update, logs, edit the compose file, remove).
import { ExternalLink, FileCode, Play, RefreshCw, RotateCw, ScrollText, Square, Trash2, Upload } from 'lucide-react'
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../../api/client'
import { appAction, getAppLogs, removeApp, updateAppCompose } from '../../api/endpoints'
import type { App, AppAction, AppContainer, AppTemplate } from '../../api/types'
import { useApi } from '../../api/useApi'
import {
  type AppStateKind,
  COMPOSE_MAX_BYTES,
  CONTAINER_STATUS,
  HEALTH_STATUS,
  appAnchorId,
  appLinks,
  appState,
  currentHost,
  isRunning,
  utf8Bytes,
} from '../../lib/apps'
import { APP_LOG_TAIL } from '../../lib/constants'
import { useDynamicT, useErrorMessage } from '../../lib/messages'
import { Button, ExternalButtonLink } from '../Button'
import { Checkbox } from '../Checkbox'
import { ConfirmName } from '../ConfirmName'
import { Icon } from '../Icon'
import { InlineLoading } from '../Loading'
import { Notice } from '../Notice'
import { StatusLabel } from '../StatusLabel'
import { ComposeField } from './ComposeField'
import { JobLog } from './JobLog'
import { PortainerWarning, claimsAdminOnFirstVisit } from './PortainerWarning'
import { SetupToken } from './SetupToken'

/** A removed app's data is kept this long in the pool's trash (apps.TrashDays, ADR-0035). */
const TRASH_DAYS = 7

interface AppRowProps {
  app: App
  template: AppTemplate | undefined
  engineActive: boolean
  /** Installed during this visit: Portainer's "create the admin account now" stands out. */
  fresh: boolean
  onChanged: () => void
  onRemoved: (name: string, deleteData: boolean) => void
}

type Panel = 'logs' | 'edit' | null

export function AppRow({ app, template, engineActive, fresh, onChanged, onRemoved }: AppRowProps) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const state = appState(app, engineActive)
  const jobRunning = isRunning(app.job)
  const links = appLinks(app.containers, template, currentHost())
  const main = links.find((link) => link.main)
  const others = links.filter((link) => !link.main)
  const [panel, setPanel] = useState<Panel>(null)
  const [busy, setBusy] = useState<AppAction | 'redeploy' | null>(null)
  const [message, setMessage] = useState('')
  const [error, setError] = useState<unknown>(null)
  const panelId = useId()

  const run = async (action: AppAction) => {
    setBusy(action)
    setError(null)
    setMessage('')
    try {
      await appAction(app.name, action)
      setMessage(t(`apps.actions.done.${action}`))
      onChanged()
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(null)
    }
  }

  // The first deployment failed: deploy the same file again.
  const redeploy = async () => {
    setBusy('redeploy')
    setError(null)
    setMessage('')
    try {
      await updateAppCompose(app.name, app.compose)
      setMessage(t('apps.actions.done.redeploy'))
      onChanged()
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(null)
    }
  }

  const locked = busy !== null || jobRunning || !engineActive
  const anyRunning = state.running > 0
  const allRunning = state.total > 0 && state.running === state.total

  return (
    <li id={appAnchorId(app.name)} className="grid scroll-mt-6 gap-x-12 gap-y-8 border-t border-border py-10 md:py-12 lg:grid-cols-12">
      <div className="flex min-w-0 flex-col gap-4 lg:col-span-5">
        <h3 className="text-5xl leading-[0.95] font-semibold tracking-tighter break-all lg:text-6xl">{app.name}</h3>
        <dl className="grid grid-cols-[minmax(5rem,auto)_1fr] gap-x-6 gap-y-2 text-sm">
          <dt className="label-mono pt-0.5 text-muted-foreground">{t('apps.row.source')}</dt>
          <dd>{template ? template.title : app.template || t('apps.row.custom')}</dd>
          <dt className="label-mono pt-0.5 text-muted-foreground">{t('shares.pool')}</dt>
          <dd>{app.pool}</dd>
        </dl>
        <AppStatus kind={state.kind} status={state.status} running={state.running} total={state.total} />
        {(main || others.length > 0) && (
          <div className="flex flex-wrap items-center gap-x-6 gap-y-1">
            {main && (
              <ExternalButtonLink
                href={main.href}
                icon={<Icon icon={ExternalLink} />}
                label={t('apps.row.openLabel', { name: app.name })}
              >
                {t('apps.row.open')}
              </ExternalButtonLink>
            )}
            {others.map((link) => (
              <ExternalButtonLink
                key={link.port}
                href={link.href}
                variant="ghost"
                size="sm"
                className="-ml-3"
                icon={<Icon icon={ExternalLink} />}
                label={t('apps.row.openPortLabel', { name: app.name, port: link.port })}
              >
                {t('apps.row.port', { port: link.port })}
              </ExternalButtonLink>
            ))}
          </div>
        )}
        {template?.expert &&
          (fresh && claimsAdminOnFirstVisit(template) ? (
            <PortainerWarning template={template} />
          ) : (
            <PortainerWarning template={template} short />
          ))}
      </div>

      <div className="flex min-w-0 flex-col gap-8 lg:col-span-7">
        {template?.setupToken && engineActive && state.running > 0 && (
          <SetupToken app={app.name} title={template.title} onChanged={onChanged} />
        )}
        {(jobRunning || (app.job.state === 'failed' && app.job.log.length > 0)) && (
          <section className="flex flex-col gap-3">
            {app.job.state === 'failed' && (
              <Notice status="crit" title={app.status === '' ? t('apps.row.failedTitle') : t('apps.row.updateFailedTitle')}>
                {t('apps.row.failedBody')}
              </Notice>
            )}
            {jobRunning && <p className="text-muted-foreground">{t('apps.row.deployingBody')}</p>}
            <JobLog lines={app.job.log} label={t('apps.row.jobLog', { name: app.name })} />
          </section>
        )}

        <Containers containers={app.containers} hidden={jobRunning && app.containers.length === 0} />

        <section className="flex flex-col gap-3">
          <h4 className="label-mono text-muted-foreground">{t('apps.actions.title')}</h4>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            {jobRunning ? null : state.kind === 'failed' ? (
              <Button variant="secondary" size="sm" icon={<Icon icon={RotateCw} />} busy={busy === 'redeploy'} disabled={locked} onClick={() => void redeploy()}>
                {t('apps.actions.redeploy')}
              </Button>
            ) : (
              <>
                {!allRunning && (
                  <Button variant="ghost" size="sm" icon={<Icon icon={Play} />} busy={busy === 'start'} disabled={locked} onClick={() => void run('start')}>
                    {t('apps.actions.start')}
                  </Button>
                )}
                {anyRunning && (
                  <Button variant="ghost" size="sm" icon={<Icon icon={Square} />} busy={busy === 'stop'} disabled={locked} onClick={() => void run('stop')}>
                    {t('apps.actions.stop')}
                  </Button>
                )}
                {anyRunning && (
                  <Button variant="ghost" size="sm" icon={<Icon icon={RotateCw} />} busy={busy === 'restart'} disabled={locked} onClick={() => void run('restart')}>
                    {t('apps.actions.restart')}
                  </Button>
                )}
                <Button variant="ghost" size="sm" icon={<Icon icon={Upload} />} busy={busy === 'update'} disabled={locked} onClick={() => void run('update')}>
                  {t('apps.actions.update')}
                </Button>
              </>
            )}
            <Button
              variant="ghost"
              size="sm"
              icon={<Icon icon={ScrollText} />}
              aria-expanded={panel === 'logs'}
              aria-controls={panelId}
              disabled={!engineActive}
              onClick={() => setPanel((p) => (p === 'logs' ? null : 'logs'))}
            >
              {panel === 'logs' ? t('apps.logs.hide') : t('apps.logs.show')}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              icon={<Icon icon={FileCode} />}
              aria-expanded={panel === 'edit'}
              aria-controls={panelId}
              onClick={() => setPanel((p) => (p === 'edit' ? null : 'edit'))}
            >
              {t('apps.edit.button')}
            </Button>
          </div>
          {!jobRunning && state.kind !== 'failed' && (
            <p className="max-w-2xl text-sm text-muted-foreground">{t('apps.actions.updateHint')}</p>
          )}
          <p className="text-sm text-foreground empty:hidden" role="status">
            {message}
          </p>
          {error !== null && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
          <div id={panelId}>
            {panel === 'logs' && <LogsPanel name={app.name} />}
            {panel === 'edit' && (
              <EditCompose
                app={app}
                locked={jobRunning}
                onCancel={() => setPanel(null)}
                onSaved={() => {
                  setPanel(null)
                  setMessage(t('apps.actions.done.redeploy'))
                  onChanged()
                }}
              />
            )}
          </div>
        </section>

        <RemoveApp app={app} onRemoved={(deleteData) => onRemoved(app.name, deleteData)} />
      </div>
    </li>
  )
}

// ---- State --------------------------------------------------------------------------------

export function AppStatus({
  kind,
  status,
  running,
  total,
}: {
  kind: AppStateKind
  status: Parameters<typeof StatusLabel>[0]['status']
  running: number
  total: number
}) {
  const { t } = useTranslation()
  return <StatusLabel status={status} label={t(`apps.state.${kind}`, { running, total })} />
}

// ---- Containers ---------------------------------------------------------------------------

export function Containers({ containers, hidden = false }: { containers: readonly AppContainer[]; hidden?: boolean }) {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  if (hidden) return null
  return (
    <section className="flex flex-col gap-3">
      <h4 className="label-mono text-muted-foreground">{t('apps.containers.title', { count: containers.length })}</h4>
      {containers.length === 0 ? (
        <p className="text-muted-foreground">{t('apps.containers.none')}</p>
      ) : (
        <ul className="flex flex-col">
          {containers.map((c) => {
            const stateKey = `apps.containers.state.${c.state}`
            const healthKey = `apps.containers.health.${c.health}`
            const ports = c.ports.map((p) => `${p.publishedPort} → ${p.targetPort}/${p.protocol}`)
            return (
              <li key={c.name} className="flex flex-col gap-1 border-t border-border py-3">
                <div className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1">
                  <span className="text-lg font-semibold tracking-tight break-all">{c.service || c.name}</span>
                  <span className="flex flex-wrap items-center gap-x-4 gap-y-1">
                    <StatusLabel status={CONTAINER_STATUS[c.state] ?? 'unknown'} label={has(stateKey) ? td(stateKey) : c.state} />
                    {c.health && (
                      <StatusLabel status={HEALTH_STATUS[c.health] ?? 'unknown'} label={has(healthKey) ? td(healthKey) : c.health} />
                    )}
                  </span>
                </div>
                <span className="font-mono text-xs break-all text-muted-foreground">
                  {[c.image, c.status, ports.length > 0 ? ports.join(', ') : null].filter(Boolean).join(' · ')}
                </span>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

// ---- Logs -----------------------------------------------------------------------------------

function LogsPanel({ name }: { name: string }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const logs = useApi((signal) => getAppLogs(name, APP_LOG_TAIL, { signal }), [name])
  const text = logs.data ?? ''

  return (
    <div className="flex flex-col gap-3 pt-2">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1">
        <span className="label-mono text-muted-foreground">{t('apps.logs.title', { count: APP_LOG_TAIL })}</span>
        <Button variant="ghost" size="sm" className="-mr-3" icon={<Icon icon={RefreshCw} />} busy={logs.loading} onClick={logs.reload}>
          {t('common.refresh')}
        </Button>
      </div>
      {logs.loading && logs.data === undefined ? (
        <InlineLoading label={t('apps.logs.loading')} className="py-2" />
      ) : logs.error && logs.data === undefined ? (
        <Notice status="warn">{errorMessage(logs.error)}</Notice>
      ) : text.trim() === '' ? (
        <p className="text-muted-foreground">{t('apps.logs.empty')}</p>
      ) : (
        <LogText text={text} label={t('apps.logs.label', { name })} />
      )}
    </div>
  )
}

/** The logs, scrolled to the end (the newest lines). */
function LogText({ text, label }: { text: string; label: string }) {
  const [el, setEl] = useState<HTMLPreElement | null>(null)
  useEffect(() => {
    if (el) el.scrollTop = el.scrollHeight
  }, [el, text])
  return (
    <pre
      ref={setEl}
      tabIndex={0}
      aria-label={label}
      className="max-h-96 max-w-full overflow-auto border border-border bg-muted px-4 py-3 font-mono text-xs leading-relaxed text-foreground"
    >
      {text}
    </pre>
  )
}

// ---- Edit the compose file -----------------------------------------------------------------

function EditCompose({ app, locked, onCancel, onSaved }: { app: App; locked: boolean; onCancel: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [compose, setCompose] = useState(app.compose)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  const unchanged = compose === app.compose
  const invalid = compose.trim() === '' || utf8Bytes(compose) > COMPOSE_MAX_BYTES
  const composeError =
    error?.code === 'apps.compose_invalid'
      ? typeof error.params.detail === 'string' && error.params.detail
        ? error.params.detail
        : error.message
      : null

  const save = async () => {
    if (unchanged || invalid) return
    setBusy(true)
    setError(null)
    try {
      await updateAppCompose(app.name, compose)
      onSaved()
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : new ApiError(0, 'unknown', String(caught)))
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-5 border-l-2 border-accent pt-2 pl-5">
      <p className="max-w-2xl text-muted-foreground">{t('apps.edit.intro', { pool: app.pool })}</p>
      <ComposeField
        label={t('apps.edit.label', { name: app.name })}
        value={compose}
        onChange={(value) => {
          setCompose(value)
          if (error?.code === 'apps.compose_invalid') setError(null)
        }}
        disabled={busy}
        error={composeError}
        errorTitle={composeError ? errorMessage(error) : null}
      />
      {error && !composeError && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
      <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
        <Button icon={<Icon icon={RotateCw} />} busy={busy} disabled={unchanged || invalid || locked} onClick={() => void save()}>
          {busy ? t('apps.edit.saving') : t('apps.edit.save')}
        </Button>
        <Button variant="ghost" className="-ml-4" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </Button>
      </div>
    </div>
  )
}

// ---- Danger zone: remove ----------------------------------------------------------------------

function RemoveApp({ app, onRemoved }: { app: App; onRemoved: (deleteData: boolean) => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  const [deleteData, setDeleteData] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const confirmed = typed === app.name

  const remove = async () => {
    if (!confirmed) return
    setBusy(true)
    setError(null)
    try {
      await removeApp(app.name, typed, deleteData)
      onRemoved(deleteData)
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-4 border-t border-border pt-6">
      <h4 className="label-mono text-status-crit">{t('destroy.zone')}</h4>
      {!open ? (
        <div>
          <Button variant="ghost" size="sm" className="-ml-3 text-status-crit" icon={<Icon icon={Trash2} />} onClick={() => setOpen(true)}>
            {t('apps.remove.button')}
          </Button>
        </div>
      ) : (
        <div
          role="group"
          aria-label={t('apps.remove.title', { name: app.name })}
          onKeyDown={(event) => {
            // Destructive: Enter in the field never removes; only the button does.
            if (event.key === 'Enter' && event.target instanceof HTMLInputElement) event.preventDefault()
          }}
          className="flex flex-col gap-5 border-l-2 border-status-crit pl-5"
        >
          <p className="text-2xl font-semibold tracking-tight">{t('apps.remove.title', { name: app.name })}</p>
          <p className="max-w-2xl text-muted-foreground">{t('apps.remove.body')}</p>
          <Checkbox
            checked={deleteData}
            onChange={setDeleteData}
            danger
            label={t('apps.remove.deleteData')}
            description={t('apps.remove.deleteDataHint', { pool: app.pool, name: app.name })}
            disabled={busy}
          />
          {deleteData ? (
            <Notice status="crit" title={t('apps.remove.dataGoneTitle', { days: TRASH_DAYS })}>
              {t('apps.remove.dataGoneBody', { pool: app.pool, days: TRASH_DAYS })}
            </Notice>
          ) : (
            <Notice status="info">{t('apps.remove.dataKept', { pool: app.pool, name: app.name })}</Notice>
          )}
          <ConfirmName name={app.name} value={typed} onChange={setTyped} />
          {error !== null && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
          <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
            <Button icon={<Icon icon={Trash2} />} busy={busy} disabled={!confirmed} onClick={() => void remove()}>
              {busy ? t('apps.remove.removing') : t('apps.remove.confirm', { name: app.name })}
            </Button>
            <Button variant="ghost" className="-ml-4" onClick={() => setOpen(false)} disabled={busy}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      )}
    </section>
  )
}
