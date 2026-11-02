// SPDX-License-Identifier: AGPL-3.0-or-later
// Pool step 3: POST /storage/pools. Sent exactly once per confirmation, never retried, never
// aborted; navigation is locked while it runs (it can take minutes on USB disks). Shows the
// ready pool, the failure (translated, with the technical detail), or, when the answer was
// lost (connection, reload), a check of what actually exists.
import { Eraser } from 'lucide-react'
import { type ReactNode, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, NETWORK_ERROR, UNKNOWN_ERROR } from '../../api/client'
import { createPool, getDisks, getPools } from '../../api/endpoints'
import type { Pool } from '../../api/types'
import { useApi } from '../../api/useApi'
import { Button } from '../../components/Button'
import { Icon } from '../../components/Icon'
import { InlineLoading, ScanLine } from '../../components/Loading'
import { Notice } from '../../components/Notice'
import { StatusLabel } from '../../components/StatusLabel'
import { WipePanel } from '../../components/WipePanel'
import { PosterNumber } from '../../components/Typography'
import { CREATE_MIN_DISPLAY_MS, POOL_MOUNT_ROOT } from '../../lib/constants'
import { useNow } from '../../lib/hooks'
import { useErrorMessage } from '../../lib/messages'
import { clock, poolCapacity, poolHealthStatus } from '../../lib/pools'
import { useFormat } from '../../lib/useFormat'
import { usePoolText } from '../../lib/usePoolText'
import { StepLayout } from '../setup/StepLayout'
import { type PoolOutcome, type PoolSubmission, useWizard } from '../setup/wizard'

/** Submissions already sent: survives StrictMode's double effects and step remounts. */
const sent = new WeakSet<PoolSubmission>()

/** Error codes answered before anything was written to the disks. */
function failedBeforeWrite(error: ApiError): boolean {
  return (
    error.code.startsWith('validation.') ||
    error.code.startsWith('request.') ||
    [
      'storage.pool_exists',
      'storage.disk_not_available',
      'storage.disk_not_found',
      'storage.disk_too_small',
      'storage.unavailable',
      'helper.unavailable',
      'csrf.invalid',
      'auth.required',
    ].includes(error.code)
  )
}

/** A server answer (even an error) tells us what happened; a lost answer doesn't. */
function toOutcome(error: unknown): PoolOutcome {
  if (error instanceof ApiError && error.status > 0 && error.code !== NETWORK_ERROR && error.code !== UNKNOWN_ERROR) {
    return { kind: 'failed', error }
  }
  return { kind: 'unknown', error }
}

