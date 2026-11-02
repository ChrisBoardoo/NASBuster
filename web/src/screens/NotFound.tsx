// SPDX-License-Identifier: AGPL-3.0-or-later
import { ArrowLeft } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ButtonLink } from '../components/Button'
import { Icon } from '../components/Icon'
import { Container, PageHeader } from '../components/Typography'
import { useDocumentTitle } from '../lib/hooks'

export default function NotFound() {
  const { t } = useTranslation()
  useDocumentTitle(t('notFound.title'))
  return (
    <Container>
      <PageHeader eyebrow={t('notFound.eyebrow')} title={t('notFound.title')} />
      <ButtonLink href="/" size="lg" icon={<Icon icon={ArrowLeft} />}>
        {t('notFound.back')}
      </ButtonLink>
    </Container>
  )
}
