// SPDX-License-Identifier: AGPL-3.0-or-later
// A status is always a word (+ icon), never color alone.
import { CircleCheck, CircleHelp, Info, type LucideIcon, OctagonX, TriangleAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { HealthStatus } from '../api/types'
import { cn } from '../lib/cn'
import { Icon } from './Icon'

export const STATUS_ICONS: Record<HealthStatus, LucideIcon> = {
  ok: CircleCheck,
  info: Info,
  warn: TriangleAlert,
  crit: OctagonX,
  unknown: CircleHelp,
}

export const STATUS_TEXT: Record<HealthStatus, string> = {
  ok: 'text-status-ok',
  info: 'text-status-info',
  warn: 'text-status-warn',
  crit: 'text-status-crit',
  unknown: 'text-status-unknown',
}

export const STATUS_BORDER: Record<HealthStatus, string> = {
  ok: 'border-status-ok',
  info: 'border-status-info',
  warn: 'border-status-warn',
  crit: 'border-status-crit',
  unknown: 'border-status-unknown',
}

interface StatusLabelProps {
  status: HealthStatus
  /** Overrides the default word ("OK", "Warning"…). */
  label?: string
  className?: string
}

export function StatusLabel({ status, label, className }: StatusLabelProps) {
  const { t } = useTranslation()
  return (
    <span className={cn('label-mono inline-flex items-center gap-1.5 whitespace-nowrap', STATUS_TEXT[status], className)}>
      <Icon icon={STATUS_ICONS[status]} size={14} className="shrink-0" />
      <span>{label ?? t(`status.${status}`)}</span>
    </span>
  )
}
