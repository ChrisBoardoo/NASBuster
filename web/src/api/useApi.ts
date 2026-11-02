// SPDX-License-Identifier: AGPL-3.0-or-later
import { useCallback, useEffect, useRef, useState } from 'react'
import { isAbortError } from './client'

export interface ApiState<T> {
  data: T | undefined
  error: unknown
  loading: boolean
  /** Re-runs the request, keeping the previous data visible meanwhile. */
  reload: () => void
}

/**
 * Runs `fetcher` on mount and whenever `deps` change; aborts in-flight requests on
 * unmount or re-run. Deliberately tiny: no cache, no global store.
 */
export function useApi<T>(fetcher: (signal: AbortSignal) => Promise<T>, deps: readonly unknown[] = []): ApiState<T> {
  const fetcherRef = useRef(fetcher)
  useEffect(() => {
    fetcherRef.current = fetcher
  })

  const [version, setVersion] = useState(0)
  const [state, setState] = useState<{ data: T | undefined; error: unknown; loading: boolean }>({
    data: undefined,
    error: undefined,
    loading: true,
  })

  useEffect(() => {
    const controller = new AbortController()
    setState((prev) => ({ data: prev.data, error: undefined, loading: true }))
    fetcherRef.current(controller.signal).then(
      (data) => {
        if (!controller.signal.aborted) setState({ data, error: undefined, loading: false })
      },
      (error: unknown) => {
        if (controller.signal.aborted || isAbortError(error)) return
        setState((prev) => ({ data: prev.data, error, loading: false }))
      },
    )
    return () => controller.abort()
    // The fetcher is read through a ref; re-running is driven by the caller-supplied deps.
  }, [version, ...deps])

  const reload = useCallback(() => setVersion((v) => v + 1), [])
  return { ...state, reload }
}
