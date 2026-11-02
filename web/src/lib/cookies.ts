// SPDX-License-Identifier: AGPL-3.0-or-later

/** Returns the decoded value of a cookie readable from JS, or null. */
export function readCookie(name: string): string | null {
  const prefix = `${name}=`
  for (const part of document.cookie.split(';')) {
    const item = part.trim()
    if (item.startsWith(prefix)) {
      const raw = item.slice(prefix.length)
      try {
        return decodeURIComponent(raw)
      } catch {
        return raw
      }
    }
  }
  return null
}
