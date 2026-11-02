// SPDX-License-Identifier: AGPL-3.0-or-later
// A compose file in a mono textarea, with its size against the 64 KiB limit and the warning
// that goes with any compose file: it can do anything root can do on the NAS.
import { CircleAlert } from 'lucide-react'
import { type Ref, useId } from 'react'
import { useTranslation } from 'react-i18next'
import { COMPOSE_MAX_BYTES, utf8Bytes } from '../../lib/apps'
import { cn } from '../../lib/cn'
import { useFormat } from '../../lib/useFormat'
import { Icon } from '../Icon'
import { Notice } from '../Notice'

interface ComposeFieldProps {
  value: string
  onChange: (value: string) => void
  label: string
  /** Compose's own message when the server refused the file (technical, English). */
  error?: string | null
  /** The translated sentence that goes with `error`. */
  errorTitle?: string | null
  disabled?: boolean
  textareaRef?: Ref<HTMLTextAreaElement>
  placeholder?: string
}

export function ComposeField({ value, onChange, label, error, errorTitle, disabled, textareaRef, placeholder }: ComposeFieldProps) {
  const { t } = useTranslation()
  const format = useFormat()
  const id = useId()
  const bytes = utf8Bytes(value)
  const tooBig = bytes > COMPOSE_MAX_BYTES
  const sizeId = `${id}-size`
  const errorId = `${id}-error`

  return (
    <div className="flex flex-col gap-4">
      <Notice status="warn" title={t('apps.compose.rootTitle')}>
        {t('apps.compose.rootBody')}
      </Notice>
      <div className="flex flex-col gap-2">
        <label htmlFor={id} className="label-mono text-muted-foreground">
          {label}
        </label>
        <textarea
          id={id}
          ref={textareaRef}
          value={value}
          disabled={disabled}
          placeholder={placeholder}
          onChange={(event) => onChange(event.target.value)}
          spellCheck={false}
          autoCapitalize="none"
          autoComplete="off"
          rows={14}
          aria-invalid={tooBig || error ? true : undefined}
          aria-describedby={[sizeId, error ? errorId : null].filter(Boolean).join(' ')}
          className={cn(
            'min-h-64 w-full resize-y border border-border bg-input px-4 py-3 font-mono text-sm leading-relaxed text-foreground outline-none',
            'transition-colors duration-150 ease-crisp placeholder:text-muted-foreground hover:border-border-hover focus:border-accent',
            'disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-status-crit',
          )}
        />
        <p id={sizeId} className={cn('label-mono tabular-nums', tooBig ? 'text-status-crit' : 'text-muted-foreground')}>
          {tooBig
            ? t('apps.compose.tooBig', { size: format.number(bytes / 1024, 1), max: format.number(COMPOSE_MAX_BYTES / 1024) })
            : bytes < 1024
              ? t('apps.compose.sizeBytes', { count: bytes, max: format.number(COMPOSE_MAX_BYTES / 1024) })
              : t('apps.compose.size', { size: format.number(bytes / 1024, 1), max: format.number(COMPOSE_MAX_BYTES / 1024) })}
        </p>
        {error && (
          <div id={errorId} role="alert" className="flex flex-col gap-2 border-l-2 border-status-crit pl-4">
            <p className="flex items-start gap-2 text-sm text-status-crit">
              <Icon icon={CircleAlert} className="mt-0.5 shrink-0" />
              <span>{errorTitle ?? t('errors.apps.compose_invalid')}</span>
            </p>
            <code className="block max-w-full overflow-x-auto font-mono text-xs whitespace-pre-wrap break-all text-foreground">{error}</code>
          </div>
        )}
      </div>
    </div>
  )
}
