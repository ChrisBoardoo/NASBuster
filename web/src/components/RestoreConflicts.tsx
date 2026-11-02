// SPDX-License-Identifier: AGPL-3.0-or-later
// Accounts a configuration restore skipped (a name or an ID taken on this system), each as the
// server wrote it ("user alice: UID 1001 is taken by bob"). Shown after the restore and again
// on the login page.
import { useTranslation } from 'react-i18next'
import { Notice } from './Notice'

export function ConflictList({ conflicts }: { conflicts: readonly string[] }) {
  const { t } = useTranslation()
  return (
    <Notice status="warn" title={t('backup.conflictsTitle', { count: conflicts.length })}>
      <span className="flex flex-col gap-2">
        <ul className="flex flex-col gap-1">
          {conflicts.map((conflict) => (
            <li key={conflict} className="font-mono text-sm break-all text-foreground">
              {conflict}
            </li>
          ))}
        </ul>
        <span>{t('backup.conflictsBody')}</span>
      </span>
    </Notice>
  )
}
