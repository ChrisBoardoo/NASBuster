// SPDX-License-Identifier: AGPL-3.0-or-later
// Danger zone of a pool: destroy it (DELETE /storage/pools/{name}, typed name). Only an empty
// pool can go: its shares, its apps and Docker's data are listed and linked first. Its disks are
// erased and become blank; a pool whose disks are all gone is only forgotten.
import { Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'wouter'
import { ApiError } from '../api/client'
import { destroyPool, getApps, getShares } from '../api/endpoints'
import type { Pool, PoolDestroyResult } from '../api/types'
import { useApi } from '../api/useApi'
import { appAnchorId } from '../lib/apps'
import { useErrorMessage } from '../lib/messages'
import { shareAnchorId } from '../lib/shares'
import { Button } from './Button'
import { Icon } from './Icon'
import { InlineLoading } from './Loading'
import { Notice } from './Notice'
import { ConfirmName } from './ConfirmName'

interface DestroyPoolProps {
  pool: Pool
  onDestroyed: (result: PoolDestroyResult) => void
}

export function DestroyPool({ pool, onDestroyed }: DestroyPoolProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  return (
    <section className="flex flex-col gap-4 border-t border-border pt-6">
      <h4 className="label-mono text-status-crit">{t('destroy.zone')}</h4>
      {open ? (
        <DestroyForm pool={pool} onCancel={() => setOpen(false)} onDestroyed={onDestroyed} />
      ) : (
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
          <Button variant="ghost" size="sm" className="-ml-3 text-status-crit" icon={<Icon icon={Trash2} />} onClick={() => setOpen(true)}>
            {t('destroy.button')}
          </Button>
          <span className="text-sm text-muted-foreground">{t('destroy.hint')}</span>
        </div>
      )}
    </section>
  )
}

function DestroyForm({ pool, onCancel, onDestroyed }: { pool: Pool; onCancel: () => void } & Pick<DestroyPoolProps, 'onDestroyed'>) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const shares = useApi((signal) => getShares({ signal }), [])
  // Apps and Docker's own data also live on a pool (ADR-0013). Unknown if this fails: the
  // server still refuses a pool that isn't empty.
  const apps = useApi((signal) => getApps({ signal }).catch(() => null), [])
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const onPool = (shares.data?.shares ?? []).filter((s) => s.pool === pool.name)
  const appsOnPool = (apps.data?.apps ?? []).filter((a) => a.pool === pool.name)
  const dockerOnPool = Boolean(apps.data?.engine.installed && apps.data.engine.pool === pool.name)
  const blocked = onPool.length > 0 || appsOnPool.length > 0 || dockerOnPool
  const confirmed = typed === pool.name

  // The server's own list wins if it knows of shares the page didn't load.
  const serverShares =
    error instanceof ApiError && error.code === 'storage.pool_has_shares' && Array.isArray(error.params.shares)
      ? (error.params.shares as unknown[]).map(String)
      : null

  const destroy = async () => {
    if (!confirmed || blocked) return
    setBusy(true)
    setError(null)
    try {
      onDestroyed(await destroyPool(pool.name, typed))
    } catch (caught) {
      setError(caught)
      setBusy(false)
      // Shares created meanwhile: show them as blockers too.
      if (caught instanceof ApiError && caught.code === 'storage.pool_has_shares') shares.reload()
    }
  }

  const links = (items: { name: string; href: string }[]) => (
    <ul className="flex flex-wrap gap-x-6 gap-y-1">
      {items.map((item) => (
        <li key={item.name}>
          <Link
            href={item.href}
            className="inline-flex min-h-11 min-w-11 items-center font-mono text-foreground underline decoration-border-hover underline-offset-4 hover:decoration-accent"
          >
            {item.name}
          </Link>
        </li>
      ))}
    </ul>
  )
  const shareLinks = (names: string[]) => links(names.map((name) => ({ name, href: `/shares#${shareAnchorId(name)}` })))

  return (
    <form
      noValidate
      onSubmit={(event) => {
        event.preventDefault()
        void destroy()
      }}
      onKeyDown={(event) => {
        if (event.key === 'Enter' && event.target instanceof HTMLInputElement) event.preventDefault()
      }}
      className="flex flex-col gap-5 border-l-2 border-status-crit pl-5"
    >
      <p className="text-2xl font-semibold tracking-tight">{t('destroy.title', { name: pool.name })}</p>
      {(shares.loading && !shares.data) || (apps.loading && apps.data === undefined) ? (
        <InlineLoading />
      ) : blocked ? (
        <>
          {onPool.length > 0 && (
            <Notice status="warn" title={t('destroy.hasShares', { count: onPool.length })}>
              <span className="flex flex-col gap-2">
                <span>{t('destroy.hasSharesBody')}</span>
                {shareLinks(onPool.map((s) => s.name))}
              </span>
            </Notice>
          )}
          {appsOnPool.length > 0 && (
            <Notice status="warn" title={t('destroy.hasApps', { count: appsOnPool.length })}>
              <span className="flex flex-col gap-2">
                <span>{t('destroy.hasAppsBody')}</span>
                {links(appsOnPool.map((a) => ({ name: a.name, href: `/apps#${appAnchorId(a.name)}` })))}
              </span>
            </Notice>
          )}
          {dockerOnPool && (
            <Notice
              status="warn"
              title={t('destroy.hasDocker')}
              actions={
                <Link
                  href="/apps#engine-remove"
                  className="inline-flex min-h-11 items-center text-foreground underline decoration-border-hover underline-offset-4 hover:decoration-accent"
                >
                  {t('destroy.hasDockerLink')}
                </Link>
              }
            >
              {t('destroy.hasDockerBody')}
            </Notice>
          )}
        </>
      ) : (
        <>
          <p className="max-w-2xl text-muted-foreground">{t('destroy.body')}</p>
          <Notice status="crit" title={t('destroy.warningTitle')}>
            {t('destroy.warningBody')}
          </Notice>
          <ConfirmName name={pool.name} value={typed} onChange={setTyped} />
        </>
      )}
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="flex flex-col gap-2 text-foreground">
            <span>{errorMessage(error)}</span>
            {serverShares && shareLinks(serverShares)}
          </span>
        </Notice>
      )}
      <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
        {!blocked && (
          <Button type="submit" icon={<Icon icon={Trash2} />} busy={busy} disabled={!confirmed}>
            {busy ? t('destroy.destroying') : t('destroy.confirm', { name: pool.name })}
          </Button>
        )}
        <Button variant="ghost" className="-ml-4" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </Button>
      </div>
    </form>
  )
}
