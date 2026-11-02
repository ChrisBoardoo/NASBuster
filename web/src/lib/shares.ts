// SPDX-License-Identifier: AGPL-3.0-or-later
// Pure helpers for shares: network paths, connections, access lists.
import type { AccessMode, SessionsResponse, ShareAccess, SmbConnection, SmbSession } from '../api/types'

/** Windows path: \\host\share */
export function uncPath(host: string, share: string): string {
  return `\\\\${host}\\${share}`
}

/** macOS / Linux (mDNS): smb://host.local/share */
export function smbUrl(host: string, share: string): string {
  return `smb://${host}.local/${share}`
}

export interface ShareConnection {
  connection: SmbConnection
  /** The session of the same machine, for the user, dialect and encryption. */
  session: SmbSession | null
}

/** Clients currently connected to a share. */
export function connectionsTo(share: string, sessions: SessionsResponse | undefined): ShareConnection[] {
  if (!sessions) return []
  return sessions.connections
    .filter((c) => c.share === share)
    .map((connection) => ({
      connection,
      session: sessions.sessions.find((s) => s.machine === connection.machine) ?? null,
    }))
}

/** Session start: Unix seconds, or RFC 3339 text from older servers (null if unusable). */
export function sinceToUnix(value: number | string): number | null {
  if (typeof value === 'number') return Number.isFinite(value) && value > 0 ? value : null
  const ms = Date.parse(value)
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : null
}

/** "SMB3_11" → "SMB 3.1.1" */
export function dialectLabel(dialect: string): string {
  const match = /^SMB(\d)_?(\d)?(\d)?$/i.exec(dialect.replace(/\./g, '_'))
  if (!match) return dialect
  return `SMB ${[match[1], match[2], match[3]].filter(Boolean).join('.')}`
}

/** Access mode of a user on a share, or null when the user can't open it. */
export function modeOf(access: readonly ShareAccess[], user: string): AccessMode | null {
  const mode = access.find((a) => a.user === user)?.mode
  if (mode === 'rw') return 'rw'
  if (mode === 'ro') return 'ro'
  return null
}

/** Sets (or removes, with null) one user's mode, keeping the list order stable. */
export function withMode(access: readonly ShareAccess[], user: string, mode: AccessMode | null): ShareAccess[] {
  const others = access.filter((a) => a.user !== user)
  if (!mode) return others
  const index = access.findIndex((a) => a.user === user)
  const next = [...others]
  next.splice(index < 0 ? next.length : index, 0, { user, mode })
  return next
}

export function sameAccess(a: readonly ShareAccess[], b: readonly ShareAccess[]): boolean {
  if (a.length !== b.length) return false
  const key = (list: readonly ShareAccess[]) =>
    [...list]
      .map((x) => `${x.user}:${x.mode}`)
      .sort()
      .join(',')
  return key(a) === key(b)
}

/** DOM id of a share row on the Shares page (the pool's danger zone links to it). */
export const shareAnchorId = (share: string) => `share-${share}`
