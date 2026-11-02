// SPDX-License-Identifier: AGPL-3.0-or-later
// /storage/pools/:name/replace — replace a disk, one decision per screen (ADR-0012):
//   1. which member (the missing or failing one is preselected);
//   2. which new disk (blank and big enough; a pre-formatted one can be erased here);
//   3. summary, typed pool name, then POST /storage/pools/{name}/replace (202).
// The rebuild runs in the background; the Storage page shows its progress.
import { Eraser, X } from 'lucide-react'
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation } from 'wouter'
import { getDisks, getPool, replaceDisk } from '../../api/endpoints'
import type { Disk, Pool } from '../../api/types'
import { useApi } from '../../api/useApi'
import { Button } from '../../components/Button'
import { Icon } from '../../components/Icon'
import { InlineLoading } from '../../components/Loading'
import { LocateDisk } from '../../components/LocateDisk'
import { Notice } from '../../components/Notice'
import { ConfirmName } from '../../components/ConfirmName'
import { StatusLabel } from '../../components/StatusLabel'
import { ChoiceList } from '../../components/ChoiceList'
import { WipePanel } from '../../components/WipePanel'
import { diskTitle } from '../../lib/disks'
import { useDocumentTitle } from '../../lib/hooks'
import { useDynamicT, useErrorMessage } from '../../lib/messages'
import {
  type MemberView,
  isRebuilding,
  memberLabel,
  memberViews,
  rememberRebuild,
  requiredBytes,
  usableOnDisk,
} from '../../lib/repair'
import { useFormat } from '../../lib/useFormat'
import { usePoolText } from '../../lib/usePoolText'
import { StepLayout } from '../setup/StepLayout'
import {
  EMPTY_ACCOUNT_DRAFT,
  EMPTY_POOL_DRAFT,
  EMPTY_SAFETY_DRAFT,
  EMPTY_SHARE_DRAFT,
  type UiStep,
  type WizardApi,
  WizardContext,
  useWizard,
} from '../setup/wizard'
import { WizardFrame, useWizardKeyboard } from '../setup/WizardFrame'

const STEPS = ['member', 'disk', 'confirm'] as const
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

export default function ReplaceWizard({ name }: { name: string }) {
  const { t } = useTranslation()
  const [, navigate] = useLocation()
  const poolState = useApi((signal) => getPool(name, { signal }), [name])
  const disks = useApi((signal) => getDisks({ signal }), [])
  const [index, setIndex] = useState(0)
  const [busy, setBusyState] = useState(false)
  const busyRef = useRef(false)
  const [oldId, setOldId] = useState<string | null>(null)
  const [newId, setNewId] = useState<string | null>(null)

  const step: Step = STEPS[index] ?? 'member'
  useDocumentTitle(t(`replace.steps.${step}`))
  const back = `/storage#pool-${name}`
  const leave = useCallback(() => navigate(back), [navigate, back])
  const goBack = useMemo(() => (index === 0 ? leave : () => setIndex((i) => i - 1)), [index, leave])
  const setBusy = useCallback((value: boolean) => {
    busyRef.current = value
    setBusyState(value)
  }, [])
  useWizardKeyboard(goBack, busyRef)

  const pool = poolState.data
  const members = useMemo(() => (pool ? memberViews(pool, disks.data?.disks) : []), [pool, disks.data])

  // Preselect the member that needs replacing (missing first, then failing).
  useEffect(() => {
    if (oldId || members.length === 0 || !disks.data) return
    const pick = members.find((m) => m.state === 'missing') ?? members.find((m) => m.state === 'failing')
    if (pick) setOldId(pick.id)
  }, [members, oldId, disks.data])

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

  const old = members.find((m) => m.id === oldId)
  const candidate = disks.data?.disks.find((d) => d.id === newId)

  let content: ReactNode
  if ((poolState.loading && !pool) || (disks.loading && !disks.data)) {
    content = <Loading />
  } else if (!pool || !pool.status) {
    content = <Blocked title={t('replace.unavailable')} body={pool?.statusError ?? ''} error={poolState.error} onLeave={leave} />
  } else if (!pool.status.mounted) {
    content = <Blocked title={t('replace.notMountedTitle')} body={t('replace.notMountedBody')} onLeave={leave} />
  } else if (isRebuilding(pool.status) || pool.status.balance) {
    content = <Blocked title={t('replace.busyTitle')} body={t('replace.busyBody')} onLeave={leave} />
  } else if (step === 'member') {
    content = <MemberStep pool={pool} members={members} value={oldId} onChange={setOldId} />
  } else if (step === 'disk' && old) {
    content = (
      <DiskStep
        pool={pool}
        old={old}
        disks={disks.data?.disks ?? []}
        value={newId}
        onChange={setNewId}
        onWiped={disks.reload}
        refreshing={disks.loading}
      />
    )
  } else if (step === 'confirm' && old && candidate) {
    content = (
      <ConfirmStep
        pool={pool}
        old={old}
        candidate={candidate}
        busy={busy}
        onDone={() => {
          rememberRebuild(pool.name, candidate.id)
          navigate(back)
        }}
      />
    )
  } else {
    content = <MemberStep pool={pool} members={members} value={oldId} onChange={setOldId} />
  }

  return (
    <WizardContext.Provider value={api}>
      <WizardFrame
        index={index}
        total={STEPS.length}
        stepKey={step}
        stepName={t(`replace.steps.${step}`)}
        headerActions={
          <Button variant="ghost" size="sm" className="-mr-3 min-w-11" icon={<Icon icon={X} size={18} />} onClick={leave} disabled={busy}>
            <span className="sr-only sm:not-sr-only">{t('common.cancel')}</span>
          </Button>
        }
      >
        {content}
      </WizardFrame>
    </WizardContext.Provider>
  )
}

