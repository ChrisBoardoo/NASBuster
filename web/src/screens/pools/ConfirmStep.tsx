// SPDX-License-Identifier: AGPL-3.0-or-later
// Pool step 2: the destructive confirmation (TrueNAS style). Summary of what will be erased,
// the pool name (validated live), and the name typed again to unlock the button. Nothing is
// sent before that explicit click: Enter never submits this step.
import { type ReactNode, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getDisks, getPools } from '../../api/endpoints'
import type { CreatePoolRequest, Disk } from '../../api/types'
import { useApi } from '../../api/useApi'
import { diskTitle } from '../../components/DiskRow'
import { Field } from '../../components/Field'
import { Notice } from '../../components/Notice'
import { PosterNumber } from '../../components/Typography'
import { POOL_MOUNT_ROOT } from '../../lib/constants'
import { defaultPoolName } from '../../lib/pools'
import { useFormat } from '../../lib/useFormat'
import { usePoolText } from '../../lib/usePoolText'
import { POOL_NAME_MAX, poolNameProblem } from '../../lib/validation'
import { StepLayout } from '../setup/StepLayout'
import { planKey, useWizard } from '../setup/wizard'

export function ConfirmStep() {
  const { t } = useTranslation()
  const text = usePoolText()
  const format = useFormat()
  const { drafts, updateDrafts, goNext, goBack, persistQuietly } = useWizard()
  const draft = drafts.pool
  const plan = draft.plan?.key === planKey(draft.layout, draft.disks) ? draft.plan.value : null
  const lost = !draft.layout || draft.disks.length === 0 || !plan

  // Fresh reads: are the disks still blank, is the name free?
  const disks = useApi((signal) => getDisks({ signal }), [])
  const pools = useApi((signal) => getPools({ signal }), [])
  const [confirmText, setConfirmText] = useState('')
  const [nameTouched, setNameTouched] = useState(false)
  const [busy, setBusy] = useState(false)

  // Default name: the first free one of tank, tank2… until the user edits it.
  const poolList = pools.data?.pools
  useEffect(() => {
    if (!poolList || draft.nameEdited) return
    const name = defaultPoolName(poolList.map((p) => p.name))
    if (name && name !== draft.name) updateDrafts((d) => ({ ...d, pool: { ...d.pool, name } }))
  }, [poolList, draft.nameEdited, draft.name, updateDrafts])

  const byId = useMemo(() => new Map<string, Disk>((disks.data?.disks ?? []).map((d) => [d.id, d])), [disks.data])

  if (lost) {
    return (
      <StepLayout
        eyebrow={t('onboarding.confirm.eyebrow')}
        title={t('onboarding.confirm.title', { count: draft.disks.length })}
        onContinue={() => goBack?.()}
        continueLabel={t('common.back')}
      >
        <Notice status="warn" className="max-w-3xl">
          {t('onboarding.confirm.lost')}
        </Notice>
      </StepLayout>
    )
  }

  const name = draft.name
  const problem = poolNameProblem(name)
  const exists = poolList?.some((p) => p.name === name) ?? false
  const nameError = problem
    ? name.length > 0 || nameTouched
      ? t(`onboarding.confirm.nameErrors.${problem}`)
      : null
    : exists
      ? t('onboarding.confirm.nameErrors.exists', { name })
      : null
  const nameOk = !problem && !exists
  const confirmed = nameOk && confirmText === name
  const mismatch = nameOk && confirmText.length >= name.length && confirmText !== name
  const gone = disks.data ? draft.disks.find((id) => byId.get(id)?.role !== 'available') : undefined
  const canCreate = confirmed && !gone && !busy

  const setName = (value: string) => {
    setNameTouched(true)
    updateDrafts((d) => ({ ...d, pool: { ...d.pool, name: value.toLowerCase(), nameEdited: true } }))
  }

  const create = async () => {
    if (!canCreate || !draft.layout) return
    setBusy(true)
    const request: CreatePoolRequest = { name, layout: draft.layout, disks: [...draft.disks], confirm: confirmText }
    await persistQuietly({ currentStep: 'create', completedStep: 'confirm' })
    updateDrafts((d) => ({
      ...d,
      pool: { ...d.pool, submission: { request, startedAt: Date.now(), outcome: null } },
    }))
    goNext()
  }

  const count = draft.disks.length
  const usable = format.bytesParts(plan.usableBytes)
  const layoutName = draft.layout ?? ''
  const layoutTech = text.layoutTech(layoutName)

  return (
    <StepLayout
      eyebrow={t('onboarding.confirm.eyebrow')}
      title={t('onboarding.confirm.title', { count })}
      intro={t('onboarding.confirm.intro')}
      onContinue={() => void create()}
      continueLabel={busy ? t('onboarding.confirm.submitting') : t('onboarding.confirm.submit')}
      busy={busy}
      continueDisabled={!canCreate}
      enterSubmits={false}
    >
      <div className="grid gap-x-16 gap-y-12 lg:grid-cols-12">
        <section aria-label={t('onboarding.confirm.summaryLabel')} className="min-w-0 lg:col-span-7">
          <dl className="border-b border-border">
            <SummaryRow label={t('onboarding.confirm.protection')}>
              {text.layoutTitle(layoutName)}
              {layoutTech && (
                <span className="label-mono mt-2 block font-normal text-muted-foreground">{layoutTech}</span>
              )}
            </SummaryRow>
            <SummaryRow label={t('onboarding.confirm.usable')}>
              <PosterNumber value={usable.value} unit={usable.unit} className="text-5xl leading-none tracking-tighter" />
            </SummaryRow>
            <SummaryRow label={t('onboarding.confirm.survives')}>{text.survives(plan.tolerates)}</SummaryRow>
            <SummaryRow label={t('onboarding.confirm.disks')}>
              <ul className="flex flex-col gap-4">
                {draft.disks.map((id) => {
                  const disk = byId.get(id)
                  return (
                    <li key={id} className="flex flex-col gap-1">
                      <span className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                        <span className="text-xl tracking-tight">{disk ? diskTitle(disk, t('common.unknown')) : id}</span>
                        {disk && (
                          <span className="font-mono text-sm font-normal text-muted-foreground tabular-nums">
                            {format.bytes(disk.sizeBytes)}
                          </span>
                        )}
                      </span>
                      <span className="font-mono text-xs font-normal break-all text-muted-foreground">{id}</span>
                    </li>
                  )
                })}
              </ul>
            </SummaryRow>
          </dl>
        </section>

        <div className="flex min-w-0 flex-col gap-10 lg:col-span-5">
          <Notice status="crit" title={t('onboarding.confirm.warningTitle', { count })}>
            {t('onboarding.confirm.warningBody')}
          </Notice>
          {gone && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{t('onboarding.confirm.diskGone', { disk: gone })}</span>
            </Notice>
          )}
          {disks.error !== undefined && !disks.data && (
            <Notice status="warn">{t('onboarding.confirm.disksLoadFailed')}</Notice>
          )}

          <Field
            label={t('onboarding.confirm.name')}
            name="pool-name"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            maxLength={POOL_NAME_MAX + 8}
            className="[&_input]:font-mono [&_input]:text-xl"
            value={name}
            onChange={(event) => setName(event.target.value)}
            hint={t('onboarding.confirm.nameHint')}
            error={nameError}
          >
            <div className="mt-4 flex flex-col gap-1 border-l-2 border-accent pl-4">
              <span className="label-mono text-muted-foreground">{t('onboarding.confirm.mountPreview')}</span>
              <span className="font-mono text-xl break-all md:text-2xl" aria-live="polite">
                {POOL_MOUNT_ROOT}/{name || '—'}
              </span>
            </div>
          </Field>

          <ConfirmField
            name={nameOk ? name : null}
            value={confirmText}
            onChange={setConfirmText}
            confirmed={confirmed}
            mismatch={mismatch}
          />
        </div>
      </div>
    </StepLayout>
  )
}

