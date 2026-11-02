// SPDX-License-Identifier: AGPL-3.0-or-later
// Onboarding "à la Wispr Flow": full screen, one decision per step, visible progress,
// resumable (server-side progress from the account step on), keyboard friendly.
import { type ComponentType, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../../api/client'
import { putOnboarding } from '../../api/endpoints'
import { LanguageToggle } from '../../components/Toggles'
import { useDocumentTitle } from '../../lib/hooks'
import { hostnameProblem } from '../../lib/validation'
import { useReadySession } from '../../session/SessionContext'
import { ConfirmStep } from '../pools/ConfirmStep'
import { CreateStep } from '../pools/CreateStep'
import { ProtectionStep } from '../pools/ProtectionStep'
import { AccountStep } from './AccountStep'
import { AppsStep } from './AppsStep'
import { DisksStep } from './DisksStep'
import { DoneStep } from './DoneStep'
import { HealthStep } from './HealthStep'
import { IdentityStep } from './IdentityStep'
import { SafetyStep } from './SafetyStep'
import { ShareStep } from './ShareStep'
import { WelcomeStep } from './WelcomeStep'
import {
  EMPTY_ACCOUNT_DRAFT,
  EMPTY_POOL_DRAFT,
  EMPTY_SAFETY_DRAFT,
  EMPTY_SHARE_DRAFT,
  type ProgressUpdate,
  UI_STEPS,
  type UiStep,
  type WizardApi,
  WizardContext,
  type WizardDrafts,
  mergeCompleted,
  resumeStep,
} from './wizard'
import { WizardFrame, useWizardKeyboard } from './WizardFrame'

const STEP_COMPONENTS: Record<UiStep, ComponentType> = {
  welcome: WelcomeStep,
  health: HealthStep,
  account: AccountStep,
  identity: IdentityStep,
  disks: DisksStep,
  protection: ProtectionStep,
  confirm: ConfirmStep,
  create: CreateStep,
  share: ShareStep,
  apps: AppsStep,
  safety: SafetyStep,
  done: DoneStep,
}

export default function SetupWizard() {
  const { t } = useTranslation()
  const { ready, setOnboarding } = useReadySession()

  const [index, setIndex] = useState(() => UI_STEPS.indexOf(resumeStep(ready.onboarding, ready.boot.needsSetup)))
  const [persistFailed, setPersistFailed] = useState(false)
  const busyRef = useRef(false)
  const [drafts, setDrafts] = useState<WizardDrafts>(() => {
    const current = ready.boot.hostname.toLowerCase()
    return {
      account: EMPTY_ACCOUNT_DRAFT,
      identity: { hostname: hostnameProblem(current) ? '' : current, timezone: '' },
      checks: null,
      pool: EMPTY_POOL_DRAFT,
      share: EMPTY_SHARE_DRAFT,
      protection: null,
      safety: EMPTY_SAFETY_DRAFT,
    }
  })

  const step = UI_STEPS[index] ?? 'welcome'
  const total = UI_STEPS.length
  useDocumentTitle(t(`onboarding.steps.${step}`))

  // A step with sub-screens (share) may own Back while it isn't on its first one.
  const [localBack, setLocalBackState] = useState<{ back: () => void } | null>(null)
  const setLocalBack = useCallback((back: (() => void) | null) => setLocalBackState(back ? { back } : null), [])
  const goNext = useCallback(() => {
    setLocalBackState(null)
    setIndex((i) => Math.min(UI_STEPS.length - 1, i + 1))
  }, [])
  const goTo = useCallback((target: UiStep) => {
    setLocalBackState(null)
    setIndex(UI_STEPS.indexOf(target))
  }, [])

  // Back is the previous screen, except: nothing goes back into a creation (it is sent once),
  // and the share step returns to the pool result, or to the protection choice if skipped.
  const created = drafts.pool.submission?.outcome?.kind === 'created'
  const backTarget: UiStep | null =
    step === 'welcome' || step === 'create'
      ? null
      : step === 'share'
        ? created
          ? 'create'
          : 'protection'
        : (UI_STEPS[index - 1] ?? null)
  const goBack = useMemo(
    () => localBack?.back ?? (backTarget ? () => goTo(backTarget) : null),
    [localBack, backTarget, goTo],
  )

  const authenticated = ready.boot.authenticated
  const onboardingRef = useRef(ready.onboarding)
  useEffect(() => {
    onboardingRef.current = ready.onboarding
  }, [ready.onboarding])

  const persist = useCallback(
    async ({ currentStep, completedStep, completed = false }: ProgressUpdate) => {
      if (!authenticated) return
      const base = onboardingRef.current?.completedSteps ?? []
      const saved = await putOnboarding({
        completed,
        currentStep,
        completedSteps: mergeCompleted(base, completedStep),
      })
      onboardingRef.current = saved
      setOnboarding(saved)
      setPersistFailed(false)
    },
    [authenticated, setOnboarding],
  )

  const persistQuietly = useCallback(
    async (update: ProgressUpdate) => {
      try {
        await persist(update)
      } catch (error) {
        // An expired session is handled globally (back to login); anything else is non-blocking.
        if (!(error instanceof ApiError && error.code === 'auth.required')) setPersistFailed(true)
      }
    },
    [persist],
  )

  const setBusy = useCallback((busy: boolean) => {
    busyRef.current = busy
  }, [])

  const updateDrafts = useCallback((update: (d: WizardDrafts) => WizardDrafts) => setDrafts(update), [])

  const skipPool = useCallback(() => {
    void persistQuietly({ currentStep: 'share' })
    goTo('share')
  }, [persistQuietly, goTo])

  useWizardKeyboard(goBack, busyRef)

  const api = useMemo<WizardApi>(
    () => ({
      mode: 'onboarding',
      step,
      goNext,
      goBack,
      goTo,
      drafts,
      updateDrafts,
      persist,
      persistQuietly,
      persistFailed,
      setBusy,
      skipPool,
      finishPool: goNext,
      setLocalBack,
    }),
    [step, goNext, goBack, goTo, drafts, updateDrafts, persist, persistQuietly, persistFailed, setBusy, skipPool, setLocalBack],
  )

  const StepComponent = STEP_COMPONENTS[step]

  return (
    <WizardContext.Provider value={api}>
      <WizardFrame
        index={index}
        total={total}
        stepKey={step}
        stepName={t(`onboarding.steps.${step}`)}
        headerActions={step !== 'welcome' ? <LanguageToggle /> : undefined}
      >
        <StepComponent />
      </WizardFrame>
    </WizardContext.Provider>
  )
}
