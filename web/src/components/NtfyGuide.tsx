// SPDX-License-Identifier: AGPL-3.0-or-later
// How to receive alerts on a phone with ntfy, and why the topic name is random.
// Shared by Settings → Notifications and the onboarding's safety step.
import { ShieldCheck } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { splitTopicUrl } from '../lib/notifications'
import { CopyText } from './CopyText'
import { Icon } from './Icon'

export function NtfyGuide({ url }: { url: string }) {
  const { t } = useTranslation()
  const parts = splitTopicUrl(url)
  const ownServer = parts !== null && parts.server !== 'ntfy.sh'

  return (
    <div className="flex flex-col gap-6">
      <ol className="flex flex-col gap-4">
        <Step n={1}>{t('notifications.guide.install')}</Step>
        <Step n={2}>
          <span className="flex flex-col gap-2">
            <span>{ownServer ? t('notifications.guide.subscribeServer', { server: parts.server }) : t('notifications.guide.subscribe')}</span>
            {parts ? (
              <CopyText value={parts.topic} label={t('common.copyValue', { value: parts.topic })} />
            ) : (
              <span className="text-muted-foreground">{t('notifications.guide.noTopic')}</span>
            )}
          </span>
        </Step>
        <Step n={3}>{t('notifications.guide.test')}</Step>
      </ol>
      <p className="flex max-w-2xl gap-3 text-sm text-muted-foreground">
        <Icon icon={ShieldCheck} size={18} className="mt-0.5 shrink-0 text-status-ok" />
        <span>{t('notifications.privacy')}</span>
      </p>
    </div>
  )
}

function Step({ n, children }: { n: number; children: ReactNode }) {
  return (
    <li className="grid grid-cols-[2rem_1fr] items-baseline gap-2">
      <span className="font-mono text-sm text-accent tabular-nums">{String(n).padStart(2, '0')}</span>
      <span className="min-w-0 break-words">{children}</span>
    </li>
  )
}
