// SPDX-License-Identifier: AGPL-3.0-or-later
// Native select of pools (a share or an app lives on one). Unmounted pools are listed but disabled.
import { type ReactNode, useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { Pool } from '../api/types'
import { poolCapacity } from '../lib/pools'
import { useFormat } from '../lib/useFormat'

interface PoolSelectProps {
  pools: readonly Pool[]
  value: string
  onChange: (pool: string) => void
  disabled?: boolean
  /** Defaults to "Pool". */
  label?: ReactNode
  hint?: ReactNode
  /** Adds each mounted pool's free space to its option. */
  showFree?: boolean
}

export function PoolSelect({ pools, value, onChange, disabled = false, label, hint, showFree = false }: PoolSelectProps) {
  const { t } = useTranslation()
  const format = useFormat()
  const id = useId()
  const optionLabel = (pool: Pool): string => {
    if (pool.status && !pool.status.mounted) return `${pool.name} · ${t('pools.health.unmounted')}`
    const capacity = showFree ? poolCapacity(pool.status) : null
    return capacity ? t('pools.selectFree', { name: pool.name, free: format.bytes(capacity.free) }) : pool.name
  }
  return (
    <div className="flex flex-col gap-2">
      <label htmlFor={id} className="label-mono text-muted-foreground">
        {label ?? t('shares.pool')}
      </label>
      <select
        id={id}
        aria-describedby={hint ? `${id}-hint` : undefined}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        className="h-12 w-full border border-border bg-input px-4 text-base text-foreground outline-none transition-colors duration-150 ease-crisp hover:border-border-hover focus:border-accent disabled:opacity-50 md:h-14"
      >
        {pools.map((pool) => (
          <option key={pool.name} value={pool.name} disabled={pool.status ? !pool.status.mounted : false}>
            {optionLabel(pool)}
          </option>
        ))}
      </select>
      {hint && (
        <p id={`${id}-hint`} className="text-sm text-muted-foreground">
          {hint}
        </p>
      )}
    </div>
  )
}
