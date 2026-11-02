// SPDX-License-Identifier: AGPL-3.0-or-later
// Pools: the Storage page list (with its loading / error / empty states) and the dashboard
// summary (poster free space across pools + one health line per pool).
import { ArrowRight, Layers } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { Disk, PoolDestroyResult, PoolsResponse } from '../api/types'
import type { ApiState } from '../api/useApi'
import { cn } from '../lib/cn'
import { useErrorMessage } from '../lib/messages'
import { poolCapacity, poolHealthStatus, usageStatus } from '../lib/pools'
import { useFormat } from '../lib/useFormat'
import { usePoolText } from '../lib/usePoolText'
import { Button, ButtonLink } from './Button'
import { Icon } from './Icon'
import { NEW_POOL_PATH } from '../lib/constants'
import { InlineLoading } from './Loading'
import { Meter } from './Meter'
import { Notice } from './Notice'
import { PoolRow } from './PoolRow'
import { StatusLabel } from './StatusLabel'
import { PosterNumber } from './Typography'

export { NEW_POOL_PATH }

interface PoolListProps {
  state: ApiState<PoolsResponse>
  disks: readonly Disk[] | undefined
  /** Something changed beyond one row: reload pools and disks. */
  onChanged?: () => void
  onDestroyed?: (name: string, result: PoolDestroyResult) => void
}

export function PoolList({ state, disks, onChanged, onDestroyed }: PoolListProps) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()

  if (state.loading && !state.data) return <InlineLoading />
  if (state.error && !state.data) {
    return (
      <Notice
        status="warn"
        title={t('storage.poolsLoadFailed')}
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

  const pools = state.data?.pools ?? []
  if (pools.length === 0) return <NoPool disks={disks} />

  return (
    <ul className="border-b border-border">
      {pools.map((pool) => (
        <PoolRow key={pool.name} pool={pool} disks={disks} onChanged={onChanged} onDestroyed={onDestroyed} />
      ))}
    </ul>
  )
}

/** Empty state with the primary action: create the first pool. */
export function NoPool({ disks, compact = false }: { disks: readonly Disk[] | undefined; compact?: boolean }) {
  const { t } = useTranslation()
  const available = disks ? disks.filter((disk) => disk.role === 'available').length : null

  return (
    <div className={cn('flex flex-col items-start gap-5', compact ? 'py-4' : 'py-6')}>
      <Icon icon={Layers} size={28} className="text-muted-foreground" />
      <p className={cn('font-semibold tracking-tight', compact ? 'text-3xl md:text-4xl' : 'text-4xl md:text-5xl')}>
        {compact ? t('dashboard.noPool.title') : t('storage.empty.title')}
      </p>
      <p className="max-w-xl text-lg text-muted-foreground">
        {compact ? t('dashboard.noPool.body') : t('storage.empty.body')}
        {available !== null && (
          <>
            {' '}
            <span className="text-foreground">
              {available > 0 ? t('storage.empty.ready', { count: available }) : t('storage.empty.noneReady')}
            </span>
          </>
        )}
      </p>
      <ButtonLink href={NEW_POOL_PATH} size="lg" iconEnd={<Icon icon={ArrowRight} />}>
        {t('storage.createPool')}
      </ButtonLink>
    </div>
  )
}

/** Dashboard: free space across all pools as the poster number, then one line per pool. */
export function PoolSummary({ state, disks }: PoolListProps) {
  const { t } = useTranslation()
  const text = usePoolText()
  const format = useFormat()
  const errorMessage = useErrorMessage()

  if (state.loading && !state.data) return <InlineLoading />
  if (state.error && !state.data) return <Notice status="warn">{errorMessage(state.error)}</Notice>

  const pools = state.data?.pools ?? []
  if (pools.length === 0) return <NoPool disks={disks} compact />

  let total = 0
  let used = 0
  let free = 0
  for (const pool of pools) {
    const capacity = poolCapacity(pool.status)
    if (!capacity) continue
    total += capacity.total
    used += capacity.used
    free += capacity.free
  }
  const ratio = total > 0 ? used / total : 0
  const freeParts = total > 0 ? format.bytesParts(free) : null

  return (
    <div className="grid gap-x-12 gap-y-10 lg:grid-cols-12">
      <div className="@container flex min-w-0 flex-col gap-4 border-t border-border pt-5 lg:col-span-5">
        <p className="label-mono text-muted-foreground">{t('dashboard.poolsCount', { count: pools.length })}</p>
        <p className="flex flex-wrap items-baseline gap-x-3 text-[length:clamp(2.75rem,24cqi,6rem)] leading-none font-semibold tracking-tighter">
          {freeParts ? <PosterNumber value={freeParts.value} unit={freeParts.unit} /> : '—'}
        </p>
        {total > 0 && (
          <>
            <p className="text-sm text-muted-foreground tabular-nums">
              {t('dashboard.poolsFree', { total: format.bytes(total) })}
            </p>
            <Meter
              value={ratio}
              status={usageStatus(ratio)}
              label={t('dashboard.poolsTitle')}
              valueText={t('pools.usedOf', { used: format.bytes(used), total: format.bytes(total) })}
            />
          </>
        )}
      </div>
      <ul className="self-start border-b border-border lg:col-span-7">
        {pools.map((pool) => {
          const capacity = poolCapacity(pool.status)
          const health = poolHealthStatus(pool.status?.health)
          return (
            <li
              key={pool.name}
              className="grid grid-cols-[1fr_auto] items-baseline gap-x-6 gap-y-1 border-t border-border py-4"
            >
              <p className="flex min-w-0 flex-wrap items-baseline gap-x-3">
                <span className="text-2xl font-semibold tracking-tight break-all">{pool.name}</span>
                <span className="label-mono text-muted-foreground">{text.layoutTitle(pool.layout)}</span>
              </p>
              <StatusLabel status={health} label={text.healthLabel(pool.status?.health)} />
              <p className="col-span-2 text-sm text-muted-foreground tabular-nums">
                {capacity
                  ? t('dashboard.poolFree', { free: format.bytes(capacity.free), total: format.bytes(capacity.total) })
                  : pool.status
                    ? text.healthHint(pool.status.health)
                    : text.statusError(pool.statusError || 'helper.unavailable')}
              </p>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
