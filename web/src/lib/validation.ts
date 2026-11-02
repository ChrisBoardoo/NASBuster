// SPDX-License-Identifier: AGPL-3.0-or-later
// Client-side mirrors of the server rules in md/8.architecture/api-v1.md.
// The server stays authoritative; these only drive live hints.

export const PASSWORD_MIN = 10

/** 3–32 chars of [a-z0-9_-], starting with a letter. */
export const USERNAME_RE = /^[a-z][a-z0-9_-]{2,31}$/
/** Account of the machine (Raspberry Pi Imager, Debian installer): same rule as validate.MachineUser. */
export const MACHINE_USER_RE = /^[a-z_][a-z0-9_-]{0,31}$/

export type HostnameProblem = 'empty' | 'tooLong' | 'invalidChars' | 'hyphenEdge'

/** RFC 1123 label: 1–63 chars of [a-z0-9-], no hyphen at either end. */
export function hostnameProblem(hostname: string): HostnameProblem | null {
  if (hostname.length === 0) return 'empty'
  if (hostname.length > 63) return 'tooLong'
  if (!/^[a-z0-9-]+$/.test(hostname)) return 'invalidChars'
  if (hostname.startsWith('-') || hostname.endsWith('-')) return 'hyphenEdge'
  return null
}

/** Setup code `XXXX-XXXX`: keeps [A-Z0-9], uppercases, inserts the dash. */
export function formatSetupCode(input: string): string {
  const raw = input
    .toUpperCase()
    .replace(/[^A-Z0-9]/g, '')
    .slice(0, 8)
  return raw.length > 4 ? `${raw.slice(0, 4)}-${raw.slice(4)}` : raw
}

export function isSetupCodeComplete(code: string): boolean {
  return /^[A-Z0-9]{4}-[A-Z0-9]{4}$/.test(code)
}

/** Pool name: `^[a-z][a-z0-9]{1,23}$`, no dash (it would need escaping in systemd unit names). */
export const POOL_NAME_RE = /^[a-z][a-z0-9]{1,23}$/
export const POOL_NAME_MAX = 24

export type PoolNameProblem = 'empty' | 'startLetter' | 'invalidChars' | 'tooShort' | 'tooLong'

export function poolNameProblem(name: string): PoolNameProblem | null {
  if (name.length === 0) return 'empty'
  if (!/^[a-z]/.test(name)) return 'startLetter'
  if (!/^[a-z0-9]+$/.test(name)) return 'invalidChars'
  if (name.length < 2) return 'tooShort'
  if (name.length > POOL_NAME_MAX) return 'tooLong'
  return POOL_NAME_RE.test(name) ? null : 'invalidChars'
}

// ---- NAS users and shares (M4) ---------------------------------------------

/** NAS user name: `^[a-z][a-z0-9_-]{1,31}$`, never a system account. */
export const NAS_USER_RE = /^[a-z][a-z0-9_-]{1,31}$/
export const NAS_USER_MAX = 32
/** Mirrors the server's list; the server stays authoritative (`users.name_taken`). */
const RESERVED_USERS = new Set([
  'root', 'daemon', 'bin', 'sys', 'sync', 'games', 'man', 'lp', 'mail', 'news', 'uucp', 'proxy',
  'www-data', 'backup', 'list', 'irc', 'nobody', 'systemd-network', 'systemd-resolve', 'messagebus',
  'sshd', 'cwnsb', 'admin', 'administrator', 'guest', 'pi',
])

export type NasUserProblem = 'empty' | 'startLetter' | 'invalidChars' | 'tooShort' | 'tooLong' | 'reserved'

export function nasUserProblem(name: string): NasUserProblem | null {
  if (name.length === 0) return 'empty'
  if (!/^[a-z]/.test(name)) return 'startLetter'
  if (!/^[a-z0-9_-]+$/.test(name)) return 'invalidChars'
  if (name.length < 2) return 'tooShort'
  if (name.length > NAS_USER_MAX) return 'tooLong'
  if (RESERVED_USERS.has(name)) return 'reserved'
  return NAS_USER_RE.test(name) ? null : 'invalidChars'
}

/** NAS user password (sent to Samba, never stored by CWNSB): 8 to 128 characters, one line. */
export const NAS_PASSWORD_MIN = 8
export const NAS_PASSWORD_MAX = 128

export type NasPasswordProblem = 'tooShort' | 'tooLong' | 'multiline'

export function nasPasswordProblem(password: string): NasPasswordProblem | null {
  if (/[\r\n\0]/.test(password)) return 'multiline'
  if (password.length < NAS_PASSWORD_MIN) return 'tooShort'
  if (password.length > NAS_PASSWORD_MAX) return 'tooLong'
  return null
}

/** Share name: `^[a-z0-9][a-z0-9_-]{0,19}$`, not a Samba reserved section. */
export const SHARE_NAME_MAX = 20
const RESERVED_SHARES = new Set(['global', 'homes', 'printers', 'print$', 'ipc$'])

export type ShareNameProblem = 'empty' | 'startChar' | 'invalidChars' | 'tooLong' | 'reserved'

export function shareNameProblem(name: string): ShareNameProblem | null {
  if (name.length === 0) return 'empty'
  if (!/^[a-z0-9]/.test(name)) return 'startChar'
  if (!/^[a-z0-9_-]+$/.test(name)) return 'invalidChars'
  if (name.length > SHARE_NAME_MAX) return 'tooLong'
  if (RESERVED_SHARES.has(name)) return 'reserved'
  return null
}

// ---- Protection (M6a) ------------------------------------------------------

/** Retention bounds (at least one tier above zero) and scrub interval, as the server checks them. */
export const RETENTION_MAX = { hourly: 168, daily: 90, weekly: 104, monthly: 120 } as const
export const SCRUB_DAYS_MIN = 7
export const SCRUB_DAYS_MAX = 365
