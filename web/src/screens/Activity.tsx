// SPDX-License-Identifier: AGPL-3.0-or-later
// Audit log (GET /audit?limit=100), newest first, with relative times.
import { Clock, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { getAudit } from '../api/endpoints'
import type { AuditEntry, HealthStatus } from '../api/types'
import { useApi } from '../api/useApi'
import { Button } from '../components/Button'
import { Icon } from '../components/Icon'
import { InlineLoading } from '../components/Loading'
import { Notice } from '../components/Notice'
import { StatusLabel } from '../components/StatusLabel'
import { Container, PageHeader } from '../components/Typography'
import { AUDIT_LIMIT } from '../lib/constants'
import { EMPTY } from '../lib/format'
import { useDocumentTitle, useNow } from '../lib/hooks'
import { useDynamicT, useErrorMessage } from '../lib/messages'
import { useFormat } from '../lib/useFormat'

/** Actor of the scheduled tasks (snapshots, scrubs), ADR-0010. */
const SCHEDULER = 'scheduler'
const OK_RESULTS = new Set(['ok', 'success'])
const FAILED_RESULTS = new Set(['error', 'failed', 'fail', 'denied', 'forbidden'])

export default function Activity() {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  useDocumentTitle(t('nav.activity'))
  const audit = useApi((signal) => getAudit(AUDIT_LIMIT, { signal }), [])

  return (
    <Container>
      <PageHeader
        eyebrow={t('activity.eyebrow')}
        title={t('activity.title')}
        intro={t('activity.intro', { count: AUDIT_LIMIT })}
        aside={
          <Button variant="secondary" size="sm" icon={<Icon icon={RefreshCw} />} onClick={audit.reload} busy={audit.loading}>
            {t('common.refresh')}
          </Button>
        }
      />
      {audit.data ? (
        audit.data.entries.length === 0 ? (
          <p className="border-t border-border py-10 text-3xl font-semibold tracking-tight">{t('activity.empty')}</p>
        ) : (
          <AuditTable entries={audit.data.entries} />
        )
      ) : audit.error ? (
        <Notice
          status="warn"
          title={t('activity.loadFailed')}
          actions={
            <Button variant="ghost" size="sm" className="-ml-3" onClick={audit.reload}>
              {t('common.retry')}
            </Button>
          }
        >
          {errorMessage(audit.error)}
        </Notice>
      ) : (
        <InlineLoading />
      )}
    </Container>
  )
}

function AuditTable({ entries }: { entries: AuditEntry[] }) {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()
  const now = useNow(30_000)

  // Known details read as words: first per action ("app.remove" + "data kept"), then by their
  // first word ("forgotten (disks gone)"); anything else (ids, pool names, "old -> new") as is.
  const detail = (action: string, value: string): string => {
    const counts = action === 'config.restore' ? /^(\d+) users?, (\d+) conflicts?$/.exec(value) : null
    if (counts) return t('activity.detail.restoreCounts', { users: Number(counts[1]), conflicts: Number(counts[2]) })
    const days = action === 'share.recycle' ? /^(\d+) days?$/.exec(value) : null
    if (days) return t('activity.detail.recycleDays', { count: Number(days[1]) })
    const scoped = `activity.detail.${action}.${value.trim().replace(/\s+/g, '_')}`
    if (has(scoped)) return td(scoped)
    const key = `activity.detail.${value.split(' ')[0]}`
    return has(key) ? td(key) : value
  }

  const result = (value: string): { status: HealthStatus; label: string } => {
    const normalized = value.toLowerCase()
    if (OK_RESULTS.has(normalized)) return { status: 'ok', label: t('activity.result.ok') }
    if (FAILED_RESULTS.has(normalized)) return { status: 'crit', label: t('activity.result.failed') }
    // Alerts record their level as the result.
    if (normalized === 'warn' || normalized === 'crit') return { status: normalized, label: t(`status.${normalized}`) }
    return { status: value ? 'info' : 'unknown', label: value || EMPTY }
  }

  return (
    <div className="-mx-6 overflow-x-auto px-6 md:mx-0 md:px-0">
      <table className="w-full min-w-[52rem] border-b border-border text-left">
        <thead>
          <tr className="label-mono text-muted-foreground">
            <th scope="col" className="py-3 pr-6 font-medium">{t('activity.columns.when')}</th>
            <th scope="col" className="py-3 pr-6 font-medium">{t('activity.columns.actor')}</th>
            <th scope="col" className="py-3 pr-6 font-medium">{t('activity.columns.action')}</th>
            <th scope="col" className="py-3 pr-6 font-medium">{t('activity.columns.target')}</th>
            <th scope="col" className="py-3 pr-6 font-medium">{t('activity.columns.result')}</th>
            <th scope="col" className="py-3 font-medium">{t('activity.columns.from')}</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((entry) => {
            // The scheduler's own wording when there is one ("Nightly configuration backup").
            const schedulerKey = `activity.actions.${entry.action}_scheduler`
            const actionKey = entry.actor === SCHEDULER && has(schedulerKey) ? schedulerKey : `activity.actions.${entry.action}`
            const outcome = result(entry.result)
            return (
              <tr key={entry.id} className="border-t border-border align-baseline">
                <td className="py-4 pr-6 whitespace-nowrap tabular-nums">
                  <time dateTime={new Date(entry.ts * 1000).toISOString()} title={format.dateTime(entry.ts)}>
                    {format.relative(entry.ts, now)}
                  </time>
                </td>
                <td className="py-4 pr-6 font-semibold">
                  {entry.actor === SCHEDULER ? (
                    <span className="inline-flex items-center gap-2">
                      <Icon icon={Clock} size={14} className="shrink-0 text-muted-foreground" />
                      {t('activity.scheduler')}
                    </span>
                  ) : (
                    entry.actor || t('activity.system')
                  )}
                </td>
                <td className="py-4 pr-6">
                  {has(actionKey) && <span className="block">{td(actionKey)}</span>}
                  <span className="block font-mono text-xs text-muted-foreground">{entry.action}</span>
                </td>
                <td className="py-4 pr-6 font-mono text-sm break-all text-muted-foreground">
                  {entry.target || EMPTY}
                  {entry.detail && <span className="block font-sans text-xs">{detail(entry.action, entry.detail)}</span>}
                </td>
                <td className="py-4 pr-6">
                  <StatusLabel status={outcome.status} label={outcome.label} />
                </td>
                <td className="py-4 font-mono text-sm text-muted-foreground">{entry.remoteAddr || EMPTY}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
