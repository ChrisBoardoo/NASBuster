// SPDX-License-Identifier: AGPL-3.0-or-later
// Event journal (GET /journal, ADR-0031): what happened to the NAS, kept across reboots,
// newest first. Each entry links to its card in the lexicon of failures, below the list.
import { RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getJournal } from '../api/endpoints'
import type { HealthStatus, JournalEvent, JournalLevel } from '../api/types'
import { useApi } from '../api/useApi'
import { Button } from '../components/Button'
import { Icon } from '../components/Icon'
import { InlineLoading } from '../components/Loading'
import { Notice } from '../components/Notice'
import { StatusLabel } from '../components/StatusLabel'
import { Container, PageHeader } from '../components/Typography'
import { cn } from '../lib/cn'
import { useDocumentTitle } from '../lib/hooks'
import { useDynamicT, useErrorMessage } from '../lib/messages'
import { alertDiskLabel, useAlertText } from '../lib/useAlertText'
import { useFormat } from '../lib/useFormat'

const PAGE = 100

/** Filters: everything, warnings and worse, errors and worse. */
const FILTERS: readonly { key: 'all' | 'warn' | 'error'; level?: JournalLevel }[] = [
  { key: 'all' },
  { key: 'warn', level: 'warn' },
  { key: 'error', level: 'error' },
]

/** Every code the lexicon explains (`journal.codes.<code>`), most useful first. */
const LEXICON = [
  'system.unclean_shutdown',
  'system.boot',
  'pool.degraded',
  'pool.failed',
  'pool.reassemble',
  'pool.manual_recovery',
  'pool.rebuilding',
  'pool.verifying',
  'pool.missing_disk',
  'pool.unmounted',
  'pool.errors',
  'pool.space',
  'system.space',
  'power.under_voltage_now',
  'power.under_voltage_occurred',
  'temperature.hot',
  'throttling.active',
  'smart.failing',
  'smart.attributes',
  'smart.crc',
] as const

const anchor = (code: string) => `lexicon-${code.replace(/[^a-z0-9]+/gi, '-')}`

const LEVEL_STATUS: Record<string, HealthStatus> = { info: 'info', warn: 'warn', error: 'crit', crit: 'crit' }

