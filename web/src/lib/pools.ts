// SPDX-License-Identifier: AGPL-3.0-or-later
// Pure helpers for pools: layout ordering and defaults, capacity, health and scrub reading.
// Contract: md/8.architecture/api-v1.md, "Pools (M2)".
import type { Disk, HealthStatus, PoolLayout, PoolScrub, PoolStatus } from '../api/types'
import { DEFAULT_POOL_NAME, POOL_USAGE_CRIT, POOL_USAGE_WARN } from './constants'

/** Display order: most protective first, "single" (no safety net) last. */
const LAYOUT_ORDER = ['mirror', 'parity1', 'parity2', 'raid10', 'single']

/** Layouts in display order, the ones the available disks allow first (stable). */
export function orderLayouts(layouts: readonly PoolLayout[], available: number): PoolLayout[] {
  const rank = (name: string) => {
    const index = LAYOUT_ORDER.indexOf(name)
    return index < 0 ? LAYOUT_ORDER.length : index
  }
  return [...layouts].sort((a, b) => {
    const possible = Number(isLayoutPossible(b, available)) - Number(isLayoutPossible(a, available))
    return possible || rank(a.name) - rank(b.name)
  })
}

export function isLayoutPossible(layout: PoolLayout, available: number): boolean {
  return available >= layout.minDisks
}

/** Mirror as soon as two blank disks exist, else single (ADR-0008). */
export function recommendedLayout(layouts: readonly PoolLayout[], available: number): PoolLayout | null {
  const byName = (name: string) => layouts.find((l) => l.name === name && isLayoutPossible(l, available))
  return byName(available >= 2 ? 'mirror' : 'single') ?? layouts.find((l) => isLayoutPossible(l, available)) ?? null
}

export function selectionFits(layout: PoolLayout, count: number): boolean {
  return count >= layout.minDisks && (layout.maxDisks === 0 || count <= layout.maxDisks)
}

/** Default disks for a layout: the largest one for single, otherwise every blank disk. */
export function defaultSelection(layout: PoolLayout, available: readonly Disk[]): string[] {
  if (layout.maxDisks === 1) {
    const largest = [...available].sort((a, b) => b.sizeBytes - a.sizeBytes)[0]
    return largest ? [largest.id] : []
  }
  const ids = available.map((disk) => disk.id)
  return layout.maxDisks > 0 ? ids.slice(0, layout.maxDisks) : ids
}

/** First free name among tank, tank2, tank3… */
export function defaultPoolName(existing: readonly string[]): string {
  const taken = new Set(existing)
  if (!taken.has(DEFAULT_POOL_NAME)) return DEFAULT_POOL_NAME
  for (let n = 2; n < 100; n++) {
    const name = `${DEFAULT_POOL_NAME}${n}`
    if (!taken.has(name)) return name
  }
  return ''
}

export interface Capacity {
  /** Usable bytes (used + estimated free). */
  total: number
  used: number
  free: number
  /** used / total, 0–1 */
  ratio: number
}

/**
 * Usable capacity of a mounted pool, as the server computes it: `usableBytes` =
 * `logicalUsedBytes` + `freeBytes` (`sizeBytes`/`usedBytes` are raw device bytes, every RAID
 * copy included). Older servers without these fields: the same sum from the Btrfs profiles.
 */
export function poolCapacity(status: PoolStatus | null | undefined): Capacity | null {
  if (!status || !status.mounted) return null
  const free = status.freeBytes
  let used: number
  let total: number
  if (typeof status.usableBytes === 'number' && status.usableBytes > 0 && typeof status.logicalUsedBytes === 'number') {
    used = status.logicalUsedBytes
    total = status.usableBytes
  } else {
    const profiles = status.profiles.filter((p) => p.type !== 'GlobalReserve')
    used = profiles.length > 0 ? profiles.reduce((sum, p) => sum + p.usedBytes, 0) : status.usedBytes
    total = used + free
  }
  if (!Number.isFinite(total) || total <= 0) return null
  return { total, used, free, ratio: Math.min(1, Math.max(0, used / total)) }
}

export function usageStatus(ratio: number): HealthStatus {
  if (ratio >= POOL_USAGE_CRIT) return 'crit'
  if (ratio >= POOL_USAGE_WARN) return 'warn'
  return 'ok'
}

const HEALTH_STATUS: Record<string, HealthStatus> = {
  ok: 'ok',
  degraded: 'crit',
  errors: 'crit',
  unmounted: 'warn',
  unknown: 'unknown',
}

/** Pool health → the shared status scale (degraded and errors need action). */
export function poolHealthStatus(health: string | null | undefined): HealthStatus {
  return (health && HEALTH_STATUS[health]) || 'unknown'
}

export function scrubStatus(scrub: PoolScrub): HealthStatus {
  if (scrub.uncorrectable > 0) return 'crit'
  switch (scrub.status) {
    case 'finished':
      return scrub.corrected > 0 ? 'warn' : 'ok'
    case 'aborted':
    case 'interrupted':
      return 'warn'
    case 'never':
    case 'running':
      return 'info'
    default:
      return 'unknown'
  }
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/**
 * `btrfs scrub status` prints the start as "Thu Oct  1 23:11:06 2026" (server local time).
 * Returns Unix seconds read in the browser's time zone, or null if the format is unknown.
 */
export function parseScrubStarted(text: string): number | null {
  const match = /^[A-Za-z]{3}\s+([A-Za-z]{3})\s+(\d{1,2})\s+(\d{1,2}):(\d{2}):(\d{2})\s+(\d{4})$/.exec(text.trim())
  if (!match) return null
  const [, mon = '', day, hh, mm, ss, year] = match
  const month = MONTHS.indexOf(mon)
  if (month < 0) return null
  const date = new Date(Number(year), month, Number(day), Number(hh), Number(mm), Number(ss))
  const ts = date.getTime()
  return Number.isFinite(ts) ? Math.floor(ts / 1000) : null
}

/** Scrub start (Unix seconds): `startedAt`, else the btrfs text read in the browser's zone. */
export function scrubStartedAt(scrub: PoolScrub): number | null {
  if (typeof scrub.startedAt === 'number' && scrub.startedAt > 0) return scrub.startedAt
  return scrub.started ? parseScrubStarted(scrub.started) : null
}

/** Scrub duration (seconds): `durationSeconds`, else the "H:MM:SS" text. */
export function scrubDuration(scrub: PoolScrub): number | null {
  if (typeof scrub.durationSeconds === 'number' && scrub.durationSeconds > 0) return scrub.durationSeconds
  return scrub.duration ? parseDuration(scrub.duration) : null
}

/** "0:41:07" → 2467 seconds; null if the format is unknown. */
export function parseDuration(text: string): number | null {
  const match = /^(\d+):(\d{2}):(\d{2})$/.exec(text.trim())
  if (!match) return null
  return Number(match[1]) * 3600 + Number(match[2]) * 60 + Number(match[3])
}

/** Elapsed time as m:ss (or h:mm:ss), for the creation screen. */
export function clock(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const rest = String(s % 60).padStart(2, '0')
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${rest}` : `${m}:${rest}`
}

/** DOM id of a disk row on the Storage page (pool members link to it). */
export function diskAnchorId(diskId: string): string {
  return `disk-${diskId}`
}
