// SPDX-License-Identifier: AGPL-3.0-or-later
import type { Disk } from '../api/types'

/** "Seagate ST4000VN008" without repeating the vendor when the model already starts with it. */
export function diskTitle(disk: Disk, fallback: string): string {
  const vendor = disk.vendor.trim()
  const model = disk.model.trim()
  if (!vendor) return model || fallback
  if (!model) return vendor
  return model.toLowerCase().startsWith(vendor.toLowerCase()) ? model : `${vendor} ${model}`
}
