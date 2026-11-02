// SPDX-License-Identifier: AGPL-3.0-or-later
// /import — copy data from a disk into a share (ADR-0015): how to migrate from OMV, or empty a
// USB key. One decision per screen:
//   1. the source (mounted read-only on Continue: never modified);
//   2. what to copy (everything, or one top-level folder);
//   3. the destination share (preselected with ?share=), into a NEW folder: nothing is overwritten;
//   4. the progress (GET /import/progress/{share} every 2 s while visible), then eject the disk.
// ?share=<name> also resumes a copy that is still running into that share.
import { HardDrive, Usb, X } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation, useSearch } from 'wouter'
import { ApiError } from '../api/client'
import {
  getDisks,
  getImportProgress,
  getImportSources,
  getShares,
  mountImport,
  startImportCopy,
  unmountImport,
} from '../api/endpoints'
import type { Disk, ImportSource, Share } from '../api/types'
import { useApi } from '../api/useApi'
import { usePolling } from '../api/usePolling'
import { JobLog } from '../components/apps/JobLog'
import { Button, ButtonLink } from '../components/Button'
import { ChoiceList } from '../components/ChoiceList'
import { Icon } from '../components/Icon'
import { InlineLoading } from '../components/Loading'
import { Meter } from '../components/Meter'
import { Notice } from '../components/Notice'
import { StatusLabel } from '../components/StatusLabel'
import { JOB_POLL_MS } from '../lib/constants'
import { diskTitle } from '../lib/disks'
import { capitalizeFirst } from '../lib/format'
import { useDocumentTitle } from '../lib/hooks'
import { useDynamicT, useErrorMessage } from '../lib/messages'
import { type ProgressSample, estimateSecondsLeft, wipeSuffix } from '../lib/repair'
import { shareAnchorId, uncPath } from '../lib/shares'
import { useFormat } from '../lib/useFormat'
import { useReadySession } from '../session/SessionContext'
import { StepLayout } from './setup/StepLayout'
import {
  EMPTY_ACCOUNT_DRAFT,
  EMPTY_POOL_DRAFT,
  EMPTY_SAFETY_DRAFT,
  EMPTY_SHARE_DRAFT,
  type UiStep,
  type WizardApi,
  WizardContext,
} from './setup/wizard'
import { WizardFrame, useWizardKeyboard } from './setup/WizardFrame'

const STEPS = ['source', 'what', 'share', 'progress'] as const
type Step = (typeof STEPS)[number]

const noop = () => undefined
const noPersist = async () => undefined
const DRAFTS = {
  account: EMPTY_ACCOUNT_DRAFT,
  identity: { hostname: '', timezone: '' },
  checks: null,
  pool: EMPTY_POOL_DRAFT,
  share: EMPTY_SHARE_DRAFT,
  protection: null,
  safety: EMPTY_SAFETY_DRAFT,
}

interface Opened {
  uuid: string
  entries: string[]
  /** Mounted by this visit: unmounted again if the user leaves before copying. */
  byUs: boolean
}