export function CreateStep() {
  const { drafts, updateDrafts, persistQuietly, goTo } = useWizard()
  const submission = drafts.pool.submission
  const outcome = submission?.outcome ?? null
  const running = submission !== null && submission.request !== null && outcome === null

  // Send once. The promise settles into the wizard drafts, which outlive this component.
  useEffect(() => {
    if (!submission || !submission.request || submission.outcome || sent.has(submission)) return
    sent.add(submission)
    const started = Date.now()
    const settle = async (result: PoolOutcome) => {
      const wait = CREATE_MIN_DISPLAY_MS - (Date.now() - started)
      if (wait > 0) await new Promise((resolve) => window.setTimeout(resolve, wait))
      updateDrafts((d) =>
        d.pool.submission === submission
          ? { ...d, pool: { ...d.pool, submission: { ...submission, outcome: result } } }
          : d,
      )
      if (result.kind === 'created') void persistQuietly({ currentStep: 'share', completedStep: 'create' })
      else if (result.kind === 'failed') void persistQuietly({ currentStep: 'protection' })
    }
    createPool(submission.request).then(
      (pool) => settle({ kind: 'created', pool }),
      (error: unknown) => settle(toOutcome(error)),
    )
  }, [submission, updateDrafts, persistQuietly])

  // Closing or reloading the tab mid-creation loses the answer: ask the browser to confirm.
  useEffect(() => {
    if (!running) return
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [running])

  const backToProtection = () => {
    updateDrafts((d) => ({ ...d, pool: { ...d.pool, submission: null } }))
    goTo('protection')
  }

  if (running && submission) return <RunningView submission={submission} />
  if (outcome?.kind === 'created') return <DoneView pool={outcome.pool} />
  if (outcome?.kind === 'failed') return <FailedView error={outcome.error} onBack={backToProtection} />
  return <UnknownView expectedName={submission?.request?.name ?? null} onBack={backToProtection} />
}

function RunningView({ submission }: { submission: PoolSubmission }) {
  const { t } = useTranslation()
  const text = usePoolText()
  const now = useNow(1000)
  const request = submission.request
  const count = request?.disks.length ?? 0

  return (
    <StepLayout
      key="running"
      eyebrow={t('onboarding.create.eyebrow')}
      title={t('onboarding.create.runningTitle')}
      intro={t('onboarding.create.runningBody', { count })}
      onContinue={() => undefined}
      continueLabel={t('onboarding.create.running')}
      busy
    >
      <div className="flex max-w-3xl flex-col gap-5">
        <div role="progressbar" aria-label={t('onboarding.create.progressLabel')} aria-busy="true">
          <ScanLine thick className="w-full" />
        </div>
        <div className="flex flex-wrap items-baseline justify-between gap-x-8 gap-y-2">
          {request && (
            <p className="label-mono flex flex-wrap gap-x-3 text-muted-foreground">
              <span className="text-foreground normal-case">{request.name}</span>
              <span aria-hidden="true">·</span>
              <span>{text.layoutTitle(request.layout)}</span>
            </p>
          )}
          {/* Ticking clock: visual only, so screen readers aren't flooded. */}
          <p className="label-mono flex gap-3 text-muted-foreground" aria-hidden="true">
            <span>{t('onboarding.create.elapsed')}</span>
            <span className="text-foreground tabular-nums">{clock((now - submission.startedAt) / 1000)}</span>
          </p>
        </div>
      </div>
    </StepLayout>
  )
}

function DoneView({ pool }: { pool: Pool }) {
  const { t } = useTranslation()
  const text = usePoolText()
  const format = useFormat()
  const { mode, drafts, finishPool } = useWizard()
  const status = pool.status

  // Usable size: measured on the pool, else the preview it was created from.
  const plan = drafts.pool.plan?.value
  const planned =
    plan && plan.layout === pool.layout && plan.disks === pool.members.length ? plan.usableBytes : null
  const usableBytes = poolCapacity(status)?.total ?? planned
  const usable = usableBytes !== null ? format.bytesParts(usableBytes) : null
  const sync = status?.md?.sync ?? null
  const tech = text.layoutTech(pool.layout)

  return (
    <StepLayout
      key="done"
      eyebrow={t('onboarding.create.doneEyebrow')}
      title={t('onboarding.create.doneTitle')}
      intro={t('onboarding.create.doneBody', { name: pool.name })}
      onContinue={finishPool}
      continueLabel={mode === 'standalone' ? t('onboarding.create.goToStorage') : t('common.continue')}
    >
      <div className="grid max-w-6xl gap-x-16 gap-y-10 lg:grid-cols-12 lg:items-end">
        <div className="flex flex-col gap-4 lg:col-span-6">
          <span aria-hidden="true" className="h-1 w-16 bg-accent" />
          <p className="text-[length:clamp(4.5rem,16vw,10rem)] leading-[0.85] font-semibold tracking-tighter">
            {usable ? <PosterNumber value={usable.value} unit={usable.unit} /> : '—'}
          </p>
          <span className="label-mono text-muted-foreground">{t('onboarding.create.usable')}</span>
        </div>
        <dl className="border-b border-border lg:col-span-6">
          <DoneRow label={t('onboarding.create.protection')}>
            {text.layoutTitle(pool.layout)}
            {tech && <span className="label-mono ml-3 font-normal text-muted-foreground">{tech}</span>}
          </DoneRow>
          <DoneRow label={t('onboarding.create.health')}>
            <StatusLabel status={poolHealthStatus(status?.health)} label={text.healthLabel(status?.health)} />
          </DoneRow>
          <DoneRow label={t('onboarding.create.mountPoint')}>
            <span className="font-mono break-all">{status?.mountPoint || `${POOL_MOUNT_ROOT}/${pool.name}`}</span>
          </DoneRow>
        </dl>
      </div>
      {sync && (
        <Notice status="info" className="max-w-3xl">
          {t('onboarding.create.syncNote', { percent: format.number(sync.percent, 1) })}
        </Notice>
      )}
      {!status && pool.statusError && (
        <Notice status="warn" title={t('pools.statusUnavailable')} className="max-w-3xl">
          {text.statusError(pool.statusError)}
        </Notice>
      )}
    </StepLayout>
  )
}

function DoneRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[minmax(7rem,auto)_1fr] items-baseline gap-x-6 border-t border-border py-4">
      <dt className="label-mono text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-lg">{children}</dd>
    </div>
  )
}

