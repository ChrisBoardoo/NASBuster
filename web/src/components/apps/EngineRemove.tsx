// SPDX-License-Identifier: AGPL-3.0-or-later
// Danger zone: uninstall Docker (POST /api/v1/apps/engine/remove { confirm: "docker", deleteData }).
// Refused while apps have containers in this Docker, or other containers exist;
// apps without containers here stay, data untouched (ADR-0035). Synchronous: it can take a minute.
import { Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../../api/client'
import { removeEngine } from '../../api/endpoints'
import type { EngineStatus } from '../../api/types'
import { appAnchorId } from '../../lib/apps'
import { useErrorMessage } from '../../lib/messages'
import { Button } from '../Button'
import { Checkbox } from '../Checkbox'
import { ConfirmName } from '../ConfirmName'
import { Icon } from '../Icon'
import { ScanLine } from '../Loading'
import { Notice } from '../Notice'

const CONFIRM = 'docker'

interface EngineRemoveProps {
  engine: EngineStatus
  /** CWNSB apps with containers in this Docker: they must be removed first. */
  apps: readonly string[]
  /** CWNSB apps without containers here: they stay, with their data. */
  kept?: readonly string[]
  onRemoved: (deleteData: boolean) => void
}

export function EngineRemove({ engine, apps, kept = [], onRemoved }: EngineRemoveProps) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  const [deleteData, setDeleteData] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const confirmed = typed === CONFIRM

  // The server's list wins once it answered (an app may have been added elsewhere).
  const serverApps =
    error instanceof ApiError && error.code === 'apps.engine_in_use' && Array.isArray(error.params.apps)
      ? error.params.apps.map(String)
      : null
  const blocking = serverApps ?? [...apps]

  const remove = async () => {
    if (!confirmed || blocking.length > 0) return
    setBusy(true)
    setError(null)
    try {
      await removeEngine(typed, deleteData)
      onRemoved(deleteData)
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <section aria-labelledby="engine-remove" className="mt-20 flex flex-col gap-4 border-t border-border pt-6">
      <h2 id="engine-remove" className="label-mono text-status-crit">
        {t('destroy.zone')}
      </h2>
      {!open ? (
        <div className="flex flex-col items-start gap-1">
          <Button variant="ghost" size="sm" className="-ml-3 text-status-crit" icon={<Icon icon={Trash2} />} onClick={() => setOpen(true)}>
            {t('apps.engineRemove.button')}
          </Button>
          <p className="text-sm text-muted-foreground">{t('apps.engineRemove.hint')}</p>
        </div>
      ) : (
        <div
          role="group"
          aria-label={t('apps.engineRemove.title')}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && event.target instanceof HTMLInputElement) event.preventDefault()
          }}
          className="flex max-w-3xl flex-col gap-5 border-l-2 border-status-crit pl-5"
        >
          <p className="text-2xl font-semibold tracking-tight">{t('apps.engineRemove.title')}</p>
          <p className="text-muted-foreground">{t('apps.engineRemove.body')}</p>

          {blocking.length > 0 ? (
            <Notice status="warn" title={t('apps.engineRemove.appsFirst', { count: blocking.length })}>
              <ul className="flex flex-wrap gap-x-4">
                {blocking.map((name) => (
                  <li key={name}>
                    <a
                      href={`#${appAnchorId(name)}`}
                      className="inline-flex min-h-11 min-w-11 items-center font-mono text-foreground underline decoration-border-hover underline-offset-4 hover:decoration-accent"
                    >
                      {name}
                    </a>
                  </li>
                ))}
              </ul>
            </Notice>
          ) : (
            <>
              {kept.length > 0 && (
                <Notice status="info" title={t('apps.engineRemove.appsKept', { count: kept.length })}>
                  <span className="font-mono text-foreground">{kept.join(', ')}</span>
                </Notice>
              )}
              <Checkbox
                checked={deleteData}
                onChange={setDeleteData}
                danger
                disabled={busy}
                label={engine.pool ? t('apps.engineRemove.deleteDataOn', { pool: engine.pool }) : t('apps.engineRemove.deleteData')}
                description={t('apps.engineRemove.volumesHint')}
              />
              {deleteData ? (
                <Notice status="crit" title={t('apps.engineRemove.dataGoneTitle')}>
                  {t('apps.engineRemove.dataGoneBody')}
                </Notice>
              ) : (
                <Notice status="info">
                  {engine.pool ? t('apps.engineRemove.dataKept', { pool: engine.pool }) : t('apps.engineRemove.dataKeptNoPool')}
                </Notice>
              )}
              <ConfirmName name={CONFIRM} value={typed} onChange={setTyped} word />
            </>
          )}

          {error !== null && !(serverApps && serverApps.length > 0) && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
          {busy && (
            <div className="flex flex-col gap-2" role="status">
              <span className="label-mono text-muted-foreground">{t('apps.engineRemove.removingLong')}</span>
              <ScanLine className="w-40" />
            </div>
          )}
          <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
            {blocking.length === 0 && (
              <Button icon={<Icon icon={Trash2} />} busy={busy} disabled={!confirmed} onClick={() => void remove()}>
                {busy ? t('apps.engineRemove.removing') : t('apps.engineRemove.confirm')}
              </Button>
            )}
            <Button
              variant="ghost"
              className={blocking.length === 0 ? '-ml-4' : '-ml-3'}
              onClick={() => {
                setOpen(false)
                setError(null)
              }}
              disabled={busy}
            >
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      )}
    </section>
  )
}
