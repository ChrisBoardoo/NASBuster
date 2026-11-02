// SPDX-License-Identifier: AGPL-3.0-or-later
// Inline message block: status rule on the left, icon + words, optional actions.
import type { ReactNode } from 'react'
import type { HealthStatus } from '../api/types'
import { cn } from '../lib/cn'
import { Icon } from './Icon'
import { STATUS_BORDER, STATUS_ICONS, STATUS_TEXT } from './StatusLabel'

interface NoticeProps {
  status: HealthStatus
  title?: string
  children?: ReactNode
  actions?: ReactNode
  /** Announce immediately (errors that follow a user action). */
  assertive?: boolean
  className?: string
}

export function Notice({ status, title, children, actions, assertive, className }: NoticeProps) {
  return (
    <div
      role={assertive ? 'alert' : 'status'}
      className={cn('flex gap-4 border-l-2 py-1 pl-4', STATUS_BORDER[status], className)}
    >
      <Icon icon={STATUS_ICONS[status]} size={20} className={cn('mt-0.5 shrink-0', STATUS_TEXT[status])} />
      <div className="flex min-w-0 flex-col gap-2">
        {title && <p className="font-semibold text-foreground">{title}</p>}
        {children && <div className="text-muted-foreground">{children}</div>}
        {actions && <div className="flex flex-wrap items-center gap-x-6 gap-y-2">{actions}</div>}
      </div>
    </div>
  )
}
