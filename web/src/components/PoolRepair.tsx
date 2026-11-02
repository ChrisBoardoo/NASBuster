// SPDX-License-Identifier: AGPL-3.0-or-later
// The pool page's answer to a failure (ADR-0012), one situation at a time:
// a parity pool lost more disks than it tolerates → all back: reassemble (ADR-0032, typed
// name), else out of service (plug them back, never disk by disk);
// a missing disk kept the pool from starting → mount degraded (explicit, typed name);
// an unplugged disk came back → take it back (ADR-0027, remount on a click);
// degraded → replace a disk (wizard); rebuilding → progress; single-copy chunks → restore;
// read/write errors → scrub, then reset the counters; a degraded period not yet checked →
// resync scrub; still mounted degraded → restart.
import { ArrowRight, Combine, Plug, RotateCcw, ScanSearch, ShieldAlert, Wrench } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  clearErrors,
  mountDegraded,
  reassemblePool,
  restoreRedundancy,
  resumePool,
  resumeScrub,
  startScrub,
} from '../api/endpoints'
import type { Pool, PoolStatus } from '../api/types'
import { useNow } from '../lib/hooks'
import { useErrorMessage } from '../lib/messages'
import { type MemberView, type RepairKind, backgroundWork, memberLabel, scrubAllowsClear } from '../lib/repair'
import { useFormat } from '../lib/useFormat'
import { Button, ButtonLink } from './Button'
import { ConfirmName } from './ConfirmName'
import { Icon } from './Icon'
import { Meter } from './Meter'
import { Notice } from './Notice'
import { StatusLabel } from './StatusLabel'

export const replacePath = (pool: string) => `/storage/pools/${encodeURIComponent(pool)}/replace`

interface PoolRepairProps {
  pool: Pool
  kind: RepairKind
  members: MemberView[]
  /** Seconds left, from the observed rate (null: no honest estimate yet). */
  secondsLeft: number | null
  /** A fresh pool returned by an action. */
  onPool: (pool: Pool) => void
  /** Ask the row to poll now (an action started background work). */
  onRefresh: () => void
}

export function PoolRepair({ pool, kind, members, secondsLeft, onPool, onRefresh }: PoolRepairProps) {
  const status = pool.status
  if (!status || !kind) return null
  switch (kind) {
    case 'reassemble':
      return <Reassemble pool={pool} members={members} onPool={onPool} onRefresh={onRefresh} />
    case 'outOfService':
      return <OutOfService pool={pool} members={members} />
    case 'mountDegraded':
      return <MountDegraded pool={pool} onPool={onPool} />
    case 'resume':
      return <Resume pool={pool} members={members} onPool={onPool} />
    case 'replace':
      return <ReplacePrompt pool={pool} members={members} />
    case 'progress':
      return <Progress status={status} secondsLeft={secondsLeft} />
    case 'restore':
      return <Restore pool={pool} onRefresh={onRefresh} />
    case 'errors':
      return <Errors pool={pool} status={status} members={members} onRefresh={onRefresh} />
    case 'resync':
      return <Resync pool={pool} status={status} onRefresh={onRefresh} />
    case 'leaveDegraded':
      return <LeaveDegraded />
  }
}

// ---- More disks lost than the parity tolerates (ADR-0032) ------------------------------

/** Numbered steps, like the rest of the repair cards. */
function Steps({ title, steps }: { title: string; steps: string[] }) {
  return (
    <div className="flex flex-col gap-3">
      <h4 className="label-mono text-muted-foreground">{title}</h4>
      <ol className="flex flex-col gap-2">
        {steps.map((step, index) => (
          <li key={index} className="grid grid-cols-[2rem_1fr] items-baseline gap-2">
            <span className="font-mono text-sm text-accent tabular-nums">{String(index + 1).padStart(2, '0')}</span>
            <span>{step}</span>
          </li>
        ))}
      </ol>
    </div>
  )
}

