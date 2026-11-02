// SPDX-License-Identifier: AGPL-3.0-or-later
// A configuration restore ends every session and restarts the daemon (ADR-0014). What it
// reported (accounts restored, conflicts) is kept for this tab so the login page can still
// show it, whichever way the page got there.
import type { ConfigBackup, ConfigRestoreResult } from '../api/types'

const KEY = 'cwnsb.restored'
const MAX_AGE_MS = 30 * 60 * 1000

export interface RestoreNotice {
  users: string[]
  conflicts: string[]
  /** Date.now() at the restore. */
  at: number
}

export function saveRestoreNotice(result: ConfigRestoreResult): void {
  try {
    const notice: RestoreNotice = { users: result.users, conflicts: result.conflicts, at: Date.now() }
    window.sessionStorage.setItem(KEY, JSON.stringify(notice))
  } catch {
    /* storage unavailable: the restore screen already showed it */
  }
}

export function readRestoreNotice(): RestoreNotice | null {
  try {
    const raw: unknown = JSON.parse(window.sessionStorage.getItem(KEY) ?? 'null')
    if (typeof raw !== 'object' || raw === null) return null
    const notice = raw as Partial<RestoreNotice>
    if (typeof notice.at !== 'number' || Date.now() - notice.at > MAX_AGE_MS) return null
    return {
      users: Array.isArray(notice.users) ? notice.users.map(String) : [],
      conflicts: Array.isArray(notice.conflicts) ? notice.conflicts.map(String) : [],
      at: notice.at,
    }
  } catch {
    return null
  }
}

export function clearRestoreNotice(): void {
  try {
    window.sessionStorage.removeItem(KEY)
  } catch {
    /* nothing to clear */
  }
}

/** Backups of the same name on several pools are copies of one backup. */
export interface BackupGroup {
  name: string
  time: number
  hostname: string
  users: number
  sizeBytes: number
  installation: string
  admin: string
  /** -1 when unknown. */
  shares: number
  thisInstallation: boolean
  pools: string[]
}

export function groupBackups(backups: readonly ConfigBackup[]): BackupGroup[] {
  const groups = new Map<string, BackupGroup>()
  for (const b of backups) {
    const group = groups.get(b.name)
    if (group) {
      if (!group.pools.includes(b.pool)) group.pools.push(b.pool)
    } else {
      const { pool, ...rest } = b
      groups.set(b.name, { ...rest, installation: b.installation ?? '', admin: b.admin ?? '', shares: b.shares ?? -1, pools: [pool] })
    }
  }
  return [...groups.values()].sort((a, b) => b.time - a.time)
}

/** Kind of origin, for the label of a backup (P6). */
export type BackupOrigin = 'this' | 'other' | 'older'

export function backupOrigin(group: BackupGroup): BackupOrigin {
  if (group.thisInstallation) return 'this'
  return group.installation ? 'other' : 'older'
}
