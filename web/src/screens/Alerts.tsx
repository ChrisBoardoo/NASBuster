// SPDX-License-Identifier: AGPL-3.0-or-later
// Alerts: what needs attention now (critical first), then what was resolved in the last
// 7 days, greyed out. "Mark as seen" stops the highlight; the alert stays until resolved.
import { BellOff, Check, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getDisks, getNotifications } from '../api/endpoints'
import type { Alert, Disk } from '../api/types'
import { useApi } from '../api/useApi'
import { Button, ButtonLink } from '../components/Button'
import { Icon } from '../components/Icon'
import { InlineLoading } from '../components/Loading'
import { Notice } from '../components/Notice'
import { StatusLabel } from '../components/StatusLabel'
import { Container, PageHeader, SectionTitle } from '../components/Typography'
import { ALERTS_RESOLVED_DAYS, alertStatus, isActive, needsAttention, sortAlerts } from '../lib/alerts'
import { cn } from '../lib/cn'
import { useDocumentTitle, useNow } from '../lib/hooks'
import { useErrorMessage } from '../lib/messages'
import { useAlertText } from '../lib/useAlertText'
import { useFormat } from '../lib/useFormat'
import { useAlerts } from '../session/AlertsContext'

export default function Alerts() {
  const { t } = useTranslation()
  useDocumentTitle(t('nav.alerts'))
  const errorMessage = useErrorMessage()
  const { alerts, error, refresh } = useAlerts()
  const disks = useApi((signal) => getDisks({ signal }), [])
  const channels = useApi((signal) => getNotifications({ signal }), [])

  const sorted = sortAlerts(alerts ?? [])
  const active = sorted.filter(isActive)
  const resolved = sorted.filter((alert) => !isActive(alert))
  const notified = channels.data ? channels.data.ntfy.enabled || channels.data.webhook.enabled : true

  return (
    <Container>
      <PageHeader
        eyebrow={t('alerts.eyebrow')}
        title={t('alerts.title')}
        intro={t('alerts.intro', { days: ALERTS_RESOLVED_DAYS })}
        aside={
          <Button variant="secondary" size="sm" icon={<Icon icon={RefreshCw} />} onClick={refresh}>
            {t('common.refresh')}
          </Button>
        }
      />

      {!notified && (
        <Notice
          status="info"
          className="mb-12 max-w-3xl"
          title={t('alerts.notNotified')}
          actions={
            <ButtonLink href="/settings#notifications" variant="ghost" size="sm" className="-ml-3">
              {t('alerts.setUp')}
            </ButtonLink>
          }
        >
          {t('alerts.notNotifiedBody')}
        </Notice>
      )}

      <section aria-labelledby="alerts-active" className="flex flex-col gap-6">
        <SectionTitle id="alerts-active" title={t('alerts.activeTitle')} />
        {!alerts ? (
          error ? (
            <Notice
              status="warn"
              title={t('alerts.loadFailed')}
              actions={
                <Button variant="ghost" size="sm" className="-ml-3" onClick={refresh}>
                  {t('common.retry')}
                </Button>
              }
            >
              {errorMessage(error)}
            </Notice>
          ) : (
            <InlineLoading />
          )
        ) : active.length === 0 ? (
          <div className="flex flex-col items-start gap-3 py-6">
            <StatusLabel status="ok" label={t('alerts.allClear')} />
            <p className="text-4xl font-semibold tracking-tight md:text-5xl">{t('alerts.noneTitle')}</p>
            <p className="max-w-xl text-lg text-muted-foreground">{t('alerts.noneBody')}</p>
          </div>
        ) : (
          <ul className="border-b border-border">
            {active.map((alert) => (
              <AlertRow key={alert.id} alert={alert} disks={disks.data?.disks} />
            ))}
          </ul>
        )}
      </section>

      {resolved.length > 0 && (
        <section aria-labelledby="alerts-resolved" className="mt-20 flex flex-col gap-6">
          <SectionTitle id="alerts-resolved" title={t('alerts.resolvedTitle', { days: ALERTS_RESOLVED_DAYS })} />
          <ul className="border-b border-border">
            {resolved.map((alert) => (
              <AlertRow key={`${alert.id}-${alert.resolvedAt}`} alert={alert} disks={disks.data?.disks} />
            ))}
          </ul>
        </section>
      )}
    </Container>
  )
}

function AlertRow({ alert, disks }: { alert: Alert; disks: readonly Disk[] | undefined }) {
  const { t } = useTranslation()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const now = useNow(30_000)
  const describe = useAlertText(disks)
  const { ack } = useAlerts()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)

  const { title, action } = describe(alert)
  const live = isActive(alert)
  const highlighted = needsAttention(alert)
  const status = alertStatus(alert.level)

  const markSeen = async () => {
    setBusy(true)
    setError(null)
    try {
      await ack(alert.id)
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(false)
    }
  }

  return (
    <li className="grid gap-x-12 gap-y-4 border-t border-border py-8 lg:grid-cols-12">
      <div className="flex flex-col gap-2 lg:col-span-3">
        {live ? (
          <StatusLabel status={status} label={t(`status.${status}`)} />
        ) : (
          <StatusLabel status="ok" label={t('alerts.resolved')} />
        )}
        <p className="label-mono text-muted-foreground tabular-nums">
          {live
            ? t('alerts.since', { time: format.relative(alert.firstSeen, now) })
            : t('alerts.resolvedAt', { time: format.relative(alert.resolvedAt, now) })}
        </p>
        {live && alert.ackedAt > 0 && (
          <p className="label-mono inline-flex items-center gap-1.5 text-muted-foreground">
            <Icon icon={BellOff} size={14} />
            {t('alerts.seen', { time: format.relative(alert.ackedAt, now) })}
          </p>
        )}
      </div>
      <div
        className={cn(
          'flex min-w-0 flex-col gap-3 lg:col-span-9',
          highlighted && (status === 'crit' ? 'border-l-2 border-status-crit pl-5' : 'border-l-2 border-status-warn pl-5'),
        )}
      >
        <h3
          className={cn(
            'text-2xl font-semibold tracking-tight break-words md:text-3xl',
            live ? 'text-foreground' : 'text-muted-foreground',
          )}
        >
          {title}
        </h3>
        {action && <p className={cn('max-w-2xl', live ? 'text-foreground' : 'text-muted-foreground')}>{action}</p>}
        <p className="font-mono text-xs break-all text-muted-foreground">
          {format.dateTime(alert.firstSeen)}
          {!live && ` → ${format.dateTime(alert.resolvedAt)}`} · {alert.id}
        </p>
        {highlighted && (
          <div>
            <Button variant="ghost" size="sm" className="-ml-3" icon={<Icon icon={Check} />} busy={busy} onClick={() => void markSeen()}>
              {t('alerts.markSeen')}
            </Button>
          </div>
        )}
        {error !== null && (
          <Notice status="crit" assertive>
            <span className="text-foreground">{errorMessage(error)}</span>
          </Notice>
        )}
      </div>
    </li>
  )
}
