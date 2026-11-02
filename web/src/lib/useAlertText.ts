// SPDX-License-Identifier: AGPL-3.0-or-later
// Alert → plain sentence + what to do, from `alerts.codes.<code>.title|action` (EN/FR).
// A disk is named like the disk list does (model + the end of its id) when it is known.
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import type { Alert, Disk } from '../api/types'
import { diskTitle } from '../components/DiskRow'
import { useDynamicT } from './messages'
import { useFormat } from './useFormat'

export interface AlertText {
  title: string
  /** What to do about it (null for unknown codes). */
  action: string | null
}

/** "Seagate ST4000VN008 (…4567)", or the full id when the disk isn't connected/known. */
export function alertDiskLabel(id: string, disks: readonly Disk[] | undefined, unknown: string): string {
  const disk = disks?.find((d) => d.id === id)
  if (!disk) return id
  return `${diskTitle(disk, unknown)} (…${id.slice(-4)})`
}

export function useAlertText(disks?: readonly Disk[]): (alert: Alert) => AlertText {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()

  return useCallback(
    (alert: Alert) => {
      const p = alert.params
      const values: Record<string, unknown> = { ...p }
      if (typeof p.disk === 'string') values.disk = alertDiskLabel(p.disk, disks, t('common.unknown'))
      if (typeof p.celsius === 'number') values.celsius = format.celsius(p.celsius)
      if (typeof p.freeBytes === 'number') values.free = format.bytes(p.freeBytes)
      if (Array.isArray(p.attributes)) {
        values.attributes = p.attributes
          .map((id) => (has(`smart.attr.${String(id)}`) ? td(`smart.attr.${String(id)}`) : `#${String(id)}`))
          .join(', ')
      }
      // smart.crc: the plural follows the new errors; the total is shown alongside.
      if (typeof p.increase === 'number') {
        values.total = format.number(typeof p.count === 'number' ? p.count : 0)
        values.count = p.increase
      }
      const base = `alerts.codes.${alert.code}`
      return {
        title: has(`${base}.title`) || has(`${base}.title_one`) ? td(`${base}.title`, values) : t('alerts.unknownTitle', { code: alert.code }),
        action: has(`${base}.action`) ? td(`${base}.action`, values) : null,
      }
    },
    [t, td, has, format, disks],
  )
}
