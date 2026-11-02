// SPDX-License-Identifier: AGPL-3.0-or-later
// Notification channels: random ntfy topics and the URL rules the server enforces
// (`400 validation.url`). The server stays authoritative.

export const NTFY_PUBLIC_SERVER = 'https://ntfy.sh'
const TOPIC_PREFIX = 'cwnsb-'
const TOPIC_RANDOM_LENGTH = 20
const ALPHABET = 'abcdefghijklmnopqrstuvwxyz0123456789'
export const NOTIFICATION_URL_MAX = 512
export const NTFY_TOKEN_MAX = 256

/**
 * A public ntfy topic can be read by anyone who guesses its name, so the name is long and
 * random: "cwnsb-" + 20 characters of [a-z0-9] from crypto.getRandomValues (≈ 103 bits).
 * Rejection sampling keeps every character equally likely.
 */
export function randomTopic(): string {
  const out: string[] = []
  const limit = 256 - (256 % ALPHABET.length)
  while (out.length < TOPIC_RANDOM_LENGTH) {
    const bytes = new Uint8Array(32)
    crypto.getRandomValues(bytes)
    for (const byte of bytes) {
      if (byte < limit && out.length < TOPIC_RANDOM_LENGTH) out.push(ALPHABET[byte % ALPHABET.length] ?? 'a')
    }
  }
  return TOPIC_PREFIX + out.join('')
}

export function randomTopicUrl(): string {
  return `${NTFY_PUBLIC_SERVER}/${randomTopic()}`
}

/** "https://ntfy.sh/cwnsb-abc" → { server: "ntfy.sh", topic: "cwnsb-abc" } (null if not a URL). */
export function splitTopicUrl(raw: string): { server: string; topic: string } | null {
  try {
    const url = new URL(raw)
    const topic = url.pathname.replace(/^\/+|\/+$/g, '')
    return topic ? { server: url.host, topic } : null
  } catch {
    return null
  }
}

export type UrlProblem = 'empty' | 'invalid' | 'tooLong' | 'credentials' | 'scheme' | 'httpPublic'

function isPrivateIPv4(host: string): boolean {
  const parts = host.split('.').map(Number)
  if (parts.length !== 4 || parts.some((n) => !Number.isInteger(n) || n < 0 || n > 255)) return false
  const [a = 0, b = 0] = parts
  return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254)
}

function isLocalHost(hostname: string): boolean {
  const host = hostname.replace(/^\[|\]$/g, '').toLowerCase()
  if (host === 'localhost' || host.endsWith('.local') || host.endsWith('.lan')) return true
  if (isPrivateIPv4(host)) return true
  // IPv6 loopback, unique-local (fc00::/7) and link-local (fe80::/10).
  return host === '::1' || /^f[cd][0-9a-f]{2}:/.test(host) || /^fe[89ab][0-9a-f]:/.test(host)
}

/** Mirrors the server: https everywhere, http only on the local network, no credentials. */
export function notificationUrlProblem(raw: string): UrlProblem | null {
  if (raw.trim() === '') return 'empty'
  if (raw.length > NOTIFICATION_URL_MAX) return 'tooLong'
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    return 'invalid'
  }
  if (!url.host) return 'invalid'
  if (url.username || url.password) return 'credentials'
  if (url.protocol === 'https:') return null
  if (url.protocol === 'http:') return isLocalHost(url.hostname) ? null : 'httpPublic'
  return 'scheme'
}
