// SPDX-License-Identifier: AGPL-3.0-or-later
// /storage/pools/new: the onboarding's pool steps (protection → confirm → create) on their
// own, full screen, outside the app shell. Leaves to /storage at the end or on cancel.
import { X } from 'lucide-react'
import { type ComponentType, useCallback, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation } from 'wouter'
import { Button } from '../../components/Button'
import { Icon } from '../../components/Icon'
import { useDocumentTitle } from '../../lib/hooks'
import {
  EMPTY_ACCOUNT_DRAFT,
  EMPTY_POOL_DRAFT,
  EMPTY_SAFETY_DRAFT,
  EMPTY_SHARE_DRAFT,
  POOL_STEPS,
  type UiStep,
  type WizardApi,
  WizardContext,
  type WizardDrafts,
} from '../setup/wizard'
import { WizardFrame, useWizardKeyboard } from '../setup/WizardFrame'
import { ConfirmStep } from './ConfirmStep'
import { CreateStep } from './CreateStep'
import { ProtectionStep } from './ProtectionStep'

type PoolStep = (typeof POOL_STEPS)[number]

const STEP_COMPONENTS: Record<PoolStep, ComponentType> = {
  protection: ProtectionStep,
  confirm: ConfirmStep,
  create: CreateStep,
}

const noPersist = async () => undefined
// The pool steps have no sub-screens of their own.
const noLocalBack = () => undefined

export default function PoolWizard() {
  const { t } = useTranslation()
  const [, navigate] = useLocation()
  const [index, setIndex] = useState(0)
  const [busy, setBusyState] = useState(false)
  const busyRef = useRef(false)
  const [drafts, setDrafts] = useState<WizardDrafts>(() => ({
    account: EMPTY_ACCOUNT_DRAFT,
    identity: { hostname: '', timezone: '' },
    checks: null,
    pool: EMPTY_POOL_DRAFT,
    share: EMPTY_SHARE_DRAFT,
    protection: null,
    safety: EMPTY_SAFETY_DRAFT,
  }))

  const step: PoolStep = POOL_STEPS[index] ?? 'protection'
  useDocumentTitle(t(`onboarding.steps.${step}`))

  const leave = useCallback(() => navigate('/storage'), [navigate])
  const goNext = useCallback(() => setIndex((i) => Math.min(POOL_STEPS.length - 1, i + 1)), [])
  const goTo = useCallback((target: UiStep) => {
    const next = (POOL_STEPS as readonly UiStep[]).indexOf(target)
    if (next >= 0) setIndex(next)
  }, [])
  // Back from the first step returns to Storage; nothing goes back into a creation.
  const goBack = useMemo(
    () => (step === 'create' ? null : index === 0 ? leave : () => setIndex((i) => Math.max(0, i - 1))),
    [step, index, leave],
  )

  const setBusy = useCallback((value: boolean) => {
    busyRef.current = value
    setBusyState(value)
  }, [])
  const updateDrafts = useCallback((update: (d: WizardDrafts) => WizardDrafts) => setDrafts(update), [])

  useWizardKeyboard(goBack, busyRef)

  const api = useMemo<WizardApi>(
    () => ({
      mode: 'standalone',
      step,
      goNext,
      goBack,
      goTo,
      drafts,
      updateDrafts,
      persist: noPersist,
      persistQuietly: noPersist,
      persistFailed: false,
      setBusy,
      skipPool: leave,
      finishPool: leave,
      setLocalBack: noLocalBack,
    }),
    [step, goNext, goBack, goTo, drafts, updateDrafts, setBusy, leave],
  )

  const StepComponent = STEP_COMPONENTS[step]
  // Leaving is locked while the creation runs (and after it: the result screen leads out).
  const canCancel = !busy && step !== 'create'

  return (
    <WizardContext.Provider value={api}>
      <WizardFrame
        index={index}
        total={POOL_STEPS.length}
        stepKey={step}
        stepName={t(`onboarding.steps.${step}`)}
        headerActions={
          // Icon only on small screens; the word stays available to assistive tech.
          <Button
            variant="ghost"
            size="sm"
            className="-mr-3 min-w-11"
            icon={<Icon icon={X} size={18} />}
            onClick={leave}
            disabled={!canCancel}
          >
            <span className="sr-only sm:not-sr-only">{t('common.cancel')}</span>
          </Button>
        }
      >
        <StepComponent />
      </WizardFrame>
    </WizardContext.Provider>
  )
}