export default function CopyWizard() {
  const { t } = useTranslation()
  const [, navigate] = useLocation()
  const params = new URLSearchParams(useSearch())
  const presetShare = params.get('share') ?? ''
  const presetSource = params.get('source') ?? ''
  const back = presetShare ? `/shares#${shareAnchorId(presetShare)}` : '/storage'

  const sources = useApi((signal) => getImportSources({ signal }), [])
  const shares = useApi((signal) => getShares({ signal }), [])
  const disks = useApi((signal) => getDisks({ signal }), [])
  // Coming back to a copy still running into this share: straight to its progress.
  const running = useApi(
    (signal) => (presetShare ? getImportProgress(presetShare, { signal }).catch(() => null) : Promise.resolve(null)),
    [presetShare],
  )

  const [index, setIndex] = useState(0)
  const [busy, setBusyState] = useState(false)
  const busyRef = useRef(false)
  const [uuid, setUuid] = useState<string | null>(presetSource || null)
  const [opened, setOpened] = useState<Opened | null>(null)
  const [subdir, setSubdir] = useState('')
  const [share, setShare] = useState<string | null>(presetShare || null)
  const [copying, setCopying] = useState<string | null>(null)

  useEffect(() => {
    if (running.data?.job.state === 'running' && presetShare) {
      setCopying(presetShare)
      setIndex(STEPS.indexOf('progress'))
    }
  }, [running.data, presetShare])

  const step: Step = STEPS[index] ?? 'source'
  useDocumentTitle(t(`copy.steps.${step}`))

  const leave = useCallback(() => {
    // Opened for nothing: close it again (best effort; it was read-only anyway).
    if (opened?.byUs && !copying) void unmountImport(opened.uuid).catch(() => undefined)
    navigate(back)
  }, [opened, copying, navigate, back])
  const goBack = useMemo(
    () => (index === 0 || step === 'progress' ? leave : () => setIndex((i) => i - 1)),
    [index, step, leave],
  )
  const setBusy = useCallback((value: boolean) => {
    busyRef.current = value
    setBusyState(value)
  }, [])
  useWizardKeyboard(goBack, busyRef)

  const api = useMemo<WizardApi>(
    () => ({
      mode: 'standalone',
      step: 'protection' as UiStep,
      goNext: () => setIndex((i) => Math.min(STEPS.length - 1, i + 1)),
      goBack,
      goTo: noop,
      drafts: DRAFTS,
      updateDrafts: noop,
      persist: noPersist,
      persistQuietly: noPersist,
      persistFailed: false,
      setBusy,
      skipPool: leave,
      finishPool: leave,
      setLocalBack: noop,
    }),
    [goBack, setBusy, leave],
  )

  const source = sources.data?.sources.find((s) => s.uuid === (opened?.uuid ?? uuid))
  const waiting = (sources.loading && !sources.data) || (presetShare && running.loading && running.data === undefined)

  let content
  if (step === 'progress' && copying) {
    content = <ProgressStep share={copying} onLeave={() => navigate(`/shares#${shareAnchorId(copying)}`)} />
  } else if (waiting) {
    content = (
      <StepLayout eyebrow={t('copy.eyebrow')} title={t('copy.source.title')} onContinue={noop} continueLabel={t('common.continue')} continueDisabled>
        <InlineLoading />
      </StepLayout>
    )
  } else if (step === 'source' || !source || !opened) {
    content = (
      <SourceStep
        sources={sources.data?.sources ?? []}
        error={sources.error}
        disks={disks.data?.disks}
        value={uuid}
        onChange={setUuid}
        onRefresh={sources.reload}
        refreshing={sources.loading}
        busy={busy}
        setBusy={setBusy}
        onOpened={(next) => {
          setOpened(next)
          setSubdir('')
          setIndex(1)
        }}
      />
    )
  } else if (step === 'what') {
    content = <WhatStep source={source} entries={opened.entries} value={subdir} onChange={setSubdir} onNext={() => setIndex(2)} />
  } else {
    content = (
      <ShareStep
        source={source}
        subdir={subdir}
        shares={shares.data?.shares}
        sharesError={shares.error}
        value={share}
        onChange={setShare}
        busy={busy}
        setBusy={setBusy}
        onStarted={(name) => {
          setCopying(name)
          setIndex(3)
        }}
      />
    )
  }

  return (
    <WizardContext.Provider value={api}>
      <WizardFrame
        index={index}
        total={STEPS.length}
        stepKey={step}
        stepName={t(`copy.steps.${step}`)}
        headerActions={
          <Button variant="ghost" size="sm" className="-mr-3 min-w-11" icon={<Icon icon={X} size={18} />} onClick={leave} disabled={busy}>
            <span className="sr-only sm:not-sr-only">{step === 'progress' ? t('copy.close') : t('common.cancel')}</span>
          </Button>
        }
      >
        {content}
      </WizardFrame>
    </WizardContext.Provider>
  )
}

/** "SanDisk Ultra (…ABCD)" for each disk of a source; md arrays list all of theirs. */
function useSourceText() {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  return {
    title: (src: ImportSource) => src.label || t('copy.source.noLabel'),
    fs: (src: ImportSource) => {
      const key = `wipe.fs.${src.fstype}`
      return has(key) ? capitalizeFirst(td(key)) : src.fstype
    },
    disks: (src: ImportSource, disks: readonly Disk[] | undefined) =>
      src.disks
        .map((id) => {
          const disk = disks?.find((d) => d.id === id)
          return disk ? `${diskTitle(disk, t('common.unknown'))} (…${wipeSuffix(id)})` : `…${wipeSuffix(id)}`
        })
        .join(', '),
  }
}

// ---- 1. The source ----------------------------------------------------------------------------

