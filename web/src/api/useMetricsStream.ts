// SPDX-License-Identifier: AGPL-3.0-or-later
import { useEffect, useState } from 'react'
import { METRICS_MAX_POINTS } from '../lib/constants'
import { EVENTS_URL, getMe, getMetrics } from './endpoints'
import type { MetricsSample } from './types'

export type StreamStatus = 'connecting' | 'live' | 'reconnecting'

export interface MetricsStream {
  current: MetricsSample | null
  /** Oldest first, deduplicated by timestamp, capped to the 15-minute window. */
  history: MetricsSample[]
  status: StreamStatus
}

function isSample(value: unknown): value is MetricsSample {
  if (typeof value !== 'object' || value === null) return false
  const v = value as Record<string, unknown>
  return typeof v.ts === 'number' && typeof v.cpuPercent === 'number' && typeof v.memUsedBytes === 'number'
}

function merge(history: MetricsSample[], incoming: MetricsSample[]): MetricsSample[] {
  const byTs = new Map<number, MetricsSample>()
  for (const sample of history) byTs.set(sample.ts, sample)
  for (const sample of incoming) byTs.set(sample.ts, sample)
  const sorted = [...byTs.values()].sort((a, b) => a.ts - b.ts)
  return sorted.length > METRICS_MAX_POINTS ? sorted.slice(sorted.length - METRICS_MAX_POINTS) : sorted
}

const MAX_BACKOFF_MS = 30_000

/**
 * Live system metrics: GET /system/metrics for the 15-minute history, then the SSE
 * stream (`event: metrics`, every 2 s). EventSource retries transient drops by itself;
 * when it gives up (non-200 answer, server restart), we reconnect with backoff and
 * refetch the history to fill the gap. The stream is closed while the tab is hidden,
 * so an idle browser tab costs the Pi nothing.
 */
export function useMetricsStream(): MetricsStream {
  const [history, setHistory] = useState<MetricsSample[]>([])
  const [status, setStatus] = useState<StreamStatus>('connecting')

  useEffect(() => {
    let source: EventSource | null = null
    let retryTimer: ReturnType<typeof setTimeout> | undefined
    let attempts = 0
    let disposed = false
    let historyController: AbortController | null = null

    const loadHistory = () => {
      historyController?.abort()
      const controller = new AbortController()
      historyController = controller
      getMetrics({ signal: controller.signal }).then(
        (res) => {
          if (disposed || controller.signal.aborted) return
          setHistory((prev) => merge(prev, [...res.history, res.current].filter(isSample)))
        },
        () => {
          /* the stream status already tells the user; keep what we have */
        },
      )
    }

    const close = () => {
      if (retryTimer !== undefined) clearTimeout(retryTimer)
      retryTimer = undefined
      source?.close()
      source = null
    }

    const scheduleReconnect = () => {
      close()
      attempts += 1
      setStatus('reconnecting')
      // A closed stream may mean an expired session: /auth/me triggers the global 401 handler.
      getMe().catch(() => undefined)
      const delay = Math.min(MAX_BACKOFF_MS, 1000 * 2 ** Math.min(attempts, 5))
      retryTimer = setTimeout(open, delay)
    }

    function open() {
      if (disposed || document.visibilityState === 'hidden') return
      close()
      const es = new EventSource(EVENTS_URL)
      source = es
      setStatus(attempts > 0 ? 'reconnecting' : 'connecting')

      es.onopen = () => {
        if (attempts > 0) loadHistory() // fill the gap left by the outage
        attempts = 0
        setStatus('live')
      }
      es.addEventListener('metrics', (event: MessageEvent<string>) => {
        let sample: unknown
        try {
          sample = JSON.parse(event.data)
        } catch {
          return
        }
        if (isSample(sample)) {
          const next = sample
          setHistory((prev) => merge(prev, [next]))
          setStatus('live')
        }
      })
      es.onerror = () => {
        if (es.readyState === EventSource.CLOSED) {
          scheduleReconnect()
        } else {
          setStatus('reconnecting') // EventSource is retrying on its own
        }
      }
    }

    const onVisibility = () => {
      if (document.visibilityState === 'hidden') {
        close()
      } else if (!source) {
        loadHistory()
        open()
      }
    }

    loadHistory()
    open()
    document.addEventListener('visibilitychange', onVisibility)

    return () => {
      disposed = true
      document.removeEventListener('visibilitychange', onVisibility)
      historyController?.abort()
      close()
    }
  }, [])

  return {
    current: history.length > 0 ? (history[history.length - 1] ?? null) : null,
    history,
    status,
  }
}
