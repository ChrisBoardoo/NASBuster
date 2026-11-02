// SPDX-License-Identifier: AGPL-3.0-or-later

// localStorage can throw (privacy mode, disabled storage, quota): never let it break the UI.

export function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

export function writeStorage(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value)
  } catch {
    /* storage unavailable: the preference simply won't persist */
  }
}