interface SourceStepProps {
  sources: ImportSource[]
  error: unknown
  disks: readonly Disk[] | undefined
  value: string | null
  onChange: (uuid: string) => void
  onRefresh: () => void
  refreshing: boolean
  busy: boolean
  setBusy: (busy: boolean) => void
  onOpened: (opened: Opened) => void
}

function SourceStep({ sources, error, disks, value, onChange, onRefresh, refreshing, busy, setBusy, onOpened }: SourceStepProps) {
  const { t } = useTranslation()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const text = useSourceText()
  const [mountError, setMountError] = useState<unknown>(null)
  const selected = sources.find((s) => s.uuid === value)

  // A single source: preselect it.
  useEffect(() => {
    const only = sources[0]
    if (!value && sources.length === 1 && only) onChange(only.uuid)
  }, [sources, value, onChange])

  const open = async () => {
    if (!selected || busy) return
    setBusy(true)
    setMountError(null)
    try {
      const mounted = await mountImport(selected.uuid)
      setBusy(false)
      onOpened({ uuid: selected.uuid, entries: mounted.entries, byUs: !selected.mounted })
    } catch (caught) {
      setMountError(caught)
      setBusy(false)
    }
  }

  return (
    <StepLayout
      key="source"
      eyebrow={t('copy.eyebrow')}
      title={t('copy.source.title')}
      intro={t('copy.source.intro')}
      onContinue={() => void open()}
      continueLabel={busy ? t('copy.source.opening') : t('copy.source.open')}
      busy={busy}
      continueDisabled={!selected}
    >
      <Notice status="info" className="max-w-3xl" title={t('copy.source.readOnly')}>
        {t('copy.source.readOnlyBody')}
      </Notice>
      {error !== undefined && error !== null && sources.length === 0 ? (
        <Notice status="warn">{errorMessage(error)}</Notice>
      ) : sources.length === 0 ? (
        <Notice
          status="info"
          className="max-w-3xl"
          title={t('copy.source.noneTitle')}
          actions={
            <Button variant="ghost" size="sm" className="-ml-3" busy={refreshing} onClick={onRefresh}>
              {t('common.refresh')}
            </Button>
          }
        >
          {t('copy.source.noneBody')}
        </Notice>
      ) : (
        <ChoiceList
          name="copy-source"
          label={t('copy.source.label')}
          value={value}
          onChange={onChange}
          items={sources.map((src) => {
            const size = format.bytesParts(src.sizeBytes)
            return {
              id: src.uuid,
              title: text.title(src),
              size: { value: size.value, unit: size.unit },
              meta: (
                <span className="flex flex-wrap items-center gap-x-4 gap-y-1">
                  <span className="text-sm text-muted-foreground">{text.fs(src)}</span>
                  {src.disks.length > 1 && <span className="label-mono text-muted-foreground">{t('copy.source.array', { count: src.disks.length })}</span>}
                  {src.mounted && <StatusLabel status="info" label={t('copy.source.opened')} />}
                </span>
              ),
              detail: text.disks(src, disks),
            }
          })}
        />
      )}
      {mountError !== null && (
        <Notice status="crit" assertive className="max-w-3xl">
          <span className="text-foreground">{errorMessage(mountError)}</span>
        </Notice>
      )}
    </StepLayout>
  )
}

// ---- 2. What to copy ------------------------------------------------------------------------------

function WhatStep({
  source,
  entries,
  value,
  onChange,
  onNext,
}: {
  source: ImportSource
  entries: string[]
  value: string
  onChange: (subdir: string) => void
  onNext: () => void
}) {
  const { t } = useTranslation()
  const format = useFormat()
  const text = useSourceText()
  const EVERYTHING = '\u0000all'
  return (
    <StepLayout
      key="what"
      eyebrow={t('copy.eyebrow')}
      title={t('copy.what.title')}
      intro={t('copy.what.intro', { source: text.title(source) })}
      onContinue={onNext}
      continueLabel={t('common.continue')}
    >
      {entries.length === 0 && (
        <Notice status="warn" className="max-w-3xl">
          {t('copy.what.empty')}
        </Notice>
      )}
      <ChoiceList
        name="copy-what"
        label={t('copy.what.label')}
        value={value === '' ? EVERYTHING : value}
        onChange={(id) => onChange(id === EVERYTHING ? '' : id)}
        items={[
          {
            id: EVERYTHING,
            title: t('copy.what.everything'),
            detail: t('copy.what.everythingDetail', { source: text.title(source), size: format.bytes(source.sizeBytes) }),
          },
          ...entries.map((entry) => ({ id: entry, title: entry, detail: null })),
        ]}
      />
      {entries.length >= 200 && <p className="max-w-3xl text-sm text-muted-foreground">{t('copy.what.truncated')}</p>}
    </StepLayout>
  )
}

