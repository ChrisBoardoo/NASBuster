// SPDX-License-Identifier: AGPL-3.0-or-later
import { m } from 'motion/react'
import { useTranslation } from 'react-i18next'
import { Logo } from '../../components/Logo'
import { LanguageToggle } from '../../components/Toggles'
import { useReveal } from '../../lib/motion'
import { StepLayout } from './StepLayout'
import { useWizard } from './wizard'

export function WelcomeStep() {
  const { t } = useTranslation()
  const { goNext } = useWizard()
  const { item } = useReveal()

  return (
    <StepLayout
      eyebrow={t('onboarding.welcome.eyebrow')}
      titleClassName="max-w-none text-[length:clamp(3.25rem,13vw,10rem)] leading-[0.9]"
      title={
        <>
          <Logo size={128} className="mb-6 block size-24 md:mb-8 md:size-32" />
          <span className="block text-muted-foreground">{t('app.brand')}</span>
          <span className="block">
            {t('app.product')}
            <span className="text-accent">.</span>
          </span>
        </>
      }
      intro={<span className="text-foreground">{t('onboarding.welcome.promise')}</span>}
      onContinue={goNext}
      continueLabel={t('onboarding.welcome.start')}
    >
      <m.div variants={item} className="flex flex-col gap-3">
        <span className="label-mono text-muted-foreground">{t('onboarding.welcome.languageLabel')}</span>
        <LanguageToggle variant="full" className="-ml-2" />
      </m.div>
    </StepLayout>
  )
}
