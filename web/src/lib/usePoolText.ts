// SPDX-License-Identifier: AGPL-3.0-or-later
// Translated words for pools (layout names, health, scrub, sync, plan warnings). Keys come
// from the server at runtime, so unknown values fall back to the raw code.
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { PoolLayout } from '../api/types'
import { useDynamicT } from './messages'

export interface PoolText {
  layoutTitle: (name: string) => string
  layoutTech: (name: string) => string
  layoutBody: (name: string) => string
  /** "Survives 1 disk failure" / "No disk can fail". */
  survives: (tolerates: number) => string
  /** "2 disks or more", "1 disk". */
  diskRange: (layout: PoolLayout) => string
  healthLabel: (health: string | null | undefined) => string
  healthHint: (health: string | null | undefined) => string | null
  scrubLabel: (status: string) => string
  syncLabel: (action: string) => string
  planWarning: (code: string) => string
  /** `errors.<code>` for a status error code, else the code. */
  statusError: (code: string) => string
}

export function usePoolText(): PoolText {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()

  return useMemo<PoolText>(() => {
    const lookup = (key: string, fallback: string) => (has(key) ? td(key) : fallback)
    return {
      layoutTitle: (name) => lookup(`layouts.${name}.title`, name),
      layoutTech: (name) => lookup(`layouts.${name}.tech`, ''),
      layoutBody: (name) => lookup(`layouts.${name}.body`, ''),
      survives: (tolerates) => (tolerates > 0 ? t('pools.survives', { count: tolerates }) : t('pools.survivesNone')),
      diskRange: (layout) => {
        if (layout.maxDisks === layout.minDisks) return t('pools.disksExact', { count: layout.minDisks })
        if (layout.maxDisks === 0) return t('pools.disksMin', { count: layout.minDisks })
        return `${layout.minDisks}–${layout.maxDisks}`
      },
      healthLabel: (health) => lookup(`pools.health.${health || 'unknown'}`, health || t('pools.health.unknown')),
      healthHint: (health) => {
        const key = `pools.healthHint.${health || 'unknown'}`
        return has(key) ? td(key) : null
      },
      scrubLabel: (status) => lookup(`pools.scrubStatus.${status}`, status),
      syncLabel: (action) => lookup(`pools.sync.${action}`, action),
      planWarning: (code) => lookup(`pools.warning.${code}`, code),
      statusError: (code) => lookup(`errors.${code}`, code),
    }
  }, [t, td, has])
}
