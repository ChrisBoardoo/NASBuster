// SPDX-License-Identifier: AGPL-3.0-or-later
// One wizard screen: eyebrow, poster title, intro, content, then a sticky Back / Continue bar.
// The whole step is a <form>: Enter in a field submits it, and the wizard maps Enter
// (outside fields) to the same submit, unless the step opts out (destructive confirmation).
import { ArrowLeft, ArrowRight } from 'lucide-react'
import { m } from 'motion/react'
import { type FormEvent, type KeyboardEvent, type ReactNode, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '../../components/Button'
import { Icon } from '../../components/Icon'
import { Container, Eyebrow } from '../../components/Typography'
import { cn } from '../../lib/cn'
import { useReveal } from '../../lib/motion'
import { useWizard } from './wizard'

interface StepLayoutProps {
  eyebrow: string
  title: ReactNode
  titleClassName?: string
  intro?: ReactNode
  children?: ReactNode
  onContinue: () => void
  continueLabel: string
  busy?: boolean
  /** Extra actions next to Continue (e.g. "Skip for now"). */
  secondaryAction?: ReactNode
  /** Continue can't be used yet (the reason must be visible on screen). */
  continueDisabled?: boolean
  /**
   * false: Enter never triggers Continue, neither in a field nor outside: only an explicit
   * click (or Space/Enter on the focused button) does. For destructive steps.
   */
  enterSubmits?: boolean
}

export function StepLayout({
  eyebrow,
  title,
  titleClassName,
  intro,
  children,
  onContinue,
  continueLabel,
  busy = false,
  secondaryAction,
  continueDisabled = false,
  enterSubmits = true,
}: StepLayoutProps) {
  const { t } = useTranslation()
  const { goBack, persistFailed, setBusy } = useWizard()
  const { item } = useReveal()
  const formRef = useRef<HTMLFormElement>(null)
  const headingRef = useRef<HTMLHeadingElement>(null)

  useEffect(() => {
    setBusy(busy)
  }, [busy, setBusy])

  // Focus the first field flagged data-autofocus, else the heading (keeps Enter = Continue).
  useEffect(() => {
    const target = formRef.current?.querySelector<HTMLElement>('[data-autofocus]') ?? headingRef.current
    target?.focus({ preventScroll: true })
  }, [])

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!busy && !continueDisabled) onContinue()
  }

  // Blocks the browser's implicit submission (Enter in a text field) when Enter must not submit.
  const blockEnter = (event: KeyboardEvent<HTMLFormElement>) => {
    if (event.key === 'Enter' && event.target instanceof HTMLInputElement) event.preventDefault()
  }

  const hint = !goBack
    ? enterSubmits && !busy
      ? t('onboarding.keyboardHintFirst')
      : null
    : enterSubmits
      ? t('onboarding.keyboardHint')
      : t('onboarding.keyboardHintBackOnly')

  return (
    <form
      ref={formRef}
      data-step-form={enterSubmits ? '' : undefined}
      onSubmit={submit}
      onKeyDown={enterSubmits ? undefined : blockEnter}
      noValidate
      className="flex flex-1 flex-col"
    >
      <Container className="relative flex flex-1 flex-col justify-center gap-8 py-10 md:gap-10 md:py-16">
        <m.div variants={item}>
          <Eyebrow>{eyebrow}</Eyebrow>
        </m.div>
        <m.h1
          ref={headingRef}
          tabIndex={-1}
          variants={item}
          className={cn(
            'max-w-5xl text-5xl leading-none font-semibold tracking-tighter text-balance md:text-7xl lg:text-8xl',
            titleClassName,
          )}
        >
          {title}
        </m.h1>
        {intro && (
          <m.p variants={item} className="max-w-2xl text-lg text-muted-foreground md:text-xl">
            {intro}
          </m.p>
        )}
        {children && (
          <m.div variants={item} className="flex flex-col gap-8">
            {children}
          </m.div>
        )}
      </Container>

      <div className="sticky bottom-0 z-10 border-t border-border bg-background">
        <Container className="flex min-h-20 flex-wrap items-center justify-between gap-x-6 gap-y-2 py-2">
          <div className="flex min-w-0 items-center gap-6">
            {goBack ? (
              <Button variant="ghost" className="-ml-4" icon={<Icon icon={ArrowLeft} />} onClick={goBack} disabled={busy}>
                {t('common.back')}
              </Button>
            ) : (
              <span />
            )}
            {hint && <span className="label-mono hidden text-muted-foreground lg:inline">{hint}</span>}
          </div>
          <div className="flex items-center gap-6">
            {secondaryAction}
            <Button
              type="submit"
              size="lg"
              busy={busy}
              disabled={continueDisabled}
              iconEnd={<Icon icon={ArrowRight} />}
            >
              {continueLabel}
            </Button>
          </div>
          {persistFailed && (
            <p className="label-mono w-full pb-2 text-status-warn" role="status">
              {t('onboarding.progressNotSaved')}
            </p>
          )}
        </Container>
      </div>
    </form>
  )
}