// ---- 3. Into which share ------------------------------------------------------------------------------

interface ShareStepProps {
  source: ImportSource
  subdir: string
  shares: Share[] | undefined
  sharesError: unknown
  value: string | null
  onChange: (share: string) => void
  busy: boolean
  setBusy: (busy: boolean) => void
  onStarted: (share: string) => void
}

function ShareStep({ source, subdir, shares, sharesError, value, onChange, busy, setBusy, onStarted }: ShareStepProps) {
  const { t } = useTranslation()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const { ready } = useReadySession()
  const text = useSourceText()
  const [error, setError] = useState<unknown>(null)
  const list = shares ?? []
  const chosen = list.find((s) => s.name === value)
  // The server names it "Import <label>[ <folder>] <date> <time>" with the NAS's clock.
  const folder = t('copy.share.folderPattern', { name: `Import ${[source.label || source.fstype, subdir].filter(Boolean).join(' ')}` })

  const start = async () => {
    if (!chosen || busy) return
    setBusy(true)
    setError(null)
    try {
      await startImportCopy({ uuid: source.uuid, share: chosen.name, subdir })
      setBusy(false)
      onStarted(chosen.name)
    } catch (caught) {
      setBusy(false)
      // A copy into this share is already running: show its progress instead.
      if (caught instanceof ApiError && caught.code === 'apps.job_running') onStarted(chosen.name)
      else setError(caught)
    }
  }

  const message =
    error instanceof ApiError && error.code === 'shares.exists' ? t('copy.share.destExists') : errorMessage(error)

  return (
    <StepLayout
      key="share"
      eyebrow={t('copy.eyebrow')}
      title={t('copy.share.title')}
      intro={t('copy.share.intro', { what: subdir || text.title(source) })}
      onContinue={() => void start()}
      continueLabel={busy ? t('copy.share.starting') : t('copy.share.start')}
      busy={busy}
      continueDisabled={!chosen}
    >
      {shares === undefined && sharesError === undefined ? (
        <InlineLoading />
      ) : list.length === 0 ? (
        <Notice
          status="warn"
          className="max-w-3xl"
          title={t('copy.share.noneTitle')}
          actions={
            <ButtonLink href="/shares" variant="ghost" size="sm" className="-ml-3">
              {t('nav.shares')}
            </ButtonLink>
          }
        >
          {sharesError ? errorMessage(sharesError) : t('copy.share.noneBody')}
        </Notice>
      ) : (
        <ChoiceList
          name="copy-share"
          label={t('copy.share.label')}
          value={value}
          onChange={onChange}
          items={list.map((share) => ({
            id: share.name,
            title: share.name,
            meta: <span className="font-mono text-sm break-all text-muted-foreground">{uncPath(ready.boot.hostname, share.name)}</span>,
            detail: t('copy.share.onPool', { pool: share.pool }),
          }))}
        />
      )}
      <Notice status="info" className="max-w-3xl" title={t('copy.share.newFolder')}>
        <span className="flex flex-col gap-2">
          <span className="font-mono text-sm break-words text-foreground">{folder}</span>
          <span>{t('copy.share.newFolderBody')}</span>
          <span>{t('copy.share.permissions')}</span>
          <span>{t('copy.share.space', { size: format.bytes(source.sizeBytes) })}</span>
        </span>
      </Notice>
      {error !== null && (
        <Notice status="crit" assertive className="max-w-3xl">
          <span className="text-foreground">{message}</span>
        </Notice>
      )}
    </StepLayout>
  )
}

// ---- 4. Progress -------------------------------------------------------------------------------

