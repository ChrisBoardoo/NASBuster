// SPDX-License-Identifier: AGPL-3.0-or-later
// Snapshot retention (hourly / daily / weekly / monthly) and scrub interval, validated live
// against the server bounds. Used by the onboarding's safety step and by Settings.
import { type ReactNode, useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { Protection } from '../api/types'
import { cn } from '../lib/cn'
import { RETENTION_TIERS, type RetentionTier, protectionProblems, retentionReach } from '../lib/protection'
import { INPUT_CLASSES } from './Field'
import { Switch } from './Switch'
import { RETENTION_MAX, SCRUB_DAYS_MAX, SCRUB_DAYS_MIN } from '../lib/validation'

interface ProtectionEditorProps {
  value: Protection
  onChange: (value: Protection) => void
  /** Settings shows the on/off switches; the onboarding keeps protection on. */
  showSwitches?: boolean
  disabled?: boolean
  /** Extra content under each block (explanations, last runs). */
  snapshotsAside?: ReactNode
  scrubAside?: ReactNode
}

const toInt = (raw: string) => (raw.trim() === '' ? Number.NaN : Number(raw))

export function ProtectionEditor({
  value,
  onChange,
  showSwitches = false,
  disabled = false,
  snapshotsAside,
  scrubAside,
}: ProtectionEditorProps) {
  const { t } = useTranslation()
  const problems = protectionProblems(value)
  const reach = retentionReach(value.snapshots.policy)
  const snapshotsOff = !value.snapshots.enabled
  const scrubOff = !value.scrub.enabled

  const setTier = (tier: RetentionTier, raw: string) =>
    onChange({
      ...value,
      snapshots: { ...value.snapshots, policy: { ...value.snapshots.policy, [tier]: toInt(raw) } },
    })

  return (
    <div className="flex flex-col gap-12">
      <section className="flex flex-col gap-5">
        <BlockTitle
          title={t('protection.snapshotsTitle')}
          switchLabel={showSwitches ? t('protection.snapshotsSwitch') : null}
          on={value.snapshots.enabled}
          disabled={disabled}
          onToggle={(enabled) => onChange({ ...value, snapshots: { ...value.snapshots, enabled } })}
        />
        <div className={cn('grid grid-cols-2 gap-x-6 gap-y-6 md:grid-cols-4', snapshotsOff && 'opacity-50')}>
          {RETENTION_TIERS.map((tier) => (
            <NumberField
              key={tier}
              label={t(`protection.tier.${tier}`)}
              value={value.snapshots.policy[tier]}
              min={0}
              max={RETENTION_MAX[tier]}
              disabled={disabled || snapshotsOff}
              onChange={(raw) => setTier(tier, raw)}
              hint={t(`protection.tierHint.${tier}`, { count: Number.isFinite(value.snapshots.policy[tier]) ? value.snapshots.policy[tier] : 0 })}
              error={problems.tiers[tier] ? t('protection.range', { min: 0, max: RETENTION_MAX[tier] }) : null}
            />
          ))}
        </div>
        <p className="text-sm text-muted-foreground" aria-live="polite">
          {problems.allZero
            ? null
            : reach && t(`protection.reach.${reach.unit}`, { count: reach.count })}
        </p>
        {problems.allZero && (
          <p className="text-sm text-status-crit" role="alert">
            {t('protection.allZero')}
          </p>
        )}
        {snapshotsAside}
      </section>

      <section className="flex flex-col gap-5">
        <BlockTitle
          title={t('protection.scrubTitle')}
          switchLabel={showSwitches ? t('protection.scrubSwitch') : null}
          on={value.scrub.enabled}
          disabled={disabled}
          onToggle={(enabled) => onChange({ ...value, scrub: { ...value.scrub, enabled } })}
        />
        <div className={cn('grid grid-cols-2 gap-x-6 md:grid-cols-4', scrubOff && 'opacity-50')}>
          <NumberField
            label={t('protection.interval')}
            value={value.scrub.intervalDays}
            min={SCRUB_DAYS_MIN}
            max={SCRUB_DAYS_MAX}
            disabled={disabled || scrubOff}
            onChange={(raw) => onChange({ ...value, scrub: { ...value.scrub, intervalDays: toInt(raw) } })}
            hint={t('protection.intervalHint', { count: Number.isFinite(value.scrub.intervalDays) ? value.scrub.intervalDays : 0 })}
            error={problems.interval ? t('protection.range', { min: SCRUB_DAYS_MIN, max: SCRUB_DAYS_MAX }) : null}
          />
        </div>
        {scrubAside}
      </section>
    </div>
  )
}

function BlockTitle({
  title,
  switchLabel,
  on,
  disabled,
  onToggle,
}: {
  title: string
  switchLabel: string | null
  on: boolean
  disabled: boolean
  onToggle: (on: boolean) => void
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2">
      <h3 className="text-2xl font-semibold tracking-tight md:text-3xl">{title}</h3>
      {switchLabel && <Switch checked={on} onChange={onToggle} label={switchLabel} disabled={disabled} />}
    </div>
  )
}

function NumberField({
  label,
  value,
  min,
  max,
  disabled,
  onChange,
  hint,
  error,
}: {
  label: string
  value: number
  min: number
  max: number
  disabled: boolean
  onChange: (raw: string) => void
  hint: string
  error: string | null
}) {
  const id = useId()
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <label htmlFor={id} className="label-mono text-muted-foreground">
        {label}
      </label>
      <input
        id={id}
        type="number"
        inputMode="numeric"
        min={min}
        max={max}
        step={1}
        disabled={disabled}
        value={Number.isFinite(value) ? value : ''}
        onChange={(event) => onChange(event.target.value)}
        aria-invalid={error ? true : undefined}
        aria-describedby={`${id}-hint`}
        className={cn(
          INPUT_CLASSES,
          'font-mono text-xl tabular-nums [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none',
        )}
      />
      <p id={`${id}-hint`} className={cn('text-sm', error ? 'text-status-crit' : 'text-muted-foreground')}>
        {error ?? hint}
      </p>
    </div>
  )
}
