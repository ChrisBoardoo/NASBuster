// SPDX-License-Identifier: AGPL-3.0-or-later
// Closing screen of the onboarding: what was set up (pool, share, protection), what comes
// next (apps, phone alerts), and marks onboarding completed.
import { m } from 'motion/react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation } from 'wouter'
import { Notice } from '../../components/Notice'
import { useErrorMessage } from '../../lib/messages'
import { useReveal } from '../../lib/motion'
import { useReadySession } from '../../session/SessionContext'
import { StepLayout } from './StepLayout'
import { useWizard } from './wizard'

export function DoneStep() {
  const { t } = useTranslation()
  const { persist, drafts } = useWizard()
  const { ready } = useReadySession()
  const [, navigate] = useLocation()
  const errorMessage = useErrorMessage()
  const { item } = useReveal()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)

  const completed = ready.onboarding?.completedSteps ?? []
  const pool = drafts.pool.submission?.outcome?.kind === 'created' || completed.includes('create')
  const share = drafts.share.share ?? (completed.includes('share') ? '' : null)

  const body =
    share !== null
      ? share
        ? t('onboarding.done.bodyShare', { share })
        : t('onboarding.done.bodyShareUnnamed')
      : pool
        ? t('onboarding.done.bodyPool')
        : t('onboarding.done.bodyNoPool')

  const finish = async () => {
    setBusy(true)
    setError(null)
    try {
      await persist({ currentStep: 'done', completed: true })
      navigate('/', { replace: true })
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <StepLayout
      eyebrow={t('onboarding.done.eyebrow')}
      title={t('onboarding.done.title')}
      intro={body}
      onContinue={() => void finish()}
      continueLabel={busy ? t('onboarding.done.finishing') : t('onboarding.done.cta')}
      busy={busy}
    >
      <m.p variants={item} className="max-w-2xl text-muted-foreground">
        {t('onboarding.done.next')}
      </m.p>
      <m.blockquote variants={item} className="flex max-w-3xl flex-col gap-4">
        <span aria-hidden="true" className="h-1 w-16 bg-accent" />
        <p className="font-display text-3xl leading-snug italic md:text-4xl">{t('onboarding.done.quote')}</p>
      </m.blockquote>
      {error !== null && (
        <Notice status="crit" assertive className="max-w-3xl">
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
    </StepLayout>
  )
}
