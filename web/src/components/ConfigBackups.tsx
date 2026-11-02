// SPDX-License-Identifier: AGPL-3.0-or-later
// Settings → Configuration backup (ADR-0014): every night the NAS saves its configuration on
// every pool and keeps 7. "Back up now", the list (copies of one backup grouped), and the
// restore: explicit about what it replaces, typed "restore", then the daemon restarts and
// every session ends; the page waits for it to answer again, then goes to the login page.
import { ArchiveRestore, DatabaseBackup, Users as UsersIcon } from 'lucide-react'
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { backupConfig, getBootstrap, getConfigBackups, getHealth, restoreConfig } from '../api/endpoints'
import type { ConfigRestoreResult } from '../api/types'
import { useApi } from '../api/useApi'
import { useNow } from '../lib/hooks'
import { useErrorMessage } from '../lib/messages'
import { type BackupGroup, backupOrigin, groupBackups, saveRestoreNotice } from '../lib/restore'
import { useFormat } from '../lib/useFormat'
import { Button } from './Button'
import { ConfirmName } from './ConfirmName'
import { Icon } from './Icon'
import { InlineLoading, ScanLine } from './Loading'
import { Notice } from './Notice'
import { ConflictList } from './RestoreConflicts'

const CONFIRM = 'restore'
const HEALTH_POLL_MS = 1000
const RESTART_PATIENCE_MS = 2 * 60 * 1000

/**
 * previousOnly: the onboarding after an import (P3) lists only the backups of another
 * installation, to restore them before any account is created; no "Back up now".
 */
