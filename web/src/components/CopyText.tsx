// SPDX-License-Identifier: AGPL-3.0-or-later
// A value to type elsewhere (\\host\share, smb://…) with a Copy button. The Clipboard API only
// exists on secure origins (HTTPS, localhost), so plain-HTTP LAN access falls back to execCommand.
import { Check, Copy } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '../lib/cn'
import { Button } from './Button'
import { Icon } from './Icon'

async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    /* fall back below */
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.className = 'fixed -left-[9999px] opacity-0'
  document.body.appendChild(area)
  area.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  area.remove()
  return ok
}

interface CopyTextProps {
  value: string
  /** Accessible name of the copy button, e.g. "Copy \\nas\docs". */
  label: string
  /** "poster": the onboarding's big path; "inline": a row on the Shares page. */
  size?: 'poster' | 'inline'
  className?: string
}

export function CopyText({ value, label, size = 'inline', className }: CopyTextProps) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!copied) return
    const id = window.setTimeout(() => setCopied(false), 2000)
    return () => window.clearTimeout(id)
  }, [copied])

  const copy = async () => setCopied(await copyToClipboard(value))

  return (
    <div className={cn('flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2', className)}>
      <code
        className={cn(
          'min-w-0 font-mono break-all select-all',
          size === 'poster'
            ? 'text-[length:clamp(1.75rem,6.5vw,4.5rem)] leading-tight font-semibold tracking-tight'
            : 'text-base',
        )}
      >
        {value}
      </code>
      <Button
        variant={size === 'poster' ? 'secondary' : 'ghost'}
        size="sm"
        className={size === 'poster' ? undefined : '-ml-3'}
        icon={<Icon icon={copied ? Check : Copy} />}
        onClick={() => void copy()}
        aria-label={copied ? undefined : label}
      >
        {copied ? t('common.copied') : t('common.copy')}
      </Button>
      <span className="sr-only" role="status">
        {copied ? t('common.copied') : ''}
      </span>
    </div>
  )
}
