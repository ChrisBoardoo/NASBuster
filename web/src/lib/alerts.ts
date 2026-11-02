// SPDX-License-Identifier: AGPL-3.0-or-later
// Pure helpers for alerts (ADR-0011): an alert is a state, active while resolvedAt is 0.
import type { Alert, HealthStatus } from '../api/types'

export const ALERTS_POLL_MS = 60_000
export const ALERTS_RESOLVED_DAYS = 7

export const isActive = (alert: Alert) => alert.resolvedAt === 0
/** Active and not yet marked as seen: what the bell counts and highlights. */
export const needsAttention = (alert: Alert) => isActive(alert) && alert.ackedAt === 0

export function alertStatus(level: string): HealthStatus {
  return level === 'crit' ? 'crit' : level === 'warn' ? 'warn' : 'info'
}

const LEVEL_RANK: Record<string, number> = { crit: 0, warn: 1 }

/** Active first (crit, then warn, newest first), then resolved (most recently resolved first). */
export function sortAlerts(alerts: readonly Alert[]): Alert[] {
  return [...alerts].sort((a, b) => {
    if (isActive(a) !== isActive(b)) return isActive(a) ? -1 : 1
    if (isActive(a)) {
      return (LEVEL_RANK[a.level] ?? 2) - (LEVEL_RANK[b.level] ?? 2) || b.firstSeen - a.firstSeen
    }
    return b.resolvedAt - a.resolvedAt
  })
}

export interface AlertCounts {
  /** Active and not acknowledged. */
  attention: number
  /** Of which critical. */
  critical: number
}

export function countAlerts(alerts: readonly Alert[]): AlertCounts {
  const pending = alerts.filter(needsAttention)
  return { attention: pending.length, critical: pending.filter((a) => a.level === 'crit').length }
}
