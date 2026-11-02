// SPDX-License-Identifier: AGPL-3.0-or-later
// Header bell: number of active alerts not yet marked as seen. Critical ones switch the
// glyph (ringing bell) and the badge to the critical color: never color alone.
import { Bell, BellRing } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Link, useLocation } from 'wouter'
import { cn } from '../lib/cn'
import { useAlerts } from '../session/AlertsContext'
import { Icon } from './Icon'

export function AlertBell() {
  const { t } = useTranslation()
  const [location] = useLocation()
  const { counts } = useAlerts()
  const active = location === '/alerts'
  const critical = counts.critical > 0
  const label =
    counts.attention === 0
      ? t('alerts.bell.none')
      : critical
        ? t('alerts.bell.critical', { count: counts.attention, critical: counts.critical })
        : t('alerts.bell.some', { count: counts.attention })

  return (
    <Link
      href="/alerts"
      aria-label={label}
      title={label}
      aria-current={active ? 'page' : undefined}
      className={cn(
        'relative inline-flex min-h-11 min-w-11 items-center justify-center transition-colors duration-150 ease-crisp',
        critical ? 'text-status-crit' : active ? 'text-accent' : 'text-muted-foreground hover:text-foreground',
      )}
    >
      <Icon icon={critical ? BellRing : Bell} size={20} />
      {counts.attention > 0 && (
        <span
          aria-hidden="true"
          className={cn(
            'absolute top-1 right-0 min-w-4 px-1 text-center font-mono text-[0.625rem] leading-4 font-semibold tabular-nums',
            critical ? 'bg-status-crit text-accent-foreground' : 'bg-status-warn text-background',
          )}
        >
          {counts.attention > 9 ? '9+' : counts.attention}
        </span>
      )}
    </Link>
  )
}
