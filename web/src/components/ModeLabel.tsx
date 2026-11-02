// SPDX-License-Identifier: AGPL-3.0-or-later
// A user's access to a share, as words: "Read & write" or "Read only".
import { useTranslation } from 'react-i18next'
import { StatusLabel } from './StatusLabel'

export function ModeLabel({ mode }: { mode: string }) {
  const { t } = useTranslation()
  if (mode === 'rw') return <StatusLabel status="ok" label={t('shares.mode.rw')} />
  if (mode === 'ro') return <StatusLabel status="info" label={t('shares.mode.ro')} />
  return <StatusLabel status="unknown" label={mode} />
}