function FailedView({ error, onBack }: { error: ApiError; onBack: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  // An old file system hidden behind a removed partition table: offer to erase that disk here.
  const residual =
    error.code === 'storage.disk_residual' && typeof error.params.disk === 'string'
      ? { disk: error.params.disk, found: typeof error.params.found === 'string' ? error.params.found : '' }
      : null
  const detail =
    (error.code === 'helper.failed' || error.code === 'internal.error') && error.message && error.message !== error.code
      ? error.message
      : null

  return (
    <StepLayout
      key="failed"
      eyebrow={t('onboarding.create.failedEyebrow')}
      title={t('onboarding.create.failedTitle')}
      onContinue={onBack}
      continueLabel={t('onboarding.create.backToProtection')}
    >
      <Notice status="crit" assertive className="max-w-3xl">
        <span className="block text-foreground">{errorMessage(error)}</span>
        {detail && (
          <span className="mt-3 flex flex-col gap-1">
            <span className="label-mono">{t('onboarding.create.technicalDetail')}</span>
            <code className="block border border-border bg-muted px-3 py-2 text-sm break-words text-foreground">
              {detail}
            </code>
          </span>
        )}
      </Notice>
      <p className="max-w-2xl text-lg text-muted-foreground">
        {residual
          ? t('onboarding.create.residualBody')
          : failedBeforeWrite(error)
            ? t('onboarding.create.failedBeforeWrite')
            : t('onboarding.create.failedDuringWrite')}
      </p>
      {residual && <ResidualErase diskId={residual.disk} found={residual.found} onWiped={onBack} />}
    </StepLayout>
  )
}

/** Erase the disk the helper refused, then go back to choose the disks again. */
function ResidualErase({ diskId, found, onWiped }: { diskId: string; found: string; onWiped: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [open, setOpen] = useState(false)
  const disks = useApi((signal) => getDisks({ signal }), [])
  const disk = disks.data?.disks.find((d) => d.id === diskId) ?? null

  if (disks.loading) return <InlineLoading label={t('disks.loading')} />
  if (disks.error) {
    return (
      <Notice status="warn" className="max-w-3xl">
        {errorMessage(disks.error)}
      </Notice>
    )
  }
  if (!disk) return null
  if (open) {
    return (
      <div className="max-w-3xl">
        <WipePanel disk={disk} residual={found} onCancel={() => setOpen(false)} onWiped={onWiped} />
      </div>
    )
  }
  return (
    <div>
      <Button variant="secondary" icon={<Icon icon={Eraser} />} onClick={() => setOpen(true)}>
        {t('wipe.offer')}
      </Button>
    </div>
  )
}

/** No answer (or a resumed session): look at what exists instead of guessing, never re-send. */
function UnknownView({ expectedName, onBack }: { expectedName: string | null; onBack: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const { updateDrafts, persistQuietly } = useWizard()
  const check = useApi((signal) => getPools({ signal }), [])
  const pools = check.data?.pools

  useEffect(() => {
    if (!pools || pools.length === 0) return
    const pool = expectedName
      ? pools.find((p) => p.name === expectedName)
      : [...pools].sort((a, b) => b.createdAt - a.createdAt)[0]
    if (!pool) return
    updateDrafts((d) => ({
      ...d,
      pool: {
        ...d.pool,
        submission: {
          request: d.pool.submission?.request ?? null,
          startedAt: d.pool.submission?.startedAt ?? 0,
          outcome: { kind: 'created', pool },
        },
      },
    }))
    void persistQuietly({ currentStep: 'share', completedStep: 'create' })
  }, [pools, expectedName, updateDrafts, persistQuietly])

  return (
    <StepLayout
      key="unknown"
      eyebrow={t('onboarding.create.eyebrow')}
      title={t('onboarding.create.unknownTitle')}
      intro={t('onboarding.create.unknownBody')}
      onContinue={check.reload}
      continueLabel={check.loading ? t('onboarding.create.checking') : t('onboarding.create.checkAgain')}
      busy={check.loading}
      secondaryAction={
        <Button variant="ghost" onClick={onBack}>
          {t('onboarding.create.backToProtection')}
        </Button>
      }
    >
      {check.loading ? (
        <InlineLoading label={t('onboarding.create.checking')} />
      ) : check.error ? (
        <Notice status="warn" className="max-w-3xl">
          {errorMessage(check.error)}
        </Notice>
      ) : (
        <Notice status="info" className="max-w-3xl">
          {t('onboarding.create.notFound')}
        </Notice>
      )}
    </StepLayout>
  )
}