// Every disk is back: one forced assembly restarts the array, then md repairs the parity
// and a scrub checks every file.
function Reassemble({
  pool,
  members,
  onPool,
  onRefresh,
}: {
  pool: Pool
  members: MemberView[]
  onPool: (pool: Pool) => void
  onRefresh: () => void
}) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const confirmed = typed === pool.name
  const gap = pool.status?.reassemble?.eventsGap ?? 0
  const present = members.filter((m) => m.disk !== null)

  const reassemble = async () => {
    if (!confirmed) return
    setBusy(true)
    setError(null)
    try {
      onPool(await reassemblePool(pool.name, typed))
    } catch (caught) {
      setError(caught)
      // A failure may have left the pool unmounted ("changed", helper.failed): read it again.
      onRefresh()
    } finally {
      setBusy(false)
    }
  }

  const cancel = () => {
    setOpen(false)
    setTyped('')
    setError(null)
  }

  return (
    <section className="flex flex-col gap-6">
      <Notice status="crit" title={t('repair.reassemble.title')}>
        {t('repair.reassemble.body')}
      </Notice>
      {present.length > 0 && (
        <div className="flex flex-col gap-2">
          <h4 className="label-mono text-muted-foreground">{t('repair.reassemble.disks', { count: present.length })}</h4>
          <ul className="flex flex-col gap-1 text-sm">
            {present.map((member) => (
              <li key={member.id} className="font-semibold">
                {memberLabel(member, t('common.unknown'))}
              </li>
            ))}
          </ul>
        </div>
      )}
      {gap > 0 && <p className="max-w-2xl text-sm text-muted-foreground">{t('repair.reassemble.gap', { count: gap })}</p>}
      <Steps
        title={t('repair.reassemble.stepsTitle')}
        steps={[t('repair.reassemble.step1'), t('repair.reassemble.step2'), t('repair.reassemble.step3')]}
      />
      {!open ? (
        <div>
          <Button size="sm" icon={<Icon icon={Combine} />} onClick={() => setOpen(true)}>
            {t('repair.reassemble.now')}
          </Button>
        </div>
      ) : (
        <form
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            void reassemble()
          }}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && event.target instanceof HTMLInputElement) event.preventDefault()
          }}
          className="flex flex-col gap-5 border-l-2 border-status-warn pl-5"
        >
          <Notice status="warn" title={t('repair.reassemble.onlyIf')}>
            {t('repair.reassemble.cut')}
          </Notice>
          <ConfirmName name={pool.name} value={typed} onChange={setTyped} />
          {error !== null && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
          <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
            <Button type="submit" busy={busy} disabled={!confirmed}>
              {busy ? t('repair.reassemble.working') : t('repair.reassemble.confirm')}
            </Button>
            <Button variant="ghost" className="-ml-4" onClick={cancel} disabled={busy}>
              {t('common.cancel')}
            </Button>
          </div>
        </form>
      )}
    </section>
  )
}

/** Reasons the server gives (`reassemble.reason`) that have their own text. */
const OUT_OF_SERVICE = ['missing_disks', 'roles', 'events_gap'] as const

// Not every disk is back, or the disks disagree: nothing to click, only what to do.
function OutOfService({ pool, members }: { pool: Pool; members: MemberView[] }) {
  const { t } = useTranslation()
  const info = pool.status?.reassemble
  const key = OUT_OF_SERVICE.find((reason) => reason === info?.reason) ?? 'other'
  const gone = members.filter((m) => m.disk === null)
  const missing = info ? Math.max(1, info.expected - info.members.length) : Math.max(1, gone.length)

  return (
    <section className="flex flex-col gap-5">
      <Notice status="crit" title={t(`repair.outOfService.${key}.title`, { count: missing })}>
        {t(`repair.outOfService.${key}.body`, { count: info?.eventsGap ?? 0 })}
      </Notice>
      {key === 'missing_disks' && gone.length > 0 && (
        <div className="flex flex-col gap-2">
          <h4 className="label-mono text-muted-foreground">{t('repair.outOfService.gone')}</h4>
          <ul className="flex flex-col gap-1 text-sm">
            {gone.map((member) => (
              <li key={member.id} className="font-mono">
                {memberLabel(member, t('common.unknown'))}
              </li>
            ))}
          </ul>
        </div>
      )}
      <Notice status="warn">{t('repair.outOfService.touchNothing')}</Notice>
    </section>
  )
}

