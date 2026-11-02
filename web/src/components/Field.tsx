// SPDX-License-Identifier: AGPL-3.0-or-later
// Text input with label, hint and validation message (uxui_chart "Inputs").
import { Check, CircleAlert } from 'lucide-react'
import { type InputHTMLAttributes, type ReactNode, type Ref, useId } from 'react'
import { cn } from '../lib/cn'
import { Icon } from './Icon'

export const INPUT_CLASSES =
  'h-12 w-full border border-border bg-input px-4 text-base text-foreground outline-none transition-colors duration-150 ease-crisp ' +
  'placeholder:text-muted-foreground hover:border-border-hover focus:border-accent focus-visible:outline-none ' +
  'disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-status-crit md:h-14'

export interface FieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'id'> {
  label: ReactNode
  /** Neutral guidance, always visible. */
  hint?: ReactNode
  /** Blocking problem; marks the input invalid. */
  error?: string | null
  /** Positive confirmation (e.g. "Passwords match."). */
  success?: string | null
  mono?: boolean
  inputRef?: Ref<HTMLInputElement>
  /** Extra content under the input (previews, help blocks). */
  children?: ReactNode
}

export function Field({ label, hint, error, success, mono, inputRef, className, children, ...rest }: FieldProps) {
  const id = useId()
  const hintId = `${id}-hint`
  const messageId = `${id}-message`
  const describedBy = [hint ? hintId : null, error || success ? messageId : null].filter(Boolean).join(' ')

  return (
    <div className={cn('flex flex-col gap-2', className)}>
      <label htmlFor={id} className="label-mono text-muted-foreground">
        {label}
      </label>
      <input
        id={id}
        ref={inputRef}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy || undefined}
        className={cn(INPUT_CLASSES, mono && 'font-mono tracking-wider')}
        {...rest}
      />
      {hint && (
        <p id={hintId} className="text-sm text-muted-foreground">
          {hint}
        </p>
      )}
      <div id={messageId} aria-live="polite" className="empty:-mt-2">
        {error ? (
          <p className="flex items-start gap-2 text-sm text-status-crit">
            <Icon icon={CircleAlert} className="mt-0.5 shrink-0" />
            <span>{error}</span>
          </p>
        ) : success ? (
          <p className="flex items-start gap-2 text-sm text-status-ok">
            <Icon icon={Check} className="mt-0.5 shrink-0" />
            <span>{success}</span>
          </p>
        ) : null}
      </div>
      {children}
    </div>
  )
}
