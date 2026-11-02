// SPDX-License-Identifier: AGPL-3.0-or-later
// Disk list with its loading / error / empty states and the "N disks available" summary.
import { HardDrive, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { DisksResponse } from '../api/types'
import type { ApiState } from '../api/useApi'
import { useErrorMessage } from '../lib/messages'
import { Button } from './Button'
import { DiskRow } from './DiskRow'
import { Icon } from './Icon'
import { InlineLoading } from './Loading'
import { Notice } from './Notice'

interface DiskListProps {
  state: ApiState<DisksResponse>
  compact?: boolean
  /** Max rows in compact mode. */
  limit?: number
  /** Rows get DOM ids so pool members can link to them. */
  anchors?: boolean
  /** Disk id → name of its pool. */
  poolOf?: ReadonlyMap<string, string>
  /** Offers "Erase…" on disks in use; called after a wipe. */
  onChanged?: () => void
}

export function DiskList({ state, compact = false, limit, anchors = false, poolOf, onChanged }: DiskListProps) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()

  if (state.loading && !state.data) return <InlineLoading label={t('disks.loading')} />

  if (state.error && !state.data) {
    return (
      <Notice
        status="warn"
        title={t('disks.loadFailed')}
        actions={
          <Button variant="ghost" size="sm" className="-ml-3" onClick={state.reload}>
            {t('common.retry')}
          </Button>
        }
      >
        {errorMessage(state.error)}
      </Notice>
    )
  }

  // Show what can be used first; the system and protected disks last.
  const disks = sortDisks(state.data?.disks ?? [])

  if (disks.length === 0) {
    return (
      <div className="flex flex-col items-start gap-4 border-t border-border py-10">
        <Icon icon={HardDrive} size={28} className="text-muted-foreground" />
        <p className="text-3xl font-semibold tracking-tight md:text-4xl">{t('disks.empty.title')}</p>
        <p className="max-w-xl text-muted-foreground">{t('disks.empty.body')}</p>
        <Button variant="secondary" size="sm" icon={<Icon icon={RefreshCw} />} onClick={state.reload} busy={state.loading}>
          {t('common.refresh')}
        </Button>
      </div>
    )
  }

  const available = disks.filter((disk) => disk.role === 'available').length
  const shown = compact && limit ? disks.slice(0, limit) : disks

  return (
    <div className="flex flex-col gap-6">
      <p className="flex items-center gap-3 text-lg font-semibold tracking-tight md:text-xl" aria-live="polite">
        <span aria-hidden="true" className="h-0.5 w-8 shrink-0 bg-accent" />
        <span className="tabular-nums">{t('disks.available', { count: available })}</span>
        <span className="label-mono font-normal text-muted-foreground">· {t('disks.count', { count: disks.length })}</span>
      </p>
      <ul className="border-b border-border">
        {shown.map((disk) => (
          <DiskRow key={disk.id} disk={disk} compact={compact} anchor={anchors} poolName={poolOf?.get(disk.id)} onChanged={onChanged} />
        ))}
      </ul>
    </div>
  )
}

const ROLE_ORDER: Record<string, number> = { available: 0, in_use: 1, protected: 2, system: 3 }

/** What can be used first, the system and protected disks last; then by device path. */
export function sortDisks<T extends { role: string; devPath: string }>(disks: readonly T[]): T[] {
  return [...disks].sort((a, b) => roleOrder(a.role) - roleOrder(b.role) || a.devPath.localeCompare(b.devPath))
}

function roleOrder(role: string): number {
  return ROLE_ORDER[role] ?? 4
}
