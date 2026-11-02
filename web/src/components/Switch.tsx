// SPDX-License-Identifier: AGPL-3.0-or-later
// On/off switch: a square track (no radius) plus the state as a word, never color alone.
import { useTranslation } from 'react-i18next'
import { cn } from '../lib/cn'

interface SwitchProps {
  checked: boolean
  onChange: (checked: boolean) => void
  /** Accessible name ("Snapshots", "ntfy"…). */
  label: string
  disabled?: boolean
}

export function Switch({ checked, onChange, label, disabled = false }: SwitchProps) {
  const { t } = useTranslation()
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className="inline-flex min-h-11 items-center gap-3 disabled:opacity-50"
    >
      <span
        aria-hidden="true"
        className={cn(
          'relative h-5 w-9 border transition-colors duration-150 ease-crisp',
          checked ? 'border-accent bg-accent' : 'border-border-hover bg-transparent',
        )}
      >
        <span
          className={cn(
            'absolute top-0.5 left-0.5 h-3.5 w-3.5 transition-transform duration-150 ease-crisp',
            checked ? 'translate-x-4 bg-accent-foreground' : 'bg-muted-foreground',
          )}
        />
      </span>
      <span className={cn('label-mono', checked ? 'text-foreground' : 'text-muted-foreground')}>
        {checked ? t('common.on') : t('common.off')}
      </span>
    </button>
  )
}
