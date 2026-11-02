// SPDX-License-Identifier: AGPL-3.0-or-later
import { type RefObject, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

/** Current time (ms), refreshed every `intervalMs`, for relative times and uptime. */
export function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}

/** Sets document.title to "<page> · NasBuster". */
export function useDocumentTitle(page: string | null): void {
  const { t } = useTranslation()
  const product = t('app.product')
  useEffect(() => {
    document.title = page ? `${page} · ${product}` : product
  }, [page, product])
}

/** Width of an element in CSS pixels, tracked with a ResizeObserver. */
export function useElementWidth(ref: RefObject<HTMLElement | null>): number {
  const [width, setWidth] = useState(0)
  useEffect(() => {
    const element = ref.current
    if (!element) return
    setWidth(element.getBoundingClientRect().width)
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0]
      if (entry) setWidth(entry.contentRect.width)
    })
    observer.observe(element)
    return () => observer.disconnect()
  }, [ref])
  return width
}

/** Seconds left before `until` (ms timestamp), ticking every second; 0 when elapsed. */
export function useCountdown(until: number | null): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (until === null) return
    setNow(Date.now())
    const id = setInterval(() => {
      const current = Date.now()
      setNow(current)
      if (current >= until) clearInterval(id)
    }, 1000)
    return () => clearInterval(id)
  }, [until])
  return until === null ? 0 : Math.max(0, Math.ceil((until - now) / 1000))
}

/** True while the page is visible (polling pauses in background tabs). */
export function usePageVisible(): boolean {
  const [visible, setVisible] = useState(() => typeof document === 'undefined' || document.visibilityState !== 'hidden')
  useEffect(() => {
    const onChange = () => setVisible(document.visibilityState !== 'hidden')
    document.addEventListener('visibilitychange', onChange)
    return () => document.removeEventListener('visibilitychange', onChange)
  }, [])
  return visible
}

/**
 * Deep links (/storage#pool-tank, /settings#notifications…): an SPA doesn't scroll to the hash
 * by itself, and sections load asynchronously, so the target is kept in view while the page
 * settles (2 s at most, and never against the user's own scrolling).
 */
export function useHashScroll(): void {
  useEffect(() => {
    const id = decodeURIComponent(window.location.hash.slice(1))
    if (!id) return
    const follow = () => document.getElementById(id)?.scrollIntoView({ block: 'start' })
    follow()
    const observer = new ResizeObserver(follow)
    const main = document.getElementById('main')
    if (main) observer.observe(main)
    const stop = () => observer.disconnect()
    const timer = window.setTimeout(stop, 2000)
    const events = ['wheel', 'touchmove', 'keydown', 'pointerdown'] as const
    for (const event of events) window.addEventListener(event, stop, { once: true, passive: true })
    return () => {
      stop()
      window.clearTimeout(timer)
      for (const event of events) window.removeEventListener(event, stop)
    }
  }, [])
}
