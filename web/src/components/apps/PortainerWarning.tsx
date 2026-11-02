// SPDX-License-Identifier: AGPL-3.0-or-later
// What an expert app (Portainer) implies, said before installing it and right after:
// it controls Docker entirely, and its admin account goes to whoever opens it first.
import { useTranslation } from 'react-i18next'
import type { AppTemplate } from '../../api/types'
import { Notice } from '../Notice'

/** Portainer creates its administrator on the first visit (and locks itself if nobody does). */
export const claimsAdminOnFirstVisit = (template: Pick<AppTemplate, 'name'> | undefined): boolean =>
  template?.name === 'portainer'

export function PortainerWarning({ template, short = false }: { template: AppTemplate; short?: boolean }) {
  const { t } = useTranslation()
  const firstVisit = claimsAdminOnFirstVisit(template)
  if (short) {
    return (
      <p className="text-sm text-status-warn">
        {t('apps.expert.controls', { title: template.title })}
        {firstVisit && ` ${t('apps.expert.firstVisitShort')}`}
      </p>
    )
  }
  return (
    <Notice status="warn" title={t('apps.expert.controls', { title: template.title })}>
      <span className="flex flex-col gap-2">
        {firstVisit && <span className="text-foreground">{t('apps.expert.firstVisit', { title: template.title })}</span>}
        {firstVisit && <span>{t('apps.expert.lockout', { title: template.title })}</span>}
        {template.webScheme === 'https' && <span>{t('apps.expert.certificate')}</span>}
      </span>
    </Notice>
  )
}
