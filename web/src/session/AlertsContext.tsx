// SPDX-License-Identifier: AGPL-3.0-or-later
// Alerts shared by the bell (app shell), the Alerts page and the dashboard banner.
// Polled every 60 s while the tab is visible, and right after an acknowledgement.
import { type ReactNode, createContext, useCallback, useContext, useMemo } from 'react'
import { ackAlert, getAlerts } from '../api/endpoints'
import type { Alert } from '../api/types'
import { usePolling } from '../api/usePolling'
import { ALERTS_POLL_MS, ALERTS_RESOLVED_DAYS, type AlertCounts, countAlerts } from '../lib/alerts'

interface AlertsApi {
  alerts: Alert[] | undefined
  error: unknown
  counts: AlertCounts
  refresh: () => void
  /** POST /alerts/{id}/ack, then refresh. Throws on failure. */
  ack: (id: string) => Promise<void>
}

const AlertsContext = createContext<AlertsApi | null>(null)

export function AlertsProvider({ children }: { children: ReactNode }) {
  const poll = usePolling((signal) => getAlerts(ALERTS_RESOLVED_DAYS, { signal }), ALERTS_POLL_MS)
  const { refresh } = poll
  const alerts = poll.data?.alerts

  const ack = useCallback(
    async (id: string) => {
      await ackAlert(id)
      refresh()
    },
    [refresh],
  )

  const api = useMemo<AlertsApi>(
    () => ({ alerts, error: poll.error, counts: countAlerts(alerts ?? []), refresh, ack }),
    [alerts, poll.error, refresh, ack],
  )
  return <AlertsContext.Provider value={api}>{children}</AlertsContext.Provider>
}

export function useAlerts(): AlertsApi {
  const context = useContext(AlertsContext)
  if (!context) throw new Error('useAlerts must be used inside <AlertsProvider>')
  return context
}
