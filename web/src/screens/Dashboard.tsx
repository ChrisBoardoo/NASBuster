// SPDX-License-Identifier: AGPL-3.0-or-later
// Dashboard: hostname as the poster headline, live metrics (SSE), pools, health, disks.
import { ArrowRight } from 'lucide-react'
import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'wouter'
import { getDisks, getPools, getSystemChecks, getSystemInfo } from '../api/endpoints'
import type { Alert, Disk, HealthStatus, MetricsSample } from '../api/types'
import { useApi } from '../api/useApi'
import { type StreamStatus, useMetricsStream } from '../api/useMetricsStream'
import { ButtonLink } from '../components/Button'
import { CheckList } from '../components/CheckList'
import { DiskList } from '../components/DiskList'
import { Icon } from '../components/Icon'
import { InlineLoading } from '../components/Loading'
import { Notice } from '../components/Notice'
import { PoolSummary } from '../components/PoolList'
import { StatTile, type TileSeries } from '../components/StatTile'
import { StatusLabel } from '../components/StatusLabel'
import { Container, Eyebrow, SectionTitle } from '../components/Typography'
import { cn } from '../lib/cn'
import { TEMP_CRIT_CELSIUS, TEMP_WARN_CELSIUS } from '../lib/constants'
import { EMPTY } from '../lib/format'
import { useDocumentTitle, useNow } from '../lib/hooks'
import { countByStatus, useErrorMessage } from '../lib/messages'
import { poolHealthStatus } from '../lib/pools'
import { useFormat } from '../lib/useFormat'
import { needsAttention, sortAlerts } from '../lib/alerts'
import { useAlertText } from '../lib/useAlertText'
import { useAlerts } from '../session/AlertsContext'
import { useReadySession } from '../session/SessionContext'

function temperatureStatus(celsius: number | null | undefined): HealthStatus | null {
  if (celsius === null || celsius === undefined || !Number.isFinite(celsius)) return null
  if (celsius >= TEMP_CRIT_CELSIUS) return 'crit'
  if (celsius >= TEMP_WARN_CELSIUS) return 'warn'
  return 'ok'
}

/** Auto domain for temperatures, at least 10 °C tall so noise doesn't look like drama. */
function temperatureDomain(values: ReadonlyArray<number | null>): [number, number] {
  const numeric = values.filter((v): v is number => v !== null && Number.isFinite(v))
  if (numeric.length === 0) return [30, 80]
  let lo = Math.floor(Math.min(...numeric) - 2)
  let hi = Math.ceil(Math.max(...numeric) + 2)
  if (hi - lo < 10) {
    const mid = (hi + lo) / 2
    lo = Math.floor(mid - 5)
    hi = Math.ceil(mid + 5)
  }
  return [lo, hi]
}

function StreamIndicator({ status }: { status: StreamStatus }) {
  const { t } = useTranslation()
  return (
    <span className="label-mono inline-flex items-center gap-2 text-muted-foreground" role="status">
      <span
        aria-hidden="true"
        className={cn('h-2 w-2', status === 'live' ? 'animate-blink bg-status-ok' : 'bg-status-warn')}
      />
      {t(`metrics.stream.${status}`)}
    </span>
  )
}

