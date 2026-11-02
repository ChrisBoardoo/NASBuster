// SPDX-License-Identifier: AGPL-3.0-or-later
// Snapshot retention and scrub interval: client-side mirror of the server bounds
// (`400 validation.protection`), and how far back the retention reaches.
import type { Protection, RetentionPolicy } from '../api/types'
import { RETENTION_MAX, SCRUB_DAYS_MAX, SCRUB_DAYS_MIN } from './validation'

export const RETENTION_TIERS = ['hourly', 'daily', 'weekly', 'monthly'] as const
export type RetentionTier = (typeof RETENTION_TIERS)[number]

export interface ProtectionProblems {
  tiers: Partial<Record<RetentionTier, 'range'>>
  /** Every tier is zero: nothing would be kept. */
  allZero: boolean
  interval: 'range' | null
}

const isInt = (n: number) => Number.isInteger(n)

export function protectionProblems(p: Protection): ProtectionProblems {
  const tiers: ProtectionProblems['tiers'] = {}
  for (const tier of RETENTION_TIERS) {
    const value = p.snapshots.policy[tier]
    if (!isInt(value) || value < 0 || value > RETENTION_MAX[tier]) tiers[tier] = 'range'
  }
  const allZero = RETENTION_TIERS.every((tier) => p.snapshots.policy[tier] === 0)
  const days = p.scrub.intervalDays
  const interval = !isInt(days) || days < SCRUB_DAYS_MIN || days > SCRUB_DAYS_MAX ? 'range' : null
  return { tiers, allZero, interval }
}

export function hasProblems(problems: ProtectionProblems): boolean {
  return Object.keys(problems.tiers).length > 0 || problems.allZero || problems.interval !== null
}

/** The oldest snapshot the policy keeps, as a count of its own unit (e.g. 6 months). */
export function retentionReach(policy: RetentionPolicy): { unit: RetentionTier; count: number } | null {
  for (const tier of ['monthly', 'weekly', 'daily', 'hourly'] as const) {
    if (policy[tier] > 0) return { unit: tier, count: policy[tier] }
  }
  return null
}

export function sameProtection(a: Protection, b: Protection): boolean {
  return (
    a.snapshots.enabled === b.snapshots.enabled &&
    a.scrub.enabled === b.scrub.enabled &&
    a.scrub.intervalDays === b.scrub.intervalDays &&
    RETENTION_TIERS.every((tier) => a.snapshots.policy[tier] === b.snapshots.policy[tier])
  )
}
