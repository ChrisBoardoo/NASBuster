// SPDX-License-Identifier: AGPL-3.0-or-later
// Health checks as full-width editorial rows. Rows reveal one by one (stagger).
import { m } from 'motion/react'
import { useTranslation } from 'react-i18next'
import type { Check } from '../api/types'
import { cn } from '../lib/cn'
import { countByStatus, useCheckText } from '../lib/messages'
import { useReveal } from '../lib/motion'
import { Notice } from './Notice'
import { StatusLabel } from './StatusLabel'

interface CheckListProps {
  checks: readonly Check[]
  /** Compact rows (dashboard): advice only for critical items. */
  compact?: boolean
}

export function CheckList({ checks, compact = false }: CheckListProps) {
  const describe = useCheckText()
  const { container, item } = useReveal()

  return (
    <m.ul variants={container} initial="hidden" animate="show" className="border-b border-border">
      {checks.map((check) => {
        const { name, message, advice, status } = describe(check)
        const showAdvice = advice && (!compact || status === 'crit')
        return (
          <m.li
            key={check.id}
            variants={item}
            className={cn(
              'grid gap-x-6 gap-y-2 border-t border-border',
              compact ? 'grid-cols-[1fr_auto] py-4' : 'py-5 md:grid-cols-12 md:py-6',
            )}
          >
            <p className={cn('label-mono pt-1 text-muted-foreground', compact ? 'col-span-2' : 'md:col-span-3')}>{name}</p>
            <div className={cn('flex min-w-0 flex-col gap-1.5', compact ? '' : 'md:col-span-6')}>
              <p className={cn('break-words', compact ? 'text-base' : 'text-lg md:text-xl')}>{message}</p>
              {showAdvice && (
                <p
                  className={cn(
                    'max-w-2xl text-sm',
                    status === 'crit' ? 'border-l-2 border-status-crit pl-3 text-foreground' : 'text-muted-foreground',
                  )}
                >
                  {advice}
                </p>
              )}
            </div>
            <div className={cn('pt-1', compact ? 'justify-self-end' : 'md:col-span-3 md:justify-self-end')}>
              <StatusLabel status={status} />
            </div>
          </m.li>
        )
      })}
    </m.ul>
  )
}

/** One sentence summarizing a set of checks, worst first. */
export function CheckSummary({ checks }: { checks: readonly Check[] }) {
  const { t } = useTranslation()
  const counts = countByStatus(checks)
  if (counts.crit > 0) {
    return (
      <Notice status="crit" assertive>
        <span className="text-foreground">{t('onboarding.health.hasCritical', { count: counts.crit })}</span>
      </Notice>
    )
  }
  if (counts.warn > 0) {
    return <Notice status="warn">{t('onboarding.health.hasWarnings', { count: counts.warn })}</Notice>
  }
  return <Notice status="ok">{t('onboarding.health.allGood')}</Notice>
}
