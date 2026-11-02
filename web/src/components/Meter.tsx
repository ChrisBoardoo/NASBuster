// SPDX-License-Identifier: AGPL-3.0-or-later
// A single ratio against a limit (pool usage, md sync): a hairline track in the border
// color with a fill that carries the state. Never color alone: callers always print the
// value and, when it matters, a status word next to it.
import type { HealthStatus } from '../api/types'
import { cn } from '../lib/cn'

const FILL: Record<HealthStatus, string> = {
  ok: 'bg-foreground',
  info: 'bg-status-info',
  warn: 'bg-status-warn',
  crit: 'bg-status-crit',
  unknown: 'bg-status-unknown',
}

interface MeterProps {
  /** 0–1 */
  value: number
  /** Accessible name, e.g. "tank". */
  label: string
  /** Spoken value, e.g. "444 GB used of 4.0 TB". */
  valueText: string
  status?: HealthStatus
  /** "meter" for a level (usage), "progressbar" for a task that completes (sync). */
  role?: 'meter' | 'progressbar'
  className?: string
}

export function Meter({ value, label, valueText, status = 'ok', role = 'meter', className }: MeterProps) {
  const percent = Math.round(Math.min(1, Math.max(0, Number.isFinite(value) ? value : 0)) * 1000) / 10
  return (
    <div
      role={role}
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={percent}
      aria-valuetext={valueText}
      className={cn('relative h-2 w-full overflow-hidden bg-border', className)}
    >
      {/* Width via the CSSOM (React style), which the CSP allows; no inline style attribute in HTML. */}
      <div
        className={cn('h-full transition-[width] duration-500 ease-crisp', FILL[status])}
        style={{ width: `${percent}%` }}
      />
    </div>
  )
}
