// SPDX-License-Identifier: AGPL-3.0-or-later
import { useCallback, useSyncExternalStore } from 'react'
import { readStorage, writeStorage } from './storage'

// Keep in sync with public/theme-init.js (applied before first paint).
export const THEMES = ['bold', 'paper'] as const
export type Theme = (typeof THEMES)[number]
export const DEFAULT_THEME: Theme = 'bold'
const STORAGE_KEY = 'cwnsb.theme'

function isTheme(value: unknown): value is Theme {
  return typeof value === 'string' && (THEMES as readonly string[]).includes(value)
}

export function currentTheme(): Theme {
  const attr = document.documentElement.getAttribute('data-theme')
  if (isTheme(attr)) return attr
  const saved = readStorage(STORAGE_KEY)
  return isTheme(saved) ? saved : DEFAULT_THEME
}

const listeners = new Set<() => void>()

export function applyTheme(theme: Theme): void {
  document.documentElement.setAttribute('data-theme', theme)
  writeStorage(STORAGE_KEY, theme)
  listeners.forEach((notify) => notify())
}

/** Ensures data-theme is set (theme-init.js normally did it already). */
export function initTheme(): void {
  document.documentElement.setAttribute('data-theme', currentTheme())
}

function subscribe(notify: () => void) {
  listeners.add(notify)
  return () => {
    listeners.delete(notify)
  }
}

export function useTheme(): [Theme, (theme: Theme) => void] {
  const theme = useSyncExternalStore(subscribe, currentTheme, () => DEFAULT_THEME)
  const set = useCallback((next: Theme) => applyTheme(next), [])
  return [theme, set]
}

export function nextTheme(theme: Theme): Theme {
  const index = THEMES.indexOf(theme)
  return THEMES[(index + 1) % THEMES.length] ?? DEFAULT_THEME
}