export default function Journal() {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  useDocumentTitle(t('nav.journal'))
  const [level, setLevel] = useState<JournalLevel | undefined>(undefined)
  const first = useApi((signal) => getJournal({ level, limit: PAGE }, { signal }), [level])
  // Older pages, appended below the first one; reset whenever the first page changes.
  const [older, setOlder] = useState<{ events: JournalEvent[]; next: number } | null>(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const [moreError, setMoreError] = useState<unknown>(null)
  useEffect(() => {
    setOlder(null)
    setMoreError(null)
  }, [first.data])

  const events = first.data ? [...first.data.events, ...(older?.events ?? [])] : []
  const next = older ? older.next : (first.data?.next ?? 0)

  const loadMore = async () => {
    setLoadingMore(true)
    setMoreError(null)
    try {
      const page = await getJournal({ level, limit: PAGE, before: next })
      setOlder((prev) => ({ events: [...(prev?.events ?? []), ...page.events], next: page.next }))
    } catch (err) {
      setMoreError(err)
    } finally {
      setLoadingMore(false)
    }
  }

  return (
    <Container>
      <PageHeader
        eyebrow={t('journal.eyebrow')}
        title={t('journal.title')}
        intro={t('journal.intro')}
        aside={
          <Button variant="secondary" size="sm" icon={<Icon icon={RefreshCw} />} onClick={first.reload} busy={first.loading}>
            {t('common.refresh')}
          </Button>
        }
      />

      <div role="group" aria-label={t('journal.filter.label')} className="mb-6 flex flex-wrap items-center gap-x-4">
        {FILTERS.map((f) => {
          const active = f.level === level
          return (
            <button
              key={f.key}
              type="button"
              aria-pressed={active}
              onClick={() => setLevel(f.level)}
              className={cn(
                'label-mono inline-flex min-h-11 items-center transition-colors duration-150 ease-crisp',
                active ? 'text-foreground underline decoration-accent decoration-2 underline-offset-8' : 'text-muted-foreground hover:text-foreground',
              )}
            >
              {t(`journal.filter.${f.key}`)}
            </button>
          )
        })}
      </div>

      {first.data ? (
        events.length === 0 ? (
          <div className="border-t border-border py-10">
            <p className="text-3xl font-semibold tracking-tight">{t('journal.empty')}</p>
            <p className="mt-3 max-w-prose text-muted-foreground">{t('journal.emptyBody')}</p>
          </div>
        ) : (
          <ol className="border-b border-border">
            {events.map((event) => (
              <JournalEntry key={event.id} event={event} />
            ))}
          </ol>
        )
      ) : first.error ? (
        <Notice
          status="warn"
          title={t('journal.loadFailed')}
          actions={
            <Button variant="ghost" size="sm" className="-ml-3" onClick={first.reload}>
              {t('common.retry')}
            </Button>
          }
        >
          {errorMessage(first.error)}
        </Notice>
      ) : (
        <InlineLoading />
      )}

      {next > 0 && (
        <div className="mt-6">
          <Button variant="secondary" size="sm" onClick={() => void loadMore()} busy={loadingMore}>
            {t('journal.more')}
          </Button>
          {moreError !== null && <p className="mt-3 text-sm text-status-crit">{errorMessage(moreError)}</p>}
        </div>
      )}

      <Lexicon />
    </Container>
  )
}

function JournalEntry({ event }: { event: JournalEvent }) {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()
  const alertText = useAlertText()

  let title: string
  if (event.source === 'alert' || has(`alerts.codes.${event.code}.title`)) {
    const text = alertText({
      id: '',
      level: event.level,
      code: event.code,
      params: event.params,
      firstSeen: 0,
      lastSeen: 0,
      resolvedAt: 0,
      ackedAt: 0,
    })
    title = event.phase === 'resolved' ? t('journal.resolved', { title: text.title }) : text.title
  } else if (has(`journal.titles.${event.code}`)) {
    title = td(`journal.titles.${event.code}`)
  } else {
    title = t('journal.unknown', { code: event.code })
  }

  const resolved = event.phase === 'resolved'
  const status: HealthStatus = resolved ? 'ok' : (LEVEL_STATUS[event.level] ?? 'unknown')
  const levelLabel = resolved ? t('journal.level.resolved') : has(`journal.level.${event.level}`) ? td(`journal.level.${event.level}`) : event.level

  const colon = event.subject.indexOf(':')
  const kind = colon < 0 ? event.subject : event.subject.slice(0, colon)
  const name = colon < 0 ? '' : event.subject.slice(colon + 1)
  const subject =
    kind === 'pool'
      ? t('journal.subject.pool', { name })
      : kind === 'disk'
        ? t('journal.subject.disk', { name: alertDiskLabel(name, undefined, t('common.unknown')) })
        : t('journal.subject.system')

  const lastAlive = event.code === 'system.unclean_shutdown' && typeof event.params.lastAlive === 'number' ? event.params.lastAlive : null

  return (
    <li className="grid gap-x-6 gap-y-1 border-t border-border py-5 md:grid-cols-[12rem_1fr]">
      <div className="tabular-nums">
        <time dateTime={new Date(event.time * 1000).toISOString()} className="block font-semibold">
          {format.dateTime(event.time)}
        </time>
        {event.timeState === 'uncertain' && (
          <span className="label-mono block text-status-warn" title={t('journal.time.uncertainHelp')}>
            {t('journal.time.uncertain')}
          </span>
        )}
        {event.timeState === 'corrected' && (
          <span className="label-mono block text-muted-foreground" title={t('journal.time.correctedHelp')}>
            {t('journal.time.corrected')}
          </span>
        )}
      </div>
      <div className="min-w-0">
        <StatusLabel status={status} label={levelLabel} />
        <p className="mt-1 text-lg font-semibold tracking-tight">{title}</p>
        {lastAlive !== null && <p className="text-muted-foreground">{t('journal.lastAlive', { time: format.dateTime(lastAlive) })}</p>}
        <p className="mt-1 flex flex-wrap gap-x-4 text-sm text-muted-foreground">
          <span className="break-all">{subject}</span>
          {has(`journal.codes.${event.code}.name`) && (
            <a href={`#${anchor(event.code)}`} className="underline decoration-accent underline-offset-4 hover:text-foreground">
              {t('journal.understand')}
            </a>
          )}
        </p>
      </div>
    </li>
  )
}

function Lexicon() {
  const { t } = useTranslation()
  const { td } = useDynamicT()
  return (
    <section aria-labelledby="journal-lexicon" className="mt-16 border-t-2 border-foreground pt-8">
      <h2 id="journal-lexicon" className="text-3xl font-semibold tracking-tight">
        {t('journal.lexicon.title')}
      </h2>
      <p className="mt-2 max-w-prose text-muted-foreground">{t('journal.lexicon.intro')}</p>
      <dl className="mt-6">
        {LEXICON.map((code) => (
          <div key={code} id={anchor(code)} className="scroll-mt-24 border-t border-border py-5 target:bg-muted">
            <dt className="text-lg font-semibold tracking-tight">
              {td(`journal.codes.${code}.name`)}
              <span className="ml-3 font-mono text-xs font-normal text-muted-foreground">{code}</span>
            </dt>
            <dd className="mt-2 grid gap-2 md:grid-cols-2 md:gap-6">
              <p>
                <span className="label-mono block text-muted-foreground">{t('journal.lexicon.what')}</span>
                {td(`journal.codes.${code}.what`)}
              </p>
              <p>
                <span className="label-mono block text-muted-foreground">{t('journal.lexicon.todo')}</span>
                {td(`journal.codes.${code}.todo`)}
              </p>
            </dd>
          </div>
        ))}
      </dl>
    </section>
  )
}
