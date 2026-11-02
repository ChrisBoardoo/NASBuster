// SPDX-License-Identifier: AGPL-3.0-or-later
// Full-screen frame shared by the onboarding and the "new pool" flow: accent progress line,
// wordmark + step counter, oversized decorative number, animated step transitions, and the
// keyboard contract (Enter = Continue outside fields, Esc = Back outside fields).
import { AnimatePresence, m } from 'motion/react'
import { type ReactNode, type RefObject, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { Container } from '../../components/Typography'
import { Wordmark } from '../../components/Wordmark'
import { useReveal } from '../../lib/motion'

const pad = (n: number) => String(n).padStart(2, '0')

function isTypingTarget(target: EventTarget | null): boolean {
  return target instanceof HTMLElement && target.closest('input, textarea, select, [contenteditable="true"]') !== null
}

function isInteractive(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    target.closest('input, textarea, select, button, a[href], summary, [role="option"], [contenteditable="true"]') !== null
  )
}

/**
 * Enter (outside fields and buttons) submits the step form flagged `data-step-form`;
 * Esc (outside fields) goes back. Both are ignored while the step is busy.
 */
export function useWizardKeyboard(goBack: (() => void) | null, busyRef: RefObject<boolean>): void {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || event.altKey || event.ctrlKey || event.metaKey) return
      if (event.key === 'Escape') {
        if (busyRef.current || !goBack || isTypingTarget(event.target)) return
        event.preventDefault()
        goBack()
      } else if (event.key === 'Enter') {
        if (busyRef.current || isInteractive(event.target)) return
        const form = document.querySelector<HTMLFormElement>('form[data-step-form]')
        if (form) {
          event.preventDefault()
          form.requestSubmit()
        }
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [goBack, busyRef])
}

interface WizardFrameProps {
  /** 0-based */
  index: number
  total: number
  stepKey: string
  stepName: string
  /** Controls next to the step counter (language, cancel…). */
  headerActions?: ReactNode
  children: ReactNode
}

export function WizardFrame({ index, total, stepKey, stepName, headerActions, children }: WizardFrameProps) {
  const { t } = useTranslation()
  const { screen, reduced } = useReveal()
  const progress = (index + 1) / total

  return (
    <div className="relative flex min-h-dvh flex-col overflow-x-clip">
      {/* Progress: thin accent line across the top. */}
      <div
        role="progressbar"
        aria-label={t('onboarding.label')}
        aria-valuemin={1}
        aria-valuemax={total}
        aria-valuenow={index + 1}
        aria-valuetext={t('onboarding.progress', { current: index + 1, total, name: stepName })}
        className="fixed inset-x-0 top-0 z-20 h-0.5 bg-border"
      >
        <m.div
          className="h-full origin-left bg-accent"
          initial={false}
          animate={{ scaleX: progress }}
          transition={{ duration: reduced ? 0 : 0.5, ease: [0.25, 0, 0, 1] }}
        />
      </div>

      <Container className="relative z-10 flex min-h-16 items-center justify-between gap-4 md:min-h-20">
        <Wordmark />
        <div className="flex items-center gap-4 md:gap-6">
          {headerActions}
          <p className="font-mono text-sm tracking-widest whitespace-nowrap" aria-hidden="true">
            <span className="text-foreground">{pad(index + 1)}</span>
            <span className="text-muted-foreground"> / {pad(total)}</span>
          </p>
        </div>
      </Container>

      {/* Decorative oversized step number, behind the content (hidden on small screens). */}
      <span
        aria-hidden="true"
        className="pointer-events-none absolute right-0 bottom-16 hidden text-[16rem] leading-none font-semibold tracking-tighter text-muted opacity-60 select-none md:block lg:text-[22rem]"
      >
        {pad(index + 1)}
      </span>

      <main className="relative flex flex-1 flex-col">
        <AnimatePresence mode="wait">
          <m.div
            key={stepKey}
            variants={screen}
            initial="hidden"
            animate="show"
            exit="exit"
            className="flex flex-1 flex-col"
          >
            {children}
          </m.div>
        </AnimatePresence>
      </main>
    </div>
  )
}