// ---- Unmounted because a disk is missing -----------------------------------------------

function MountDegraded({ pool, onPool }: { pool: Pool; onPool: (pool: Pool) => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const confirmed = typed === pool.name

  const mount = async () => {
    if (!confirmed) return
    setBusy(true)
    setError(null)
    try {
      onPool(await mountDegraded(pool.name, typed))
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-6">
      <Notice status="crit" title={t('repair.missing.title')}>
        {t('repair.missing.body')}
      </Notice>
      <Steps
        title={t('repair.missing.firstTitle')}
        steps={[t('repair.missing.first1'), t('repair.missing.first2'), t('repair.missing.first3')]}
      />
      {!open ? (
        <div>
          <Button variant="secondary" size="sm" icon={<Icon icon={ShieldAlert} />} onClick={() => setOpen(true)}>
            {t('repair.missing.gone')}
          </Button>
        </div>
      ) : (
        <form
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            void mount()
          }}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && event.target instanceof HTMLInputElement) event.preventDefault()
          }}
          className="flex flex-col gap-5 border-l-2 border-status-warn pl-5"
        >
          <p className="text-lg font-semibold tracking-tight">{t('repair.degraded.title')}</p>
          <p className="max-w-2xl text-muted-foreground">{t('repair.degraded.what')}</p>
          <Notice status="warn" title={t('repair.degraded.onlyIf')}>
            {t('repair.degraded.risk')}
          </Notice>
          <ConfirmName name={pool.name} value={typed} onChange={setTyped} />
          {error !== null && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
          <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
            <Button type="submit" busy={busy} disabled={!confirmed}>
              {busy ? t('repair.degraded.mounting') : t('repair.degraded.mount')}
            </Button>
            <Button variant="ghost" className="-ml-4" onClick={() => setOpen(false)} disabled={busy}>
              {t('common.cancel')}
            </Button>
          </div>
        </form>
      )}
    </section>
  )
}

// ---- An unplugged disk came back: the pool uses it again only once remounted -----------

