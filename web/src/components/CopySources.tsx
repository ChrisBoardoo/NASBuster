// SPDX-License-Identifier: AGPL-3.0-or-later
// "Disks with data to copy" (ADR-0015): file systems found on disks outside any pool (an OMV disk,
// a USB key), each with a way into the copy flow. They are read, never modified.
import { FolderInput } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { Disk, ImportSource } from '../api/types'
import { diskTitle } from '../lib/disks'
import { capitalizeFirst } from '../lib/format'
import { useDynamicT } from '../lib/messages'
import { wipeSuffix } from '../lib/repair'
import { useFormat } from '../lib/useFormat'
import { ButtonLink } from './Button'
import { Icon } from './Icon'
import { StatusLabel } from './StatusLabel'

export function CopySources({ sources, disks }: { sources: readonly ImportSource[]; disks?: readonly Disk[] }) {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()
  return (
    <ul className="border-b border-border">
      {sources.map((src) => {
        const fsKey = `wipe.fs.${src.fstype}`
        return (
          <li key={src.uuid} className="flex flex-wrap items-center justify-between gap-x-8 gap-y-3 border-t border-border py-5">
            <div className="flex min-w-0 flex-col gap-1">
              <p className="flex flex-wrap items-baseline gap-x-3 text-2xl font-semibold tracking-tight">
                <span className="break-all">{src.label || t('copy.source.noLabel')}</span>
                <span className="font-mono text-sm font-normal text-muted-foreground tabular-nums">{format.bytes(src.sizeBytes)}</span>
              </p>
              <p className="text-sm text-muted-foreground">
                {has(fsKey) ? capitalizeFirst(td(fsKey)) : src.fstype}
                {src.disks.length > 1 && ` · ${t('copy.source.array', { count: src.disks.length })}`}
              </p>
              <p className="font-mono text-xs break-all text-muted-foreground">
                {src.disks
                  .map((id) => {
                    const disk = disks?.find((d) => d.id === id)
                    return disk ? `${diskTitle(disk, t('common.unknown'))} (…${wipeSuffix(id)})` : `…${wipeSuffix(id)}`
                  })
                  .join(', ')}
              </p>
              {src.mounted && <StatusLabel status="info" label={t('copy.source.opened')} />}
            </div>
            <ButtonLink href={`/import?source=${encodeURIComponent(src.uuid)}`} variant="secondary" size="sm" icon={<Icon icon={FolderInput} />}>
              {t('copy.copyButton')}
            </ButtonLink>
          </li>
        )
      })}
    </ul>
  )
}
