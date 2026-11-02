// SPDX-License-Identifier: AGPL-3.0-or-later
// Polls an endpoint while enabled and while the page is visible. One request at a time
// (the next one is scheduled when the previous one settles); the last data is kept.
import { useCallback, useEffect, useRef, useState } from 'react'
import { usePageVisible } from '../lib/hooks'
import { isAbortError } from './client'

export interface PollState<T> {
  data: T | undefined
  error: unknown
  /** Polls right now instead of waiting for the next tick. */
  refresh: () => void
}

export function usePolling<T>(
  fetcher: (signal: AbortSignal) => Promise<T>,
  intervalMs: number,
  enabled = true,
): PollState<T> {
  const fetcherRef = useRef(fetcher)
  useEffect(() => {
    fetcherRef.current = fetcher
  })
  const visible = usePageVisible()
  const [epoch, setEpoch] = useState(0)
  const [state, setState] = useState<{ data: T | undefined; error: unknown }>({ data: undefined, error: undefined })

  useEffect(() => {
    if (!enabled || !visible) return
    const controller = new AbortController()
    let timer = 0
    const tick = async () => {
      try {
        const data = await fetcherRef.current(controller.signal)
        if (!controller.signal.aborted) setState({ data, error: undefined })
      } catch (error) {
        if (controller.signal.aborted || isAbortError(error)) return
        setState((prev) => ({ data: prev.data, error }))
      }
      if (!controller.signal.aborted) timer = window.setTimeout(() => void tick(), intervalMs)
    }
    void tick()
    return () => {
      controller.abort()
      window.clearTimeout(timer)
    }
  }, [enabled, visible, intervalMs, epoch])

  const refresh = useCallback(() => setEpoch((n) => n + 1), [])
  return { ...state, refresh }
}