export default function Dashboard() {
  const { t } = useTranslation()
  const { ready } = useReadySession()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  useDocumentTitle(t('nav.dashboard'))

  const metrics = useMetricsStream()
  const checks = useApi((signal) => getSystemChecks({ signal }), [])
  const disks = useApi((signal) => getDisks({ signal }), [])
  const pools = useApi((signal) => getPools({ signal }), [])
  const info = useApi(
    (signal) =>
      getSystemInfo({ signal }).then((data) => ({ data, bootTs: Date.now() / 1000 - data.uptimeSeconds })),
    [],
  )
  const now = useNow(30_000)

  const headingRef = useRef<HTMLHeadingElement>(null)
  useEffect(() => {
    headingRef.current?.focus({ preventScroll: true })
  }, [])

  const series = useMemo(() => buildSeries(metrics.history), [metrics.history])
  const current = metrics.current

  // A pool that isn't healthy needs attention as much as a failing health check.
  const counts = checks.data ? countByStatus(checks.data.checks) : null
  if (counts && pools.data) {
    for (const pool of pools.data.pools) {
      const status = poolHealthStatus(pool.status?.health)
      if (status === 'warn' || status === 'crit') counts[status] += 1
    }
  }
  const attention = counts ? counts.warn + counts.crit : 0
  const worst: HealthStatus = counts ? (counts.crit > 0 ? 'crit' : counts.warn > 0 ? 'warn' : 'ok') : 'unknown'

  const cpuText = (v: number) => `${format.number(v)} ${t('units.percent')}`
  const tempStatus = temperatureStatus(current?.tempCelsius)
  const memTotal = current?.memTotalBytes ?? 0
  const memPercent = current && memTotal > 0 ? (current.memUsedBytes / memTotal) * 100 : null

  return (
    <Container>
      <CriticalBanner disks={disks.data?.disks} />
      <header className="flex flex-col gap-6 pt-12 pb-12 md:pt-20 md:pb-16">
        <Eyebrow>{t('dashboard.eyebrow')}</Eyebrow>
        <h1
          ref={headingRef}
          tabIndex={-1}
          className="text-[length:clamp(3.5rem,11vw,9rem)] leading-[0.9] font-semibold tracking-tighter break-words"
        >
          {ready.boot.hostname}
          <span className="text-accent">.</span>
        </h1>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2" aria-live="polite">
          {counts ? (
            <>
              <StatusLabel status={worst} />
              <p className="text-lg md:text-xl">
                {attention === 0 ? t('dashboard.summary.allGood') : t('dashboard.summary.attention', { count: attention })}
              </p>
            </>
          ) : (
            <p className="label-mono text-muted-foreground">
              {checks.error ? errorMessage(checks.error) : t('dashboard.summary.loading')}
            </p>
          )}
        </div>
      </header>

      <section aria-labelledby="dash-live" className="flex flex-col gap-8">
        <SectionTitle id="dash-live" title={t('dashboard.metricsTitle')} aside={<StreamIndicator status={metrics.status} />} />
        <div className="grid gap-x-8 gap-y-10 sm:grid-cols-2 xl:grid-cols-4">
          <StatTile
            label={t('metrics.cpu')}
            parts={[{ value: current ? format.number(current.cpuPercent) : EMPTY, unit: current ? t('units.percent') : '' }]}
            sub={current ? t('metrics.load', { load: format.number(current.load1, 2, 2) }) : null}
            series={{ ...series.cpu, format: cpuText }}
          />
          <StatTile
            label={t('metrics.memory')}
            parts={[current ? format.bytesParts(current.memUsedBytes) : { value: EMPTY, unit: '' }]}
            sub={
              current && memPercent !== null
                ? t('metrics.ofTotal', { total: format.bytes(memTotal), percent: format.number(memPercent) })
                : null
            }
            series={{ ...series.memory, domain: [0, memTotal || 1], format: (v) => format.bytes(v) }}
          />
          <StatTile
            label={t('metrics.temperature')}
            parts={[format.celsiusParts(current?.tempCelsius)]}
            status={tempStatus ? <StatusLabel status={tempStatus} /> : null}
            sub={current && current.tempCelsius === null ? t('metrics.noSensor') : null}
            series={
              current && current.tempCelsius === null
                ? undefined
                : { ...series.temperature, format: (v) => format.celsius(v) }
            }
          />
          <StatTile
            label={t('metrics.uptime')}
            parts={info.data ? format.durationParts(now / 1000 - info.data.bootTs) : [{ value: EMPTY, unit: '' }]}
            sub={info.data ? t('metrics.since', { date: format.dateTime(info.data.bootTs) }) : null}
          />
        </div>
      </section>

      <section aria-labelledby="dash-pools" className="mt-20 flex flex-col gap-8">
        <SectionTitle
          id="dash-pools"
          title={t('dashboard.poolsTitle')}
          aside={
            <ButtonLink href="/storage" variant="ghost" size="sm" className="-mr-3" iconEnd={<Icon icon={ArrowRight} />}>
              {t('common.seeAll')}
            </ButtonLink>
          }
        />
        <PoolSummary state={pools} disks={disks.data?.disks} />
      </section>

      <div className="mt-20 grid gap-16 lg:grid-cols-12 lg:gap-12">
        <section aria-labelledby="dash-health" className="flex flex-col gap-6 lg:col-span-7">
          <SectionTitle
            id="dash-health"
            title={t('dashboard.healthTitle')}
            aside={
              <ButtonLink href="/system" variant="ghost" size="sm" className="-mr-3" iconEnd={<Icon icon={ArrowRight} />}>
                {t('common.seeAll')}
              </ButtonLink>
            }
          />
          {checks.data ? (
            <CheckList checks={checks.data.checks} compact />
          ) : checks.error ? (
            <Notice status="warn">{errorMessage(checks.error)}</Notice>
          ) : (
            <InlineLoading />
          )}
        </section>

        <section aria-labelledby="dash-disks" className="flex flex-col gap-6 lg:col-span-5">
          <SectionTitle
            id="dash-disks"
            title={t('dashboard.disksTitle')}
            aside={
              <ButtonLink href="/storage" variant="ghost" size="sm" className="-mr-3" iconEnd={<Icon icon={ArrowRight} />}>
                {t('common.seeAll')}
              </ButtonLink>
            }
          />
          <DiskList state={disks} compact limit={4} />
        </section>
      </div>
    </Container>
  )
}

type BaseSeries = Omit<TileSeries, 'format'>

function buildSeries(history: readonly MetricsSample[]): {
  cpu: BaseSeries
  memory: Omit<BaseSeries, 'domain'>
  temperature: BaseSeries
} {
  const timestamps = history.map((p) => p.ts)
  const temps = history.map((p) => p.tempCelsius)
  return {
    cpu: { values: history.map((p) => p.cpuPercent), timestamps, domain: [0, 100] },
    memory: { values: history.map((p) => p.memUsedBytes), timestamps },
    temperature: { values: temps, timestamps, domain: temperatureDomain(temps) },
  }
}

/** Top banner while a critical alert is active and not yet marked as seen; links to Alerts. */
function CriticalBanner({ disks }: { disks: readonly Disk[] | undefined }) {
  const { t } = useTranslation()
  const { alerts } = useAlerts()
  const describe = useAlertText(disks)
  const critical: Alert[] = sortAlerts(alerts ?? []).filter((a) => needsAttention(a) && a.level === 'crit')
  const first = critical[0]
  if (!first) return null
  return (
    <Link
      href="/alerts"
      className="group mt-8 flex flex-wrap items-center gap-x-5 gap-y-2 border-l-2 border-status-crit bg-muted py-4 pr-4 pl-5 transition-colors duration-150 ease-crisp hover:bg-border md:mt-10"
    >
      <StatusLabel status="crit" />
      <span className="min-w-0 flex-1 text-lg font-semibold tracking-tight">
        {describe(first).title}
        {critical.length > 1 && (
          <span className="ml-3 font-normal text-muted-foreground">{t('alerts.banner.more', { count: critical.length - 1 })}</span>
        )}
      </span>
      <span className="label-mono inline-flex items-center gap-2 text-foreground">
        {t('alerts.banner.see')}
        <Icon icon={ArrowRight} />
      </span>
    </Link>
  )
}
