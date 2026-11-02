// SPDX-License-Identifier: AGPL-3.0-or-later
// Time Machine on a share (M4b): Macs see it as a backup destination by themselves, it gets no
// snapshots (Time Machine keeps its own versions), and a size limit keeps backups from filling
// the pool. Turning it on or off restarts Samba: connected computers drop for a moment.
import { Clock } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { setShareTimeMachine } from '../api/endpoints'
import type { Share } from '../api/types'
import { useErrorMessage } from '../lib/messages'
import { useFormat } from '../lib/useFormat'
import { Button } from './Button'
import { Field } from './Field'
import { Icon } from './Icon'
import { Notice } from './Notice'
import { StatusLabel } from './StatusLabel'
import { Switch } from './Switch'

/** The server's bound (1 PiB). */
export const TIME_MACHINE_MAX_GIB = 1 << 20
const GIB = 1024 ** 3

export type LimitProblem = 'notNumber' | 'tooBig'

/** "" = no limit (0). */
export function parseLimit(text: string): { value: number; problem: LimitProblem | null } {
  const trimmed = text.trim()
  if (trimmed === '') return { value: 0, problem: null }
  if (!/^\d+$/.test(trimmed)) return { value: 0, problem: 'notNumber' }
  const value = Number(trimmed)
  return { value, problem: value > TIME_MACHINE_MAX_GIB ? 'tooBig' : null }
}

interface TimeMachineFieldsProps {
  enabled: boolean
  onEnabled: (enabled: boolean) => void
  limit: string
  onLimit: (limit: string) => void
  /** The pool's free space, to warn about a limit above it. */
  freeBytes?: number | null
  disabled?: boolean
  /** false when a heading above already says "Time Machine". */
  showLabel?: boolean
}

/** The switch, what it means, and the size limit (only while it's on). */
export function TimeMachineFields({ enabled, onEnabled, limit, onLimit, freeBytes, disabled = false, showLabel = true }: TimeMachineFieldsProps) {
  const { t } = useTranslation()
  const format = useFormat()
  const parsed = parseLimit(limit)
  const bytes = parsed.value * GIB
  const aboveFree = !parsed.problem && parsed.value > 0 && typeof freeBytes === 'number' && freeBytes > 0 && bytes > freeBytes

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1">
        {showLabel && <span className="label-mono text-muted-foreground">{t('timeMachine.label')}</span>}
        <Switch checked={enabled} onChange={onEnabled} label={t('timeMachine.label')} disabled={disabled} />
      </div>
      <ul className="flex max-w-2xl list-disc flex-col gap-1.5 pl-5 text-sm text-muted-foreground marker:text-border-hover">
        <li>{t('timeMachine.macs')}</li>
        <li>{t('timeMachine.noSnapshots')}</li>
        <li>{t('timeMachine.account')}</li>
      </ul>
      {enabled && (
        <Field
          label={t('timeMachine.limitLabel')}
          name="time-machine-limit"
          inputMode="numeric"
          autoComplete="off"
          placeholder={t('timeMachine.noLimit')}
          className="max-w-xs [&_input]:font-mono"
          value={limit}
          disabled={disabled}
          onChange={(event) => onLimit(event.target.value)}
          hint={t('timeMachine.limitHint')}
          error={parsed.problem ? t(`timeMachine.limitErrors.${parsed.problem}`, { max: format.number(TIME_MACHINE_MAX_GIB) }) : null}
        >
          {!parsed.problem && (
            <p className="label-mono text-muted-foreground tabular-nums">
              {parsed.value === 0 ? t('timeMachine.unlimited') : t('timeMachine.equals', { size: format.bytes(bytes) })}
            </p>
          )}
          {aboveFree && typeof freeBytes === 'number' && (
            <p className="text-sm text-status-warn">{t('timeMachine.aboveFree', { free: format.bytes(freeBytes) })}</p>
          )}
        </Field>
      )}
    </div>
  )
}

/** "On · 1,000 GiB at most" / "Off", never color alone. */
export function TimeMachineLabel({ share }: { share: Pick<Share, 'timeMachine' | 'timeMachineMaxGiB'> }) {
  const { t } = useTranslation()
  const format = useFormat()
  if (!share.timeMachine) return <StatusLabel status="unknown" label={t('timeMachine.off')} />
  return (
    <StatusLabel
      status="info"
      label={
        share.timeMachineMaxGiB > 0
          ? t('timeMachine.onLimited', { gib: format.number(share.timeMachineMaxGiB) })
          : t('timeMachine.onUnlimited')
      }
    />
  )
}

interface TimeMachinePanelProps {
  share: Share
  /** Computers connected to any share right now (they drop when Samba restarts). */
  connected: number
  freeBytes?: number | null
  onSaved: (share: Share) => void
}

/** On a share's row: the current setting, then a change made explicit (restart warning). */
export function TimeMachinePanel({ share, connected, freeBytes, onSaved }: TimeMachinePanelProps) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [editing, setEditing] = useState(false)
  const [enabled, setEnabled] = useState(share.timeMachine)
  const [limit, setLimit] = useState(share.timeMachineMaxGiB > 0 ? String(share.timeMachineMaxGiB) : '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const parsed = parseLimit(limit)
  const maxGiB = enabled ? parsed.value : 0
  const unchanged = enabled === share.timeMachine && maxGiB === share.timeMachineMaxGiB
  const restarts = enabled !== share.timeMachine

  const open = () => {
    setEnabled(share.timeMachine)
    setLimit(share.timeMachineMaxGiB > 0 ? String(share.timeMachineMaxGiB) : '')
    setError(null)
    setEditing(true)
  }

  const save = async () => {
    if (unchanged || (enabled && parsed.problem)) return
    setBusy(true)
    setError(null)
    try {
      const saved = await setShareTimeMachine(share.name, enabled, maxGiB)
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
        <h4 className="label-mono text-muted-foreground">{t('timeMachine.label')}</h4>
        {!editing && (
          <Button variant="ghost" size="sm" className="-mr-3" icon={<Icon icon={Clock} />} onClick={open}>
            {share.timeMachine ? t('timeMachine.change') : t('timeMachine.turnOn')}
          </Button>
        )}
      </div>
      {!editing ? (
        <TimeMachineLabel share={share} />
      ) : (
        <div
          role="group"
          aria-label={t('timeMachine.label')}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && event.target instanceof HTMLInputElement) {
              event.preventDefault()
              void save()
            }
          }}
          className="flex flex-col gap-5 border-l-2 border-accent pl-5"
        >
          <TimeMachineFields
            enabled={enabled}
            onEnabled={setEnabled}
            limit={limit}
            onLimit={setLimit}
            freeBytes={freeBytes}
            disabled={busy}
            showLabel={false}
          />
          {restarts && (
            <Notice status="warn" title={t('timeMachine.restartTitle')}>
              {connected > 0 ? t('timeMachine.restartConnected', { count: connected }) : t('timeMachine.restartBody')}
            </Notice>
          )}
          {error !== null && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
          <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
            <Button busy={busy} disabled={unchanged || (enabled && parsed.problem !== null)} onClick={() => void save()}>
              {busy ? t('common.saving') : restarts ? (enabled ? t('timeMachine.saveOn') : t('timeMachine.saveOff')) : t('timeMachine.save')}
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
