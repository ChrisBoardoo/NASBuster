// SPDX-License-Identifier: AGPL-3.0-or-later
import { useTranslation } from 'react-i18next'
import { cn } from '../lib/cn'
import { Logo } from './Logo'

export function Wordmark({ className }: { className?: string }) {
  const { t } = useTranslation()
  return (
    <span className={cn('inline-flex items-center gap-3 font-mono text-sm font-semibold tracking-widest', className)}>
      <Logo size={32} />
      <span>{t('app.wordmark')}</span>
    </span>
  )
}