/** Placeholder that can't appear in a translation, replaced by the styled pool name. */
const NAME_MARK = '\u0001'

interface ConfirmFieldProps {
  /** The valid pool name to type, or null while the name itself is invalid. */
  name: string | null
  value: string
  onChange: (value: string) => void
  confirmed: boolean
  mismatch: boolean
}

function ConfirmField({ name, value, onChange, confirmed, mismatch }: ConfirmFieldProps) {
  const { t } = useTranslation()
  // The name is shown as typed (lowercase), even though labels are set in capitals. The
  // sentence is split around a marker so the name can be styled without parsing markup.
  let label: ReactNode = t('onboarding.confirm.confirmLabelEmpty')
  if (name) {
    const [before = '', ...after] = t('onboarding.confirm.confirmLabel', { name: NAME_MARK }).split(NAME_MARK)
    label = (
      <>
        {before}
        <span className="font-semibold tracking-normal text-foreground normal-case">{name}</span>
        {after.join(name)}
      </>
    )
  }

  return (
    <Field
      label={label}
      name="pool-confirm"
      mono
      autoComplete="off"
      autoCapitalize="none"
      autoCorrect="off"
      spellCheck={false}
      disabled={!name}
      className="[&_input]:text-xl"
      value={value}
      onChange={(event) => onChange(event.target.value)}
      hint={confirmed ? undefined : t('onboarding.confirm.confirmHint')}
      success={confirmed ? t('onboarding.confirm.confirmMatch') : null}
      error={mismatch ? t('onboarding.confirm.confirmMismatch') : null}
    />
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