function Loading() {
  const { t } = useTranslation()
  return (
    <StepLayout eyebrow={t('replace.eyebrow')} title={t('replace.member.title')} onContinue={noop} continueLabel={t('common.continue')} continueDisabled>
      <InlineLoading />
    </StepLayout>
  )
}

function Blocked({ title, body, error, onLeave }: { title: string; body: string; error?: unknown; onLeave: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  return (
    <StepLayout eyebrow={t('replace.eyebrow')} title={title} intro={body || undefined} onContinue={onLeave} continueLabel={t('replace.backToStorage')}>
      {error !== undefined && error !== null && <Notice status="warn">{errorMessage(error)}</Notice>}
    </StepLayout>
  )
}

// ---- 1. Which member --------------------------------------------------------------------

function MemberStep({ pool, members, value, onChange }: { pool: Pool; members: MemberView[]; value: string | null; onChange: (id: string) => void }) {
  const { t } = useTranslation()
  const { goNext } = useWizard()
  const parity = Boolean(pool.status?.md)

  return (
    <StepLayout
      key="member"
      eyebrow={t('replace.eyebrow')}
      title={t('replace.member.title')}
      intro={t('replace.member.intro', { pool: pool.name })}
      onContinue={goNext}
      continueLabel={t('common.continue')}
      continueDisabled={!value}
    >
      <ChoiceList
        name="replace-choice"
        label={t('replace.member.label')}
        items={members.map((member) => {
          // Parity: only a missing or failing member can be replaced (409 storage.member_healthy).
          const blocked = parity && (member.state === 'ok' || member.state === 'present')
          return {
            id: member.id,
            title: memberLabel(member, t('common.unknown')),
            meta: <MemberState member={member} />,
            detail: blocked
              ? t('replace.member.healthyParity')
              : member.state === 'ok'
                ? t('replace.member.preventive')
                : member.state === 'missing'
                  ? t('replace.member.missingHint')
                  : null,
            disabled: blocked,
            // A disk still connected can blink, to find the one to pull among identical ones.
            action: member.state !== 'missing' ? <LocateDisk diskId={member.id} /> : undefined,
          }
        })}
        value={value}
        onChange={onChange}
      />
    </StepLayout>
  )
}

function MemberState({ member }: { member: MemberView }) {
  const { t } = useTranslation()
  const status = member.state === 'ok' ? 'ok' : member.state === 'missing' || member.state === 'failing' ? 'crit' : 'info'
  return <StatusLabel status={status} label={t(`repair.member.${member.state}`)} />
}

// ---- 2. Which new disk ----------------------------------------------------------------------

function DiskStep({
  pool,
  old,
  disks,
  value,
  onChange,
  onWiped,
  refreshing,
}: {
  pool: Pool
  old: MemberView
  disks: readonly Disk[]
  value: string | null
  onChange: (id: string) => void
  onWiped: () => void
  refreshing: boolean
}) {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()
  const { goNext } = useWizard()
  const [wiping, setWiping] = useState<string | null>(null)
  const required = requiredBytes(old)
  const memberIds = new Set(pool.members)
  const candidates = disks.filter((disk) => !memberIds.has(disk.id) && disk.role !== 'system' && disk.role !== 'protected')
  const fits = (disk: Disk) => required === null || usableOnDisk(disk.sizeBytes) >= required
  const selectable = candidates.filter((disk) => disk.role === 'available' && fits(disk))
  const wipeTarget = candidates.find((disk) => disk.id === wiping)

  return (
    <StepLayout
      key="disk"
      eyebrow={t('replace.eyebrow')}
      title={t('replace.disk.title')}
      intro={required !== null ? t('replace.disk.introSize', { size: format.bytes(required) }) : t('replace.disk.intro')}
      onContinue={goNext}
      continueLabel={t('common.continue')}
      continueDisabled={!value || !selectable.some((d) => d.id === value)}
    >
      {selectable.length === 0 && (
        <Notice status="warn" title={t('replace.disk.noneTitle')} className="max-w-3xl">
          {required !== null ? t('replace.disk.noneBodySize', { size: format.bytes(required) }) : t('replace.disk.noneBody')}
        </Notice>
      )}
      {wipeTarget ? (
        <WipePanel
          disk={wipeTarget}
          onCancel={() => setWiping(null)}
          onWiped={() => {
            setWiping(null)
            onWiped()
          }}
        />
      ) : (
        <ChoiceList
          name="replace-choice"
          label={t('replace.disk.label')}
          items={candidates.map((disk) => {
            const blank = disk.role === 'available'
            const big = fits(disk)
            const roleKey = `disks.roleHint.${disk.role}`
            return {
              id: disk.id,
              title: diskTitle(disk, t('common.unknown')),
              size: format.bytesParts(disk.sizeBytes),
              meta: <span className="font-mono text-xs break-all text-muted-foreground">{disk.id}</span>,
              detail: !blank
                ? has(roleKey)
                  ? td(roleKey)
                  : disk.role
                : !big && required !== null
                  ? t('replace.disk.tooSmall', { size: format.bytes(required) })
                  : null,
              disabled: !blank || !big,
              action:
                // A mounted disk would only be refused: no point offering to erase it here.
                disk.role === 'in_use' && !disk.usage.some((u) => u.kind === 'mounted') ? (
                  <Button variant="ghost" size="sm" className="-ml-3" icon={<Icon icon={Eraser} />} onClick={() => setWiping(disk.id)}>
                    {t('wipe.offer')}
                  </Button>
                ) : null,
            }
          })}
          value={value}
          onChange={onChange}
        />
      )}
      {refreshing && <InlineLoading />}
    </StepLayout>
  )
}

// ---- 3. Confirm -------------------------------------------------------------------------------

function ConfirmStep({ pool, old, candidate, busy, onDone }: { pool: Pool; old: MemberView; candidate: Disk; busy: boolean; onDone: () => void }) {
  const { t } = useTranslation()
  const format = useFormat()
  const text = usePoolText()
  const errorMessage = useErrorMessage()
  const { setBusy } = useWizard()
  const [typed, setTyped] = useState('')
  const [error, setError] = useState<unknown>(null)
  const confirmed = typed === pool.name

  const start = async () => {
    if (!confirmed) return
    setBusy(true)
    setError(null)
    try {
      await replaceDisk(pool.name, { old: old.id, new: candidate.id, confirm: typed })
      setBusy(false)
      onDone()
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <StepLayout
      key="confirm"
      eyebrow={t('replace.eyebrow')}
      title={t('replace.confirm.title')}
      intro={t('replace.confirm.intro')}
      onContinue={() => void start()}
      continueLabel={busy ? t('replace.confirm.starting') : t('replace.confirm.start')}
      busy={busy}
      continueDisabled={!confirmed}
      enterSubmits={false}
    >
      <div className="grid max-w-6xl gap-x-16 gap-y-10 lg:grid-cols-12">
        <dl className="border-b border-border lg:col-span-7">
          <SummaryRow label={t('replace.confirm.pool')}>
            {pool.name}
            <span className="label-mono ml-3 font-normal text-muted-foreground">{text.layoutTitle(pool.layout)}</span>
          </SummaryRow>
          <SummaryRow label={t('replace.confirm.old')}>
            <span className="flex flex-col gap-1">
              <span>{memberLabel(old, t('common.unknown'))}</span>
              <MemberState member={old} />
            </span>
          </SummaryRow>
          <SummaryRow label={t('replace.confirm.new')}>
            <span className="flex flex-col gap-1">
              <span>
                {diskTitle(candidate, t('common.unknown'))}{' '}
                <span className="font-mono text-base font-normal text-muted-foreground">{format.bytes(candidate.sizeBytes)}</span>
              </span>
              <span className="font-mono text-xs font-normal break-all text-muted-foreground">{candidate.id}</span>
            </span>
          </SummaryRow>
        </dl>
        <div className="flex flex-col gap-8 lg:col-span-5">
          <Notice status="crit" title={t('replace.confirm.eraseTitle')}>
            {t('replace.confirm.eraseBody')}
          </Notice>
          <p className="text-muted-foreground">{t('replace.confirm.after')}</p>
          <ConfirmName name={pool.name} value={typed} onChange={setTyped} />
        </div>
      </div>
      {error !== null && (
        <Notice status="crit" assertive className="max-w-3xl">
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
    </StepLayout>
  )
}

function SummaryRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-2 border-t border-border py-5 md:grid-cols-12 md:gap-6">
      <dt className="label-mono pt-2 text-muted-foreground md:col-span-4">{label}</dt>
      <dd className="min-w-0 text-2xl font-semibold tracking-tight break-words md:col-span-8">{children}</dd>
    </div>
  )
}

// ---- Shared radio list ----------------------------------------------------------------------

