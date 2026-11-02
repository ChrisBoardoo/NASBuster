// SPDX-License-Identifier: AGPL-3.0-or-later
// Settings → system journal (ADR-0031): kept across restarts by default, so that the
// journal can still say what happened before a power cut. Raspberry Pi OS keeps it in
// memory only, to spare the SD card: the measured cost is shown next to the switch.
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'wouter'
import { getJournalSettings, putJournalSettings } from '../api/endpoints'
import type { JournalSettings as Settings } from '../api/types'
import { useApi } from '../api/useApi'
import { useErrorMessage } from '../lib/messages'
import { InlineLoading } from './Loading'
import { Notice } from './Notice'
import { Switch } from './Switch'

export function JournalSettings() {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const view = useApi((signal) => getJournalSettings({ signal }), [])
  const [saved, setSaved] = useState<Settings | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)

  if (!view.data) {
    if (view.error) return <Notice status="crit">{errorMessage(view.error)}</Notice>
    return <InlineLoading />
  }
  const settings = saved ?? view.data

  const change = async (persistent: boolean) => {
    setBusy(true)
    setError(null)
    try {
      setSaved(await putJournalSettings(persistent))
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex max-w-2xl flex-col gap-4">
      {settings.managedBy ? (
        <Notice status="info">
          <span className="text-foreground">{t('journal.settings.managedBy', { file: settings.managedBy })}</span>
        </Notice>
      ) : (
        <>
          <Switch
            checked={settings.persistent}
            onChange={(on) => void change(on)}
            label={t('journal.settings.switch')}
            disabled={busy}
          />
          <p className="text-muted-foreground">{t(settings.persistent ? 'journal.settings.on' : 'journal.settings.off')}</p>
          <p className="text-sm text-muted-foreground">{t('journal.settings.cost')}</p>
          {settings.persistent && !settings.active && (
            <p className="text-sm text-status-warn">{t('journal.settings.pending')}</p>
          )}
        </>
      )}
      {error !== null && <Notice status="crit">{errorMessage(error)}</Notice>}
      <Link href="/journal" className="label-mono self-start underline decoration-accent underline-offset-4">
        {t('journal.settings.open')}
      </Link>
    </div>
  )
}
