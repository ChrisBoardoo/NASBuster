// SPDX-License-Identifier: AGPL-3.0-or-later
// Shared state of the onboarding wizard, and of the standalone "new pool" flow that reuses
// its pool steps (protection → confirm → create).
import { createContext, useContext } from 'react'
import type { ApiError } from '../../api/client'
import {
  ONBOARDING_STEPS,
  type ChecksResponse,
  type CreatePoolRequest,
  type Onboarding,
  type OnboardingStep,
  type Pool,
  type PoolPlan,
  type Protection,
} from '../../api/types'
import { DEFAULT_POOL_NAME } from '../../lib/constants'

/** Screens of the onboarding, named after the server's step ids; `done` is the closing screen. */
export const UI_STEPS = [
  'welcome',
  'health',
  'account',
  'identity',
  'disks',
  'protection',
  'confirm',
  'create',
  'share',
  'apps',
  'safety',
  'done',
] as const
export type UiStep = (typeof UI_STEPS)[number]

/** The pool steps, also used on their own at /storage/pools/new. */
export const POOL_STEPS = ['protection', 'confirm', 'create'] as const satisfies readonly UiStep[]

export interface AccountDraft {
  /** How ownership is proven (ADR-0018): the Pi's own account, or the setup code. */
  proof: 'machine' | 'code'
  /** The account created in Raspberry Pi Imager. Kept in memory only, never stored. */
  machineUser: string
  machinePassword: string
  setupCode: string
  username: string
  password: string
  confirm: string
}

export const EMPTY_ACCOUNT_DRAFT: AccountDraft = {
  proof: 'machine',
  machineUser: '',
  machinePassword: '',
  setupCode: '',
  username: '',
  password: '',
  confirm: '',
}

export interface IdentityDraft {
  hostname: string
  timezone: string
}

export type PoolOutcome =
  | { kind: 'created'; pool: Pool }
  /** The server answered with an error: we know nothing (more) was created by this request. */
  | { kind: 'failed'; error: ApiError }
  /** No answer (connection lost, page reloaded): the pool may exist anyway. */
  | { kind: 'unknown'; error: unknown }

/**
 * One confirmed creation. The create step sends `request` exactly once (never retried);
 * the outcome lives here so Back / Continue never re-sends it.
 */
export interface PoolSubmission {
  /** null when the wizard resumed at `create` after a reload (the request is unknown). */
  request: CreatePoolRequest | null
  /** Date.now() when the user confirmed. */
  startedAt: number
  outcome: PoolOutcome | null
}

export interface PoolDraft {
  layout: string | null
  disks: string[]
  name: string
  /** Once edited, the name no longer follows the default (tank, tank2…). */
  nameEdited: boolean
  /** Latest capacity preview, with the selection it was computed for. */
  plan: { key: string; value: PoolPlan } | null
  submission: PoolSubmission | null
}

/** Lifted out of the steps so Back / Continue never loses what was typed. */
/** The share step: first NAS user, first share, then the live connection test. */
export interface ShareDraft {
  /** Sub-screen of the share step. */
  screen: 'user' | 'share' | 'connect'
  userName: string
  password: string
  confirm: string
  /** NAS user created (or picked) during this step. */
  user: string | null
  shareName: string
  pool: string
  mode: 'rw' | 'ro'
  /** Recycle bin on the new share (on by default). */
  recycle: boolean
  /** Share created (or picked) during this step. */
  share: string | null
}

export const EMPTY_SHARE_DRAFT: ShareDraft = {
  screen: 'user',
  userName: '',
  password: '',
  confirm: '',
  user: null,
  shareName: 'documents',
  pool: '',
  mode: 'rw',
  recycle: true,
  share: null,
}

/** The safety step: protection settings, then (optional) alerts on a phone. */
export interface SafetyDraft {
  screen: 'protection' | 'phone'
  /** Random ntfy topic URL offered on the phone screen ("" until generated). */
  topicUrl: string
}

export const EMPTY_SAFETY_DRAFT: SafetyDraft = { screen: 'protection', topicUrl: '' }

export interface WizardDrafts {
  account: AccountDraft
  identity: IdentityDraft
  checks: ChecksResponse | null
  pool: PoolDraft
  share: ShareDraft
  /** Protection settings as edited on the safety step (null until loaded). */
  protection: Protection | null
  safety: SafetyDraft
}

export const EMPTY_POOL_DRAFT: PoolDraft = {
  layout: null,
  disks: [],
  name: DEFAULT_POOL_NAME,
  nameEdited: false,
  plan: null,
  submission: null,
}

/** Identifies a selection, to match a capacity preview with what's on screen. */
export function planKey(layout: string | null, disks: readonly string[]): string {
  return `${layout ?? ''}|${disks.join(',')}`
}

export interface ProgressUpdate {
  currentStep: OnboardingStep
  /** Step just finished (skipped steps are not marked completed). */
  completedStep?: OnboardingStep
  completed?: boolean
}

export interface WizardApi {
  /** `onboarding` inside the setup wizard, `standalone` at /storage/pools/new. */
  mode: 'onboarding' | 'standalone'
  step: UiStep
  goNext: () => void
  goBack: (() => void) | null
  goTo: (step: UiStep) => void
  drafts: WizardDrafts
  updateDrafts: (update: (drafts: WizardDrafts) => WizardDrafts) => void
  /** Saves progress server-side (no-op before the account exists). Throws on failure. */
  persist: (update: ProgressUpdate) => Promise<void>
  /** Saves progress, but never blocks the user: failures only show a discreet note. */
  persistQuietly: (update: ProgressUpdate) => Promise<void>
  persistFailed: boolean
  setBusy: (busy: boolean) => void
  /** Leaves the pool steps without creating anything ("Skip for now" / back to Storage). */
  skipPool: () => void
  /** Leaves the pool steps after a successful creation. */
  finishPool: () => void
  /**
   * Lets a step with sub-screens own Back (button and Esc) while it is not on its first
   * sub-screen; null gives Back to the wizard again.
   */
  setLocalBack: (back: (() => void) | null) => void
}

export const WizardContext = createContext<WizardApi | null>(null)

export function useWizard(): WizardApi {
  const wizard = useContext(WizardContext)
  if (!wizard) throw new Error('useWizard must be used inside the setup wizard')
  return wizard
}

/**
 * Where to resume after a reload:
 * - never before `identity` once the account exists;
 * - `confirm` → `protection` (the selection lived in the closed tab);
 * - `create` → `create`, which then only *checks* what happened (it never re-sends).
 */
export function resumeStep(onboarding: Onboarding | null, needsSetup: boolean): UiStep {
  if (needsSetup || !onboarding) return 'welcome'
  const step = onboarding.currentStep
  if (step === 'share' || step === 'apps' || step === 'safety' || step === 'done') return step
  if (step === 'create') return 'create'
  if (step === 'confirm' || step === 'protection') return 'protection'
  const uiIndex = Math.max(UI_STEPS.indexOf(step as UiStep), UI_STEPS.indexOf('identity'))
  return UI_STEPS[uiIndex] ?? 'identity'
}

/** completedSteps in canonical order, without duplicates. */
export function mergeCompleted(
  current: readonly OnboardingStep[],
  extra: OnboardingStep | undefined,
): OnboardingStep[] {
  const set = new Set<OnboardingStep>(current)
  if (extra) set.add(extra)
  return ONBOARDING_STEPS.filter((step) => set.has(step))
}