function Resume({ pool, members, onPool }: { pool: Pool; members: MemberView[]; onPool: (pool: Pool) => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  // Connected, yet missing from the pool: the disks that came back.
  const back = members.filter((m) => m.disk !== null && m.state === 'missing')
  // Parity: md re-adds the disk from its bitmap, nothing is unmounted or cut.
  const md = Boolean(pool.status?.md)

  const resume = async () => {
    setBusy(true)
    setError(null)
    try {
      onPool(await resumePool(pool.name))
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-5">
      <Notice status="crit" title={t('repair.resume.title')}>
        {md ? t('repair.resume.bodyMd') : t('repair.resume.body')}
      </Notice>
      {back.length > 0 && (
        <ul className="flex flex-col gap-1 text-sm">
          {back.map((member) => (
            <li key={member.id} className="font-semibold">
              {memberLabel(member, t('common.unknown'))}
            </li>
          ))}
        </ul>
      )}
      {!open ? (
        <div>
          <Button size="sm" icon={<Icon icon={Plug} />} onClick={() => setOpen(true)}>
            {t('repair.resume.now')}
          </Button>
        </div>
      ) : (
        <div className="flex flex-col gap-5 border-l-2 border-status-warn pl-5">
          {!md && <Notice status="warn">{t('repair.resume.cut')}</Notice>}
          {error !== null && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
          <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
            <Button busy={busy} onClick={() => void resume()}>
              {busy ? t('repair.resume.working') : t('repair.resume.confirm')}
            </Button>
            <Button variant="ghost" className="-ml-4" onClick={() => setOpen(false)} disabled={busy}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      )}
    </section>
  )
}

// ---- Degraded: replace a disk ----------------------------------------------------------

function ReplacePrompt({ pool, members }: { pool: Pool; members: MemberView[] }) {
  const { t } = useTranslation()
  const missing = members.filter((m) => m.state === 'missing').length
  const failing = members.filter((m) => m.state === 'failing').length
  return (
    <section className="flex flex-col gap-5">
      <Notice
        status="crit"
        title={missing > 0 ? t('repair.replace.missingTitle', { count: missing }) : t('repair.replace.failingTitle', { count: Math.max(1, failing) })}
      >
        {t('repair.replace.body')}
      </Notice>
      <div>
        <ButtonLink href={replacePath(pool.name)} icon={<Icon icon={Wrench} />} iconEnd={<Icon icon={ArrowRight} />}>
          {t('repair.replace.cta')}
        </ButtonLink>
      </div>
    </section>
  )
}

// ---- Rebuilding / restoring ---------------------------------------------------------------

function Progress({ status, secondsLeft }: { status: PoolStatus; secondsLeft: number | null }) {
  const { t } = useTranslation()
  const format = useFormat()
  const work = backgroundWork(status)
  if (!work) return null
  const percent = Math.max(0, Math.min(100, work.percent))
  const label = t(`repair.progress.${work.kind}`)
  const replaceErrors = (status.replace?.writeErrors ?? 0) + (status.replace?.readErrors ?? 0)

  return (
    <section className="flex flex-col gap-4 border-l-2 border-status-info pl-5" aria-live="polite">
      <p className="text-2xl font-semibold tracking-tight">{label}</p>
      <div className="flex flex-col gap-2">
        <Meter role="progressbar" value={percent / 100} status="info" label={label} valueText={`${format.number(percent, 1)} %`} />
        <p className="flex flex-wrap justify-between gap-x-6 gap-y-1 text-sm text-muted-foreground tabular-nums">
          <span className="text-foreground">{format.number(percent, 1)} %</span>
          <span>{secondsLeft !== null ? t('repair.progress.left', { time: format.duration(secondsLeft) }) : t('repair.progress.measuring')}</span>
        </p>
      </div>
      <p className="max-w-2xl text-muted-foreground">{t('repair.progress.usable')}</p>
      {work.kind === 'replace' && replaceErrors > 0 && (
        <StatusLabel status="warn" label={t('repair.progress.errors', { count: replaceErrors })} />
      )}
    </section>
  )
}

function Restore({ pool, onRefresh }: { pool: Pool; onRefresh: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [message, setMessage] = useState('')

  const restore = async () => {
    setBusy(true)
    setError(null)
    setMessage('')
    try {
      const started = await restoreRedundancy(pool.name)
      setMessage(started.balanceStarted ? t('repair.restore.started') : t('repair.restore.nothing'))
      onRefresh()
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-4">
      <Notice status="warn" title={t('repair.restore.title')}>
        {t('repair.restore.body')}
      </Notice>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
        <Button variant="secondary" size="sm" icon={<Icon icon={RotateCcw} />} onClick={() => void restore()} busy={busy}>
          {t('repair.restore.now')}
        </Button>
        <span className="text-sm text-foreground" role="status">
          {message}
        </span>
      </div>
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
    </section>
  )
}

// ---- Read/write errors: scrub, then reset the counters --------------------------------------

function Errors({ pool, status, members, onRefresh }: { pool: Pool; status: PoolStatus; members: MemberView[]; onRefresh: () => void }) {
  const { t } = useTranslation()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const now = useNow(30_000)
  const [busy, setBusy] = useState<'scrub' | 'clear' | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [message, setMessage] = useState('')
  const failing = members.filter((m) => m.state === 'failing')
  const scrubRunning = status.scrub.status === 'running'
  const canClear = scrubAllowsClear(status, now)

  const run = async (kind: 'scrub' | 'clear') => {
    setBusy(kind)
    setError(null)
    setMessage('')
    try {
      if (kind === 'scrub') {
        await startScrub(pool.name)
        setMessage(t('pools.scrubStarted'))
      } else {
        await clearErrors(pool.name)
        setMessage(t('repair.errors.cleared'))
      }
      onRefresh()
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(null)
    }
  }

  return (
    <section className="flex flex-col gap-5">
      <Notice status="crit" title={t('repair.errors.title')}>
        {t('repair.errors.body')}
      </Notice>
      {failing.length > 0 && (
        <ul className="flex flex-col gap-1 text-sm">
          {failing.map((member) => (
            <li key={member.id} className="flex flex-wrap gap-x-3">
              <span className="font-semibold">{memberLabel(member, t('common.unknown'))}</span>
              <span className="text-status-crit tabular-nums">{t('repair.errors.count', { count: member.errors, formatted: format.number(member.errors) })}</span>
            </li>
          ))}
        </ul>
      )}
      <ol className="flex flex-col gap-5">
        <li className="grid grid-cols-[2rem_1fr] items-baseline gap-2">
          <span className="font-mono text-sm text-accent">01</span>
          <div className="flex flex-col gap-2">
            <span>{t('repair.errors.step1')}</span>
            <div>
              <Button
                variant="secondary"
                size="sm"
                icon={<Icon icon={ScanSearch} />}
                onClick={() => void run('scrub')}
                busy={busy === 'scrub'}
                disabled={scrubRunning || busy !== null}
              >
                {scrubRunning ? t('pools.scrubStatus.running') : t('pools.scrubRun')}
              </Button>
            </div>
          </div>
        </li>
        <li className="grid grid-cols-[2rem_1fr] items-baseline gap-2">
          <span className="font-mono text-sm text-accent">02</span>
          <div className="flex flex-col gap-2">
            <span>{t('repair.errors.step2')}</span>
            {!canClear && <span className="text-sm text-muted-foreground">{t('repair.errors.needScrub')}</span>}
            <div>
              <Button
                variant="secondary"
                size="sm"
                icon={<Icon icon={RotateCcw} />}
                onClick={() => void run('clear')}
                busy={busy === 'clear'}
                disabled={!canClear || busy !== null}
              >
                {t('repair.errors.clear')}
              </Button>
            </div>
          </div>
        </li>
        <li className="grid grid-cols-[2rem_1fr] items-baseline gap-2">
          <span className="font-mono text-sm text-accent">03</span>
          <span>
            {t('repair.errors.step3')}{' '}
            <ButtonLink href={replacePath(pool.name)} variant="ghost" size="sm" className="-ml-1 align-baseline">
              {t('repair.replace.cta')}
            </ButtonLink>
          </span>
        </li>
      </ol>
      <p className="text-sm text-foreground" role="status">
        {message}
      </p>
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
    </section>
  )
}

// ---- A degraded period not checked yet: a scrub before "healthy" (ADR-0027) ----------------

function Resync({ pool, status, onRefresh }: { pool: Pool; status: PoolStatus; onRefresh: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const running = status.scrub.status === 'running'
  // Stopped by the user or cut by a reboot: go on where it stopped.
  const stopped = status.scrub.status === 'aborted' || status.scrub.status === 'interrupted'
  // After a reassembly (ADR-0032): md repairs the parity first, the NAS starts both by itself.
  const repairFirst = Boolean(status.repairNeeded)

  const scrub = async () => {
    setBusy(true)
    setError(null)
    try {
      await (stopped ? resumeScrub(pool.name) : startScrub(pool.name))
      onRefresh()
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-4" aria-live="polite">
      <Notice status="warn" title={t('repair.resync.title')}>
        {t('repair.resync.body')}
      </Notice>
      <p className="max-w-2xl text-muted-foreground">
        {repairFirst
          ? t('repair.resync.repairFirst')
          : running
            ? t('repair.resync.running')
            : stopped
              ? t('repair.resync.stopped')
              : t('repair.resync.waiting')}
      </p>
      {!running && !repairFirst && (
        <div>
          <Button variant="secondary" size="sm" icon={<Icon icon={ScanSearch} />} onClick={() => void scrub()} busy={busy}>
            {stopped ? t('pools.scrubResume') : t('pools.scrubRun')}
          </Button>
        </div>
      )}
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
    </section>
  )
}

function LeaveDegraded() {
  const { t } = useTranslation()
  return <Notice status="info">{t('repair.leaveDegraded')}</Notice>
}
