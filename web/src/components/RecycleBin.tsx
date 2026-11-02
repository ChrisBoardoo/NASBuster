// SPDX-License-Identifier: AGPL-3.0-or-later
// A share's recycle bin: what people delete from their computer goes to a visible "#recycle"
// folder, where they can take it back themselves, until it's purged after N days. It covers
// what the hourly snapshots miss (a file created and deleted within the hour).
import { Trash } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { setShareRecycle } from '../api/endpoints'
import type { Share } from '../api/types'
import { useErrorMessage } from '../lib/messages'
import { Button } from './Button'
import { Field } from './Field'
import { Icon } from './Icon'
import { Notice } from './Notice'
import { StatusLabel } from './StatusLabel'
import { Switch } from './Switch'

export const RECYCLE_DEFAULT_DAYS = 30
export const RECYCLE_MAX_DAYS = 365

/** Whole days, 1 to 365. */
export function parseDays(text: string): { value: number; valid: boolean } {
  const trimmed = text.trim()
  if (!/^\d+$/.test(trimmed)) return { value: 0, valid: false }
  const value = Number(trimmed)
  return { value, valid: value >= 1 && value <= RECYCLE_MAX_DAYS }
}

interface RecycleFieldsProps {
  enabled: boolean
  onEnabled: (enabled: boolean) => void
  days: string
  onDays: (days: string) => void
  disabled?: boolean
  /** false when a heading above already names it. */
  showLabel?: boolean
}

export function RecycleFields({ enabled, onEnabled, days, onDays, disabled = false, showLabel = true }: RecycleFieldsProps) {
  const { t } = useTranslation()
  const parsed = parseDays(days)
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1">
        {showLabel && <span className="label-mono text-muted-foreground">{t('recycle.label')}</span>}
        <Switch checked={enabled} onChange={onEnabled} label={t('recycle.label')} disabled={disabled} />
      </div>
      <ul className="flex max-w-2xl list-disc flex-col gap-1.5 pl-5 text-sm text-muted-foreground marker:text-border-hover">
        <li>{t('recycle.what')}</li>
        <li>{t('recycle.restore')}</li>
        <li>{t('recycle.why')}</li>
      </ul>
      {enabled && (
        <Field
          label={t('recycle.daysLabel')}
          name="recycle-days"
          inputMode="numeric"
          autoComplete="off"
          className="max-w-xs [&_input]:font-mono"
          value={days}
          disabled={disabled}
          onChange={(event) => onDays(event.target.value)}
          hint={parsed.valid ? t('recycle.daysHint', { count: parsed.value }) : undefined}
          error={parsed.valid ? null : t('recycle.daysError', { max: RECYCLE_MAX_DAYS })}
        />
      )}
    </div>
  )
}

export function RecycleLabel({ share }: { share: Pick<Share, 'recycle' | 'recycleDays'> }) {
  const { t } = useTranslation()
  return share.recycle ? (
    <StatusLabel status="ok" label={t('recycle.on', { count: share.recycleDays })} />
  ) : (
    <StatusLabel status="unknown" label={t('recycle.off')} />
  )
}

export function RecyclePanel({ share, onSaved }: { share: Share; onSaved: (share: Share) => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [editing, setEditing] = useState(false)
  const [enabled, setEnabled] = useState(share.recycle)
  const [days, setDays] = useState(String(share.recycleDays || RECYCLE_DEFAULT_DAYS))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const parsed = parseDays(days)
  const unchanged = enabled === share.recycle && (!enabled || parsed.value === share.recycleDays)
  const invalid = enabled && !parsed.valid

  const open = () => {
    setEnabled(true) // "Turn on…" and "Change…" both start with the bin on
    setDays(String(share.recycleDays || RECYCLE_DEFAULT_DAYS))
    setError(null)
    setEditing(true)
  }

  const save = async () => {
    if (unchanged || invalid) return
    setBusy(true)
    setError(null)
    try {
      const saved = await setShareRecycle(share.name, enabled, parsed.value)
      setEditing(false)
      onSaved(saved)
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2">
        <h4 className="label-mono text-muted-foreground">{t('recycle.label')}</h4>
        {!editing && (
          <Button variant="ghost" size="sm" className="-mr-3" icon={<Icon icon={Trash} />} onClick={open}>
            {share.recycle ? t('recycle.change') : t('recycle.turnOn')}
          </Button>
        )}
      </div>
      {!editing ? (
        <RecycleLabel share={share} />
      ) : (
        <div
          role="group"
          aria-label={t('recycle.label')}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && event.target instanceof HTMLInputElement) {
              event.preventDefault()
              void save()
            }
          }}
          className="flex flex-col gap-5 border-l-2 border-accent pl-5"
        >
          <RecycleFields enabled={enabled} onEnabled={setEnabled} days={days} onDays={setDays} disabled={busy} showLabel={false} />
          {!enabled && share.recycle && <Notice status="warn">{t('recycle.offWarning')}</Notice>}
          {error !== null && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
          <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
            <Button busy={busy} disabled={unchanged || invalid} onClick={() => void save()}>
              {busy ? t('common.saving') : t('recycle.save')}
            </Button>
            <Button variant="ghost" className="-ml-4" onClick={() => setEditing(false)} disabled={busy}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      )}
    </section>
  )
}