export function ConfigBackups({ previousOnly = false }: { previousOnly?: boolean }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const list = useApi((signal) => getConfigBackups({ signal }), [])
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState<unknown>(null)
  const [open, setOpen] = useState<string | null>(null)
  const [restarting, setRestarting] = useState<ConfigRestoreResult | null>(null)
  const groups = groupBackups(list.data?.backups ?? []).filter((group) => !previousOnly || !group.thisInstallation)

  const backupNow = async () => {
    setSaving(true)
    setError(null)
    setMessage('')
    try {
      const res = await backupConfig()
      setMessage(t('backup.saved', { pools: res.pools.join(', '), count: res.pools.length }))
      list.reload()
    } catch (caught) {
      setError(caught)
    } finally {
      setSaving(false)
    }
  }

  if (restarting) return <RestartWait result={restarting} />

  return (
    <div className="flex flex-col gap-8">
      {!previousOnly && (
        <div className="flex flex-col items-start gap-3">
          <Button variant="secondary" size="sm" icon={<Icon icon={DatabaseBackup} />} busy={saving} onClick={() => void backupNow()}>
            {saving ? t('backup.saving') : t('backup.now')}
          </Button>
          <p className="text-sm text-foreground empty:hidden" role="status">
            {message}
          </p>
          {error !== null && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
        </div>
      )}

      {list.loading && !list.data ? (
        <InlineLoading label={t('backup.loading')} />
      ) : list.error && !list.data ? (
        <Notice status="warn">{errorMessage(list.error)}</Notice>
      ) : groups.length === 0 ? (
        <p className="text-muted-foreground">{t(previousOnly ? 'import.noBackups' : 'backup.none')}</p>
      ) : (
        <ul className="border-b border-border">
          {groups.map((group) => (
            <BackupRow
              key={group.name}
              group={group}
              open={open === group.name}
              onOpen={() => setOpen(group.name)}
              onClose={() => setOpen(null)}
              onRestarting={setRestarting}
            />
          ))}
        </ul>
      )}
    </div>
  )
}

interface BackupRowProps {
  group: BackupGroup
  open: boolean
  onOpen: () => void
  onClose: () => void
  onRestarting: (result: ConfigRestoreResult) => void
}

function BackupRow({ group, open, onOpen, onClose, onRestarting }: BackupRowProps) {
  const { t } = useTranslation()
  const format = useFormat()
  const now = useNow(60_000)
  const date = format.dateTime(group.time)
  // Which installation made it, and whether it holds anything (P6): after a dead SD card,
  // the backup to restore is the old installation's, not the newest one.
  const origin = backupOrigin(group)
  const empty = group.users === 0 && group.shares <= 0

  return (
    <li className="flex flex-col gap-4 border-t border-border py-5">
      <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-2">
        <div className="flex min-w-0 flex-col gap-1">
          <p className="flex flex-wrap items-baseline gap-x-3 text-xl font-semibold tracking-tight tabular-nums">
            <time dateTime={new Date(group.time * 1000).toISOString()}>{date}</time>
            <span className={origin === 'this' ? 'label-mono text-muted-foreground' : 'label-mono text-accent'}>
              {t(`backup.origin.${origin}`)}
            </span>
          </p>
          <p className="text-sm text-muted-foreground">
            {[
              format.relative(group.time, now),
              group.hostname ? `\\\\${group.hostname}` : null,
              t('backup.users', { count: group.users }),
              group.shares >= 0 ? t('backup.shares', { count: group.shares }) : null,
              group.admin ? t('backup.admin', { name: group.admin }) : null,
              format.bytes(group.sizeBytes),
            ]
              .filter(Boolean)
              .join(' · ')}
          </p>
          {empty && <p className="text-sm text-status-warn">{t('backup.empty')}</p>}
          <p className="text-sm text-muted-foreground">{t('backup.copies', { pools: group.pools.join(', '), count: group.pools.length })}</p>
          <p className="font-mono text-xs break-all text-muted-foreground">{group.name}</p>
        </div>
        {!open && (
          <Button variant="ghost" size="sm" className="-mr-3" icon={<Icon icon={ArchiveRestore} />} onClick={onOpen}>
            {t('backup.restore')}
          </Button>
        )}
      </div>
      {open && <RestorePanel group={group} date={date} onCancel={onClose} onRestarting={onRestarting} />}
    </li>
  )
}

function RestorePanel({
  group,
  date,
  onCancel,
  onRestarting,
}: {
  group: BackupGroup
  date: string
  onCancel: () => void
  onRestarting: (result: ConfigRestoreResult) => void
}) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const selectId = useId()
  const [pool, setPool] = useState(group.pools[0] ?? '')
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const confirmed = typed === CONFIRM

  const restore = async () => {
    if (!confirmed || busy) return
    setBusy(true)
    setError(null)
    try {
      const result = await restoreConfig(pool, group.name, typed)
      saveRestoreNotice(result)
      onRestarting(result)
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <div
      role="group"
      aria-label={t('backup.restoreTitle', { date })}
      onKeyDown={(event) => {
        // Enter in the field never restores; only the button does.
        if (event.key === 'Enter' && event.target instanceof HTMLInputElement) event.preventDefault()
      }}
      className="flex flex-col gap-5 border-l-2 border-status-crit pl-5"
    >
      <p className="text-2xl font-semibold tracking-tight">{t('backup.restoreTitle', { date })}</p>
      <Notice status="crit" title={t('backup.replacesTitle')}>
        <ul className="flex list-disc flex-col gap-1.5 pl-5 marker:text-border-hover">
          <li>{t('backup.replacesWhat', { date })}</li>
          <li>{t('backup.sessionsEnd')}</li>
          {group.admin && <li>{t('backup.adminIs', { name: group.admin })}</li>}
          <li className="font-semibold text-foreground">{t('backup.oldPassword')}</li>
          <li>{t('backup.restarts')}</li>
        </ul>
      </Notice>
      {group.pools.length > 1 && (
        <div className="flex max-w-md flex-col gap-2">
          <label htmlFor={selectId} className="label-mono text-muted-foreground">
            {t('backup.copyLabel')}
          </label>
          <select
            id={selectId}
            value={pool}
            disabled={busy}
            onChange={(event) => setPool(event.target.value)}
            className="h-12 w-full border border-border bg-input px-4 text-base text-foreground outline-none transition-colors duration-150 ease-crisp hover:border-border-hover focus:border-accent disabled:opacity-50 md:h-14"
          >
            {group.pools.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        </div>
      )}
      <ConfirmName name={CONFIRM} value={typed} onChange={setTyped} word />
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
      <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
        <Button icon={<Icon icon={ArchiveRestore} />} busy={busy} disabled={!confirmed} onClick={() => void restore()}>
          {busy ? t('backup.restoring') : t('backup.restoreConfirm')}
        </Button>
        <Button variant="ghost" className="-ml-4" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </Button>
      </div>
    </div>
  )
}

/**
 * After the 202: what was restored and skipped, then wait for the restarted daemon. It is back
 * when /health answers AND this session is gone (the old process may still answer for a moment).
 */
function RestartWait({ result }: { result: ConfigRestoreResult }) {
  const { t } = useTranslation()
  const [back, setBack] = useState(false)
  const [late, setLate] = useState(false)
  const conflicts = result.conflicts

  useEffect(() => {
    let stopped = false
    let timer = 0
    const started = Date.now()
    const tick = async () => {
      try {
        await getHealth()
        const boot = await getBootstrap()
        if (!boot.authenticated) {
          if (stopped) return
          setBack(true)
          // Nothing to read: straight to the login page (a full load: fresh state and cookies).
          if (conflicts.length === 0) window.location.assign('/login')
          return
        }
      } catch {
        /* restarting: not answering yet */
      }
      if (stopped) return
      if (Date.now() - started > RESTART_PATIENCE_MS) setLate(true)
      timer = window.setTimeout(() => void tick(), HEALTH_POLL_MS)
    }
    timer = window.setTimeout(() => void tick(), HEALTH_POLL_MS)
    return () => {
      stopped = true
      window.clearTimeout(timer)
    }
  }, [conflicts.length])

  return (
    <div className="flex max-w-3xl flex-col gap-6">
      <div className="flex flex-col gap-3" role="status">
        <p className="text-2xl font-semibold tracking-tight">{back ? t('backup.back') : t('backup.restartingTitle')}</p>
        {!back && <ScanLine className="w-40" />}
        <p className="text-muted-foreground">{back ? t('backup.backBody') : t('backup.restartingBody')}</p>
      </div>
      <p className="flex items-center gap-2">
        <Icon icon={UsersIcon} size={16} className="text-muted-foreground" />
        {t('backup.usersRestored', { count: result.users.length })}
      </p>
      {conflicts.length > 0 && <ConflictList conflicts={conflicts} />}
      {late && !back && (
        <Notice status="warn" title={t('backup.lateTitle')}>
          {t('backup.lateBody')}
        </Notice>
      )}
      {(back || late) && (
        <div>
          <Button onClick={() => window.location.assign('/login')}>{t('backup.loginAgain')}</Button>
        </div>
      )}
    </div>
  )
}
