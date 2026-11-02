// SPDX-License-Identifier: AGPL-3.0-or-later
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '../components/Button'
import { Icon } from '../components/Icon'
import { LanguageToggle } from '../components/Toggles'
import { Container, Eyebrow } from '../components/Typography'
import { Wordmark } from '../components/Wordmark'
import { useErrorMessage } from '../lib/messages'

export function BootError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()

  return (
    <div className="flex min-h-dvh flex-col">
      <Container className="flex min-h-16 items-center justify-between md:min-h-20">
        <Wordmark />
        <LanguageToggle />
      </Container>
      <Container className="flex flex-1 flex-col justify-center gap-8 py-16">
        <Eyebrow>{t('app.bootError.eyebrow')}</Eyebrow>
        <h1 className="max-w-4xl text-5xl leading-none font-semibold tracking-tighter text-balance md:text-7xl lg:text-8xl">
          {t('app.bootError.title')}
        </h1>
        <p className="max-w-2xl text-lg text-muted-foreground md:text-xl">{t('app.bootError.body')}</p>
        <p className="font-mono text-sm text-muted-foreground" role="alert">
          {errorMessage(error)}
        </p>
        <div>
          <Button size="lg" iconEnd={<Icon icon={RefreshCw} />} onClick={onRetry}>
            {t('app.bootError.retry')}
          </Button>
        </div>
      </Container>
    </div>
  )
}
