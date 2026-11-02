// SPDX-License-Identifier: AGPL-3.0-or-later
// Settings → Protection: snapshot retention, scrub interval, on/off switches, last runs.
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getProtection, putProtection } from '../api/endpoints'
import type { Protection } from '../api/types'
import { useApi } from '../api/useApi'
import { useNow } from '../lib/hooks'
import { useErrorMessage } from '../lib/messages'
import { hasProblems, protectionProblems, sameProtection } from '../lib/protection'
import { useFormat } from '../lib/useFormat'
import { Button } from './Button'
import { InlineLoading } from './Loading'
import { Notice } from './Notice'
import { ProtectionEditor } from './ProtectionEditor'

export function ProtectionSettings() {
  const { t } = useTranslation()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const now = useNow(30_000)
  const loaded = useApi((signal) => getProtection({ signal }), [])
  const [saved, setSaved] = useState<Protection | null>(null)
  const [value, setValue] = useState<Protection | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [message, setMessage] = useState('')

  useEffect(() => {
    if (!loaded.data) return
    setSaved(loaded.data.protection)
    setValue(loaded.data.protection)
  }, [loaded.data])

  if (loaded.loading && !loaded.data) return <InlineLoading />
  if (!value || !saved) {
    return (
      <Notice
        status="warn"
        title={t('protection.loadFailed')}
        actions={
          <Button variant="ghost" size="sm" className="-ml-3" onClick={loaded.reload}>
            {t('common.retry')}
          </Button>
        }
      >
        {errorMessage(loaded.error)}
      </Notice>
    )
  }

  const state = loaded.data?.state
  const invalid = hasProblems(protectionProblems(value))
  const unchanged = sameProtection(value, saved)
  const when = (ts: number | undefined) =>
    ts && ts > 0 ? `${format.relative(ts, now)} · ${format.dateTime(ts)}` : t('protection.never')
  const scrubs = Object.entries(state?.lastScrub ?? {})

  const save = async () => {
    setBusy(true)
    setError(null)
    setMessage('')
    try {
      const next = await putProtection(value)
      setSaved(next)
      setValue(next)
      setMessage(t('protection.saved'))
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-8">
      <ProtectionEditor
        value={value}
        onChange={(next) => {
          setValue(next)
          setMessage('')
        }}
        showSwitches
        disabled={busy}
        snapshotsAside={
          <p className="label-mono text-muted-foreground">
            {t('protection.lastSnapshotRun')} · <span className="text-foreground normal-case">{when(state?.lastSnapshotRun)}</span>
          </p>
        }
        scrubAside={
          scrubs.length === 0 ? (
            <p className="label-mono text-muted-foreground">
              {t('protection.lastScrub')} · <span className="text-foreground normal-case">{t('protection.never')}</span>
            </p>
          ) : (
            <ul className="flex flex-col gap-1">
              {scrubs.map(([pool, ts]) => (
                <li key={pool} className="label-mono text-muted-foreground">
                  {t('protection.lastScrubOf', { pool })} ·{' '}
                  <span className="text-foreground normal-case">{when(ts)}</span>
                </li>
              ))}
            </ul>
          )
        }
      />
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
      <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
        <Button onClick={() => void save()} busy={busy} disabled={invalid || unchanged}>
          {busy ? t('common.saving') : t('protection.save')}
        </Button>
        {!unchanged && !busy && (
          <Button variant="ghost" className="-ml-4" onClick={() => setValue(saved)}>
            {t('protection.reset')}
          </Button>
        )}
        <span className="text-sm text-status-ok" role="status">
          {message}
        </span>
      </div>
    </div>
  )
}