function ProgressStep({ share, onLeave }: { share: string; onLeave: () => void }) {
  const { t } = useTranslation()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const { ready } = useReadySession()
  const [finished, setFinished] = useState(false)
  const poll = usePolling((signal) => getImportProgress(share, { signal }), JOB_POLL_MS, !finished)
  const progress = poll.data
  const state = progress?.job.state ?? 'running'
  const samples = useRef<ProgressSample[]>([])
  const [ejecting, setEjecting] = useState(false)
  const [ejected, setEjected] = useState(false)
  const [error, setError] = useState<unknown>(null)

  useEffect(() => {
    if (progress && progress.job.state !== 'running') setFinished(true)
  }, [progress])

  const percent = progress && progress.totalBytes > 0 ? Math.min(100, (progress.copiedBytes / progress.totalBytes) * 100) : null
  if (percent !== null && state === 'running') {
    const last = samples.current[samples.current.length - 1]
    if (!last || last.percent !== percent) samples.current = [...samples.current.slice(-30), { at: Date.now(), percent }]
  }
  const secondsLeft = state === 'running' ? estimateSecondsLeft(samples.current) : null
  const destPath = progress?.dest ? `${share}\\${progress.dest}` : share

  const eject = async () => {
    if (!progress?.source) return
    setEjecting(true)
    setError(null)
    try {
      await unmountImport(progress.source)
      setEjected(true)
    } catch (caught) {
      setError(caught)
    } finally {
      setEjecting(false)
    }
  }

  const title =
    state === 'succeeded' ? t('copy.progress.doneTitle') : state === 'failed' ? t('copy.progress.failedTitle') : t('copy.progress.title')

  return (
    <StepLayout
      key="progress"
      eyebrow={t('copy.eyebrow')}
      title={title}
      onContinue={onLeave}
      continueLabel={state === 'running' ? t('copy.progress.leave') : t('copy.progress.openShare')}
      busy={ejecting}
    >
      <p className="max-w-3xl font-mono text-lg break-all">\\{ready.boot.hostname}\{destPath}</p>
      {!progress ? (
        poll.error ? (
          <Notice status="warn" className="max-w-3xl">
            {errorMessage(poll.error)}
          </Notice>
        ) : (
          <InlineLoading />
        )
      ) : (
        <div className="flex max-w-3xl flex-col gap-6">
          <div className="flex flex-col gap-3">
            {percent !== null ? (
              <>
                <p className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1">
                  <span className="text-5xl font-semibold tracking-tighter tabular-nums">{format.number(percent, 1)} %</span>
                  <span className="label-mono text-muted-foreground tabular-nums">
                    {t('copy.progress.ofTotal', { copied: format.bytes(progress.copiedBytes), total: format.bytes(progress.totalBytes) })}
                  </span>
                </p>
                <Meter
                  role="progressbar"
                  value={percent / 100}
                  label={t('copy.progress.label')}
                  valueText={`${format.number(percent, 1)} %`}
                  status={state === 'failed' ? 'crit' : state === 'succeeded' ? 'ok' : 'info'}
                />
              </>
            ) : (
              <p className="text-5xl font-semibold tracking-tighter tabular-nums">{format.bytes(progress.copiedBytes)}</p>
            )}
            <p className="text-muted-foreground tabular-nums">
              {[
                t('copy.progress.files', { count: progress.files, formatted: format.number(progress.files) }),
                state === 'running'
                  ? secondsLeft !== null
                    ? t('repair.progress.left', { time: format.duration(secondsLeft) })
                    : percent !== null
                      ? t('repair.progress.measuring')
                      : null
                  : null,
              ]
                .filter(Boolean)
                .join(' · ')}
            </p>
          </div>

          {state === 'running' && (
            <Notice status="info" title={t('copy.progress.leaveTitle')}>
              {t('copy.progress.leaveBody')}
            </Notice>
          )}
          {state === 'failed' && (
            <div className="flex flex-col gap-3">
              <Notice status="crit">{t('copy.progress.failedBody')}</Notice>
              <JobLog lines={progress.job.log} label={t('copy.progress.log')} />
            </div>
          )}
          {state !== 'running' && progress.source && (
            <div className="flex flex-col items-start gap-3">
              {ejected ? (
                <Notice status="ok" title={t('copy.progress.ejected')}>
                  {t('copy.progress.ejectedBody')}
                </Notice>
              ) : (
                <>
                  <Button variant="secondary" icon={<Icon icon={state === 'succeeded' ? Usb : HardDrive} />} busy={ejecting} onClick={() => void eject()}>
                    {t('copy.progress.eject')}
                  </Button>
                  <p className="text-sm text-muted-foreground">{t('copy.progress.ejectHint')}</p>
                </>
              )}
              {error !== null && (
                <Notice status="crit" assertive>
                  <span className="text-foreground">{errorMessage(error)}</span>
                </Notice>
              )}
            </div>
          )}
        </div>
      )}
    </StepLayout>
  )
}
