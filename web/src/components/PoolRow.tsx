// SPDX-License-Identifier: AGPL-3.0-or-later
// One pool = one full-width row: poster name, layout and health on the left; what to do about
// a failure (ADR-0012), free space, usage meter, members with their state (linked to their disk
// rows), md array and scrub on the right; a danger zone to destroy it at the bottom.
// While a scrub, a rebuild, a balance or an md sync runs, the row polls its own pool every 3 s
// while the tab is visible (5 min at most per run of polling).
import { Play, ScanSearch, Square } from 'lucide-react'
import { type MouseEvent, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { cancelScrub, getPool, resumeScrub, startScrub } from '../api/endpoints'
import type { Disk, HealthStatus, Pool, PoolDestroyResult } from '../api/types'
import { cn } from '../lib/cn'
import { POOL_POLL_MAX_MS, POOL_POLL_MS } from '../lib/constants'
import { usePageVisible } from '../lib/hooks'
import { useErrorMessage } from '../lib/messages'
import { useReveal } from '../lib/motion'
import {
  diskAnchorId,
  scrubDuration,
  scrubStartedAt,
  poolCapacity,
  poolHealthStatus,
  scrubStatus,
  usageStatus,
} from '../lib/pools'
import {
  type MemberState,
  type MemberView,
  type ProgressSample,
  backgroundWork,
  estimateSecondsLeft,
  isRebuilding,
  memberViews,
  repairKind,
} from '../lib/repair'
import { useFormat } from '../lib/useFormat'
import { usePoolText } from '../lib/usePoolText'
import { Button } from './Button'
import { DestroyPool } from './DestroyPool'
import { diskTitle } from './DiskRow'
import { Icon } from './Icon'
import { Meter } from './Meter'
import { Notice } from './Notice'
import { PoolRepair } from './PoolRepair'
import { StatusLabel } from './StatusLabel'
import { PosterNumber } from './Typography'

interface PoolRowProps {
  pool: Pool
  /** Known disks, to name the members (undefined while loading). */
  disks: readonly Disk[] | undefined
  /** Something changed beyond this row (mounted, member replaced…): reload pools and disks. */
  onChanged?: () => void
  /** The pool was destroyed or forgotten. */
  onDestroyed?: (name: string, result: PoolDestroyResult) => void
}

export function PoolRow({ pool: listed, disks, onChanged, onDestroyed }: PoolRowProps) {
  const { t } = useTranslation()
  const text = usePoolText()
  const format = useFormat()
  const visible = usePageVisible()

  // Fresher copy fetched by this row (polling, actions); a reloaded list replaces it.
  const [live, setLive] = useState<{ from: Pool; pool: Pool } | null>(null)
  const pool = live && live.from === listed ? live.pool : listed
  const status = pool.status
  const health = poolHealthStatus(status?.health)
  const members = useMemo(() => memberViews(pool, disks), [pool, disks])
  const kind = repairKind(status, members)
  // A parity pool past what it tolerates serves no data: not merely "degraded" (ADR-0032).
  const outOfService = kind === 'reassemble' || kind === 'outOfService'
  const healthHint = status && !kind ? (status.health === 'ok' ? null : text.healthHint(status.health)) : null

  // ---- Polling while something runs in the background, or is owed and about to start
  // (after a reassembly the parity repair, then the scrub, start a moment later).
  const watching = Boolean(
    status &&
      (status.scrub.status === 'running' ||
        status.md?.sync ||
        isRebuilding(status) ||
        status.balance ||
        status.repairNeeded ||
        status.resyncNeeded),
  )
  const [pollEpoch, setPollEpoch] = useState(0)
  const [timedOut, setTimedOut] = useState(false)
  const name = listed.name

  useEffect(() => {
    if (!watching || !visible) return
    const controller = new AbortController()
    const deadline = Date.now() + POOL_POLL_MAX_MS
    let timer = 0
    setTimedOut(false)
    const tick = async () => {
      if (Date.now() >= deadline) {
        setTimedOut(true)
        return
      }
      try {
        const fresh = await getPool(name, { signal: controller.signal })
        if (!controller.signal.aborted) setLive({ from: listed, pool: fresh })
      } catch {
        if (controller.signal.aborted) return
      }
      if (!controller.signal.aborted) timer = window.setTimeout(() => void tick(), POOL_POLL_MS)
    }
    timer = window.setTimeout(() => void tick(), POOL_POLL_MS)
    return () => {
      controller.abort()
      window.clearTimeout(timer)
    }
  }, [watching, visible, name, listed, pollEpoch])

  // An action just started background work: read the pool now, then keep polling.
  const refresh = useCallback(() => {
    void getPool(name).then(
      (fresh) => setLive({ from: listed, pool: fresh }),
      () => undefined,
    )
    setPollEpoch((n) => n + 1)
  }, [name, listed])

  // Observed progress, for an honest "time left" (reset when the work changes or goes back).
  const samples = useRef<{ kind: string; list: ProgressSample[] }>({ kind: '', list: [] })
  const work = status ? backgroundWork(status) : null
  if (work) {
    const current = samples.current
    const last = current.list[current.list.length - 1]
    if (current.kind !== work.kind || (last && work.percent < last.percent)) {
      samples.current = { kind: work.kind, list: [{ at: Date.now(), percent: work.percent }] }
    } else if (!last || last.percent !== work.percent) {
      current.list.push({ at: Date.now(), percent: work.percent })
    }
  } else if (samples.current.list.length) {
    samples.current = { kind: '', list: [] }
  }
  const secondsLeft = work ? estimateSecondsLeft(samples.current.list) : null

  const capacity = poolCapacity(status)
  const layoutTech = text.layoutTech(pool.layout)

  return (
    <li id={`pool-${pool.name}`} className="grid scroll-mt-6 gap-x-12 gap-y-8 border-t border-border py-10 md:py-12 lg:grid-cols-12">
      <div className="flex min-w-0 flex-col gap-4 lg:col-span-4">
        <h3 className="text-6xl leading-[0.9] font-semibold tracking-tighter break-all lg:text-7xl">{pool.name}</h3>
        <p className="label-mono flex flex-wrap gap-x-3 gap-y-1 text-muted-foreground">
          <span className="text-foreground">{text.layoutTitle(pool.layout)}</span>
          {layoutTech && (
            <>
              <span aria-hidden="true">·</span>
              <span>{layoutTech}</span>
            </>
          )}
        </p>
        <StatusLabel
          status={outOfService ? 'crit' : health}
          label={outOfService ? t('pools.health.outOfService') : text.healthLabel(status?.health)}
        />
        {status?.mountedDegraded && (
          <StatusLabel status="warn" label={t('repair.mountedDegradedLabel')} />
        )}
        {healthHint && (
          <p
            className={cn(
              'max-w-sm text-sm',
              health === 'crit' ? 'border-l-2 border-status-crit pl-3 text-foreground' : 'text-muted-foreground',
            )}
          >
            {healthHint}
          </p>
        )}
      </div>

      <div className="flex min-w-0 flex-col gap-10 lg:col-span-8">
        <PoolRepair
          pool={pool}
          kind={kind}
          members={members}
          secondsLeft={secondsLeft}
          onPool={(fresh) => {
            setLive({ from: listed, pool: fresh })
            onChanged?.()
          }}
          onRefresh={refresh}
        />
        {capacity ? (
          <CapacityBlock name={pool.name} {...capacity} />
        ) : !status ? (
          <Notice status="warn" title={t('pools.statusUnavailable')}>
            {text.statusError(pool.statusError || 'helper.unavailable')}
          </Notice>
        ) : null}

        <dl className="grid grid-cols-1 gap-x-8 gap-y-6 sm:grid-cols-[minmax(8rem,auto)_1fr]">
          <Detail label={t('pools.mountPoint')}>
            <span className="font-mono break-all">{status?.mountPoint || '—'}</span>
          </Detail>
          <Detail label={t('pools.members')}>
            <Members members={members} known={Boolean(disks)} />
          </Detail>
          {status?.md && (
            <Detail label={t('pools.array')}>
              <ArrayInfo md={status.md} />
            </Detail>
          )}
          {status && (
            <Detail label={t('pools.scrub')}>
              <ScrubInfo
                pool={pool}
                timedOut={timedOut}
                onStarted={(fresh) => {
                  if (fresh) setLive({ from: listed, pool: fresh })
                  setPollEpoch((n) => n + 1)
                }}
              />
            </Detail>
          )}
          <Detail label={t('pools.created')}>
            <span className="tabular-nums">{format.dateTime(pool.createdAt)}</span>
          </Detail>
        </dl>

        {onDestroyed && <DestroyPool pool={pool} onDestroyed={(result) => onDestroyed(pool.name, result)} />}
      </div>
    </li>
  )
}

function Detail({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="label-mono pt-1 text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  )
}

function CapacityBlock({ name, total, used, free, ratio }: { name: string; total: number; used: number; free: number; ratio: number }) {
  const { t } = useTranslation()
  const format = useFormat()
  const freeParts = format.bytesParts(free)
  const usage = usageStatus(ratio)
  const usedText = t('pools.usedOf', { used: format.bytes(used), total: format.bytes(total) })
  const percent = format.number(ratio * 100)

  return (
    <div className="flex flex-col gap-3">
      <p className="flex flex-wrap items-baseline gap-x-3">
        <PosterNumber
          value={freeParts.value}
          unit={freeParts.unit}
          className="text-5xl leading-none font-semibold tracking-tighter md:text-6xl"
        />
        <span className="label-mono text-muted-foreground">{t('pools.free')}</span>
      </p>
      <Meter
        value={ratio}
        status={usage}
        label={name}
        valueText={t('pools.capacityLabel', {
          name,
          used: format.bytes(used),
          total: format.bytes(total),
          free: format.bytes(free),
        })}
      />
      <p className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1 text-sm text-muted-foreground tabular-nums">
        <span>{usedText}</span>
        <span className="flex items-center gap-3">
          {usage !== 'ok' && (
            <StatusLabel status={usage} label={usage === 'crit' ? t('pools.almostFull') : t('pools.gettingFull')} />
          )}
          <span>{t('pools.percentUsed', { percent })}</span>
        </span>
      </p>
    </div>
  )
}

const MEMBER_STATUS: Record<MemberState, HealthStatus> = {
  ok: 'ok',
  present: 'info',
  missing: 'crit',
  failing: 'crit',
  rebuilding: 'info',
}

function Members({ members, known }: { members: MemberView[]; known: boolean }) {
  const { t } = useTranslation()
  const format = useFormat()
  const { reduced } = useReveal()

  // Scroll to the disk row and move focus there (keyboard and screen-reader users follow).
  const goToDisk = (id: string) => (event: MouseEvent<HTMLAnchorElement>) => {
    const row = document.getElementById(diskAnchorId(id))
    if (!row) return
    event.preventDefault()
    row.scrollIntoView({ behavior: reduced ? 'auto' : 'smooth', block: 'start' })
    row.focus({ preventScroll: true })
  }

  return (
    <ul className="flex flex-col gap-2">
      {members.map((member) => {
        const { id, disk, state } = member
        const label = (
          <StatusLabel
            status={MEMBER_STATUS[state]}
            label={
              state === 'failing' && member.errors > 0
                ? t('repair.member.failingCount', { count: member.errors, formatted: format.number(member.errors) })
                : t(`repair.member.${state}`)
            }
          />
        )
        return (
          <li key={id} className="flex flex-col">
            {disk ? (
              <span className="flex flex-wrap items-center gap-x-4 gap-y-1">
                <a
                  href={`#${diskAnchorId(id)}`}
                  onClick={goToDisk(id)}
                  title={t('pools.showDisk')}
                  className="group inline-flex min-h-11 flex-col justify-center self-start py-1"
                >
                  <span className="flex flex-wrap items-baseline gap-x-3">
                    <span className="font-semibold underline decoration-border-hover decoration-1 underline-offset-4 transition-colors duration-150 ease-crisp group-hover:decoration-accent">
                      {diskTitle(disk, t('common.unknown'))}
                    </span>
                    <span className="font-mono text-sm text-muted-foreground tabular-nums">{format.bytes(disk.sizeBytes)}</span>
                  </span>
                  <span className="font-mono text-xs break-all text-muted-foreground">{id}</span>
                </a>
                {known && label}
              </span>
            ) : (
              <span className="flex min-h-11 flex-wrap items-center gap-x-4 gap-y-1 py-1">
                <span className="font-mono text-sm break-all">{id}</span>
                {known && label}
              </span>
            )}
          </li>
        )
      })}
    </ul>
  )
}

function ArrayInfo({ md }: { md: NonNullable<NonNullable<Pool['status']>['md']> }) {
  const { t } = useTranslation()
  const text = usePoolText()
  const format = useFormat()
  const complete = md.expected === 0 || md.active >= md.expected
  const percent = md.sync ? format.number(md.sync.percent, 1) : ''
  const syncLabel = md.sync ? t('pools.sync.label', { action: text.syncLabel(md.sync.action), percent }) : ''

  return (
    <div className="flex flex-col gap-2">
      <span className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <span className="tabular-nums">
          {t('pools.arrayLine', { level: md.level.toUpperCase(), count: md.active, expected: md.expected })}
        </span>
        <StatusLabel
          status={complete ? 'ok' : 'crit'}
          label={complete ? t('pools.arrayComplete') : t('pools.arrayIncomplete')}
        />
      </span>
      <span className="font-mono text-xs break-all text-muted-foreground">{md.device}</span>
      {md.sync && (
        <div className="mt-2 flex max-w-md flex-col gap-2">
          <Meter role="progressbar" value={md.sync.percent / 100} status="info" label={syncLabel} valueText={`${percent} %`} />
          <span className="label-mono text-muted-foreground tabular-nums">{syncLabel}</span>
        </div>
      )}
    </div>
  )
}

interface ScrubInfoProps {
  pool: Pool
  /** Polling gave up while the scrub was still running. */
  timedOut: boolean
  onStarted: (fresh: Pool | null) => void
}

function ScrubInfo({ pool, timedOut, onStarted }: ScrubInfoProps) {
  const { t } = useTranslation()
  const text = usePoolText()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const [phase, setPhase] = useState<'idle' | 'starting' | 'started'>('idle')
  const [error, setError] = useState<unknown>(null)

  const status = pool.status
  if (!status) return null
  const scrub = status.scrub
  const running = scrub.status === 'running'
  // A broken array (ADR-0032) serves no data: nothing to read until it is reassembled.
  const readable = status.mounted && status.md?.state !== 'broken'
  const startedTs = scrubStartedAt(scrub)
  const duration = scrubDuration(scrub)

  const stopped = scrub.status === 'aborted' || scrub.status === 'interrupted'

  const stop = async () => {
    setError(null)
    try {
      await cancelScrub(pool.name)
      onStarted(await getPool(pool.name).catch(() => null))
    } catch (caught) {
      setError(caught)
    }
  }

  const run = async (resume = false) => {
    setPhase('starting')
    setError(null)
    try {
      await (resume ? resumeScrub(pool.name) : startScrub(pool.name))
      let fresh: Pool | null = null
      try {
        fresh = await getPool(pool.name)
      } catch {
        /* polling or the next refresh will catch up */
      }
      setPhase('started')
      onStarted(fresh)
    } catch (caught) {
      setError(caught)
      setPhase('idle')
    }
  }

  const message =
    phase === 'started'
      ? running
        ? timedOut
          ? t('pools.scrubStillRunning')
          : t('pools.scrubStarted')
        : t('pools.scrubFinished')
      : running && timedOut
        ? t('pools.scrubStillRunning')
        : ''

  return (
    <div className="flex flex-col gap-2">
      <span className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <StatusLabel status={scrubStatus(scrub)} label={text.scrubLabel(scrub.status)} />
        {startedTs !== null ? (
          <span className="text-sm text-muted-foreground tabular-nums">
            {t('pools.scrubLast', { date: format.dateTime(startedTs) })}
            {duration !== null && !running && ` · ${t('pools.scrubTook', { duration: format.duration(duration) })}`}
          </span>
        ) : (
          scrub.started && <span className="font-mono text-sm text-muted-foreground">{scrub.started}</span>
        )}
      </span>
      {scrub.status !== 'never' && !running && (
        <span className="text-sm text-muted-foreground tabular-nums">
          {t('pools.scrubResult', {
            corrected: format.number(scrub.corrected),
            uncorrectable: format.number(scrub.uncorrectable),
          })}
        </span>
      )}
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 pt-2">
        {running ? (
          <Button variant="secondary" size="sm" icon={<Icon icon={Square} />} onClick={() => void stop()}>
            {t('pools.scrubStop')}
          </Button>
        ) : (
          <>
            {stopped && (
              <Button
                variant="secondary"
                size="sm"
                icon={<Icon icon={Play} />}
                onClick={() => void run(true)}
                busy={phase === 'starting'}
                disabled={!readable}
              >
                {t('pools.scrubResume')}
              </Button>
            )}
            <Button
              variant={stopped ? 'ghost' : 'secondary'}
              size="sm"
              icon={<Icon icon={ScanSearch} />}
              onClick={() => void run()}
              busy={phase === 'starting' && !stopped}
              disabled={!readable || phase === 'starting'}
            >
              {phase === 'starting' && !stopped ? t('pools.scrubStarting') : t('pools.scrubRun')}
            </Button>
          </>
        )}
        <span className="max-w-sm text-sm text-muted-foreground">
          {!status.mounted ? t('pools.scrubNotMounted') : stopped ? t('pools.scrubStoppedHint') : t('pools.scrubHint')}
        </span>
      </div>
      <p className="text-sm text-foreground" role="status">
        {message}
      </p>
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
    </div>
  )
}
