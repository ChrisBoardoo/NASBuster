// SPDX-License-Identifier: AGPL-3.0-or-later
// A square checkbox (no radius) with its label: the whole line is the 44px target.
import { Check } from 'lucide-react'
import { type ReactNode, useId } from 'react'
import { cn } from '../lib/cn'
import { Icon } from './Icon'

interface CheckboxProps {
  checked: boolean
  onChange: (checked: boolean) => void
  label: ReactNode
  /** A second line under the label. */
  description?: ReactNode
  disabled?: boolean
  /** Marks a destructive choice (accent box when checked). */
  danger?: boolean
}

export function Checkbox({ checked, onChange, label, description, disabled = false, danger = false }: CheckboxProps) {
  const id = useId()
  return (
    <label
      className={cn(
        'group flex min-h-11 items-start gap-3 py-2',
        disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer',
        'has-[input:focus-visible]:outline-2 has-[input:focus-visible]:outline-offset-2 has-[input:focus-visible]:outline-ring',
      )}
    >
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        aria-describedby={description ? `${id}-desc` : undefined}
        className="sr-only"
      />
      <span
        aria-hidden="true"
        className={cn(
          'mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center border transition-colors duration-150 ease-crisp',
          checked
            ? danger
              ? 'border-status-crit bg-status-crit text-accent-foreground'
              : 'border-accent bg-accent text-accent-foreground'
            : 'border-foreground group-hover:border-accent',
        )}
      >
        {checked && <Icon icon={Check} size={14} strokeWidth={2.5} />}
      </span>
      <span className="flex min-w-0 flex-col gap-1">
        <span className="text-foreground">{label}</span>
        {description && (
          <span id={`${id}-desc`} className="text-sm text-muted-foreground">
            {description}
          </span>
        )}
      </span>
    </label>
  )
}
