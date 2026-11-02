// SPDX-License-Identifier: AGPL-3.0-or-later
// Minimal typographic loading states: a hairline with a short accent bar scanning it.
import { useTranslation } from 'react-i18next'
import { cn } from '../lib/cn'
import { Wordmark } from './Wordmark'

export function ScanLine({ className, thick = false }: { className?: string; thick?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={cn('relative block overflow-hidden bg-border', thick ? 'h-0.5' : 'h-px', className)}
    >
      <span className="animate-scan absolute inset-y-0 left-0 w-1/4 bg-accent" />
    </span>
  )
}

/** Full-screen state while the app boots. */
export function LoadingScreen() {
  const { t } = useTranslation()
  return (
    <div className="flex min-h-dvh items-center justify-center px-6" role="status" aria-live="polite">
      <div className="flex w-56 flex-col gap-4">
        <Wordmark />
        <ScanLine />
        <span className="sr-only">{t('app.loading')}</span>
      </div>
    </div>
  )
}

/** Inline state inside a page or a section. */
export function InlineLoading({ label, className }: { label?: string; className?: string }) {
  const { t } = useTranslation()
  return (
    <div className={cn('flex flex-col gap-3 py-8', className)} role="status" aria-live="polite">
      <span className="label-mono text-muted-foreground">{label ?? t('common.loading')}</span>
      <ScanLine className="w-40" />
    </div>
  )
}
