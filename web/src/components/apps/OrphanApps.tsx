// SPDX-License-Identifier: AGPL-3.0-or-later
// CWNSB projects without a recorded app (ADR-0036): removed while their containers lived in another
// Docker, or a restored configuration older than them. Docker restarted them by itself: they can be
// stopped, or have their containers removed. Their folder is never touched.
import { Square, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { orphanAction } from '../../api/endpoints'
import type { OrphanAction, OrphanApp } from '../../api/types'
import { appState } from '../../lib/apps'
import { useErrorMessage } from '../../lib/messages'
import { Button } from '../Button'
import { Icon } from '../Icon'
import { Notice } from '../Notice'
import { AppStatus, Containers } from './AppRow'

interface OrphanAppsProps {
  orphans: readonly OrphanApp[]
  engineActive: boolean
  onChanged: () => void
  /** Its row goes away with its containers: the page says it's done. */
  onRemoved: (name: string) => void
}

export function OrphanApps({ orphans, engineActive, onChanged, onRemoved }: OrphanAppsProps) {
  return (
    <ul className="border-b border-border">
      {orphans.map((orphan) => (
        <OrphanRow key={orphan.name} orphan={orphan} engineActive={engineActive} onChanged={onChanged} onRemoved={onRemoved} />
      ))}
    </ul>
  )
}

function OrphanRow({ orphan, engineActive, onChanged, onRemoved }: { orphan: OrphanApp } & Omit<OrphanAppsProps, 'orphans'>) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const state = appState(orphan)
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState<OrphanAction | null>(null)
  const [message, setMessage] = useState('')
  const [error, setError] = useState<unknown>(null)
  const name = orphan.app
  const locked = busy !== null || !engineActive

  const run = async (action: OrphanAction) => {
    setBusy(action)
    setError(null)
    setMessage('')
    try {
      await orphanAction(name, action)
      setConfirming(false)
      if (action === 'remove') {
        onRemoved(name)
        return
      }
      setMessage(t('apps.orphans.stopped', { name }))
      onChanged()
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(null)
    }
  }

  return (
    <li className="grid gap-x-12 gap-y-6 border-t border-border py-8 lg:grid-cols-12">
      <div className="flex min-w-0 flex-col gap-3 lg:col-span-5">
        <h3 className="text-3xl font-semibold tracking-tight break-all">{name}</h3>
        <AppStatus kind={state.kind} status={state.status} running={state.running} total={state.total} />
        <p className="text-muted-foreground">
          {orphan.pool ? t('apps.orphans.folder', { pool: orphan.pool, name }) : t('apps.orphans.folderUnknown')}
        </p>
        {orphan.pool && <p className="max-w-xl text-muted-foreground">{t('apps.orphans.keep', { name, pool: orphan.pool })}</p>}

        {!confirming ? (
          <div className="flex flex-wrap items-center gap-x-6 gap-y-1">
            {state.running > 0 && (
              <Button variant="ghost" size="sm" className="-ml-3" icon={<Icon icon={Square} />} busy={busy === 'stop'} disabled={locked} onClick={() => void run('stop')}>
                {t('apps.orphans.stop')}
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              className="-ml-3 text-status-crit"
              icon={<Icon icon={Trash2} />}
              disabled={locked}
              onClick={() => setConfirming(true)}
            >
              {t('apps.orphans.remove')}
            </Button>
          </div>
        ) : (
          <div role="group" aria-label={t('apps.orphans.removeTitle', { name })} className="flex flex-col gap-4 border-l-2 border-status-crit pl-5">
            <p className="text-xl font-semibold tracking-tight">{t('apps.orphans.removeTitle', { name })}</p>
            <p className="max-w-xl text-muted-foreground">
              {orphan.pool ? t('apps.orphans.removeBody', { pool: orphan.pool, name }) : t('apps.orphans.removeBodyNoPool')}
            </p>
            <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
              <Button icon={<Icon icon={Trash2} />} busy={busy === 'remove'} disabled={locked} onClick={() => void run('remove')}>
                {busy === 'remove' ? t('apps.orphans.removing') : t('apps.orphans.removeConfirm')}
              </Button>
              <Button variant="ghost" className="-ml-4" onClick={() => setConfirming(false)} disabled={busy !== null}>
                {t('common.cancel')}
              </Button>
            </div>
          </div>
        )}
        {message && <Notice status="ok">{message}</Notice>}
        {error !== null && (
          <Notice status="crit" assertive>
            <span className="text-foreground">{errorMessage(error)}</span>
          </Notice>
        )}
      </div>
      <div className="min-w-0 lg:col-span-7">
        <Containers containers={orphan.containers} />
      </div>
    </li>
  )
}
