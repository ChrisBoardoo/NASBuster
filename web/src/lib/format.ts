// SPDX-License-Identifier: AGPL-3.0-or-later
// Pure, locale-aware formatters. The API sends raw values only (bytes, °C, Unix
// seconds); everything human-readable is produced here with Intl.

/** A number and its unit, kept apart so the UI can set them in different type styles. */
export interface Parts {
  value: string
  unit: string
}

export const BYTE_UNITS = ['B', 'kB', 'MB', 'GB', 'TB', 'PB'] as const
export type ByteUnit = (typeof BYTE_UNITS)[number]

export const EMPTY = '—'

const numberFormats = new Map<string, Intl.NumberFormat>()

function numberFormat(lang: string, options: Intl.NumberFormatOptions): Intl.NumberFormat {
  const key = `${lang}|${JSON.stringify(options)}`
  let format = numberFormats.get(key)
  if (!format) {
    format = new Intl.NumberFormat(lang, options)
    numberFormats.set(key, format)
  }
  return format
}

export function formatNumber(lang: string, value: number, maxFraction = 0, minFraction = 0): string {
  if (!Number.isFinite(value)) return EMPTY
  return numberFormat(lang, {
    maximumFractionDigits: maxFraction,
    minimumFractionDigits: minFraction,
  }).format(value)
}

/**
 * Decimal (SI) byte units, as storage vendors label disks: 4000787030016 → 4.0 TB.
 * One decimal below 100 (4.0 TB, 8.2 GB), none above (912 MB).
 */
export function bytesParts(lang: string, bytes: number, unitLabel: (unit: ByteUnit) => string): Parts {
  if (!Number.isFinite(bytes) || bytes < 0) return { value: EMPTY, unit: '' }
  let index = 0
  let value = bytes
  while (value >= 1000 && index < BYTE_UNITS.length - 1) {
    value /= 1000
    index++
  }
  // 999.96 GB would round to "1,000 GB": promote to the next unit instead.
  if (Math.round(value) >= 1000 && index < BYTE_UNITS.length - 1) {
    value /= 1000
    index++
  }
  const unit = BYTE_UNITS[index] ?? 'B'
  const digits = index === 0 || value >= 100 ? 0 : 1
  return { value: formatNumber(lang, value, digits, digits), unit: unitLabel(unit) }
}

export function joinParts(parts: Parts): string {
  return parts.unit ? `${parts.value} ${parts.unit}` : parts.value
}

export interface DurationSplit {
  days: number
  hours: number
  minutes: number
}

export function splitDuration(totalSeconds: number): DurationSplit {
  const seconds = Math.max(0, Math.floor(totalSeconds))
  return {
    days: Math.floor(seconds / 86400),
    hours: Math.floor((seconds % 86400) / 3600),
    minutes: Math.floor((seconds % 3600) / 60),
  }
}

/** Two most significant units: "3 d 04 h", "4 h 12 min", "7 min". */
export function durationParts(
  lang: string,
  totalSeconds: number,
  unitLabel: (unit: 'd' | 'h' | 'min') => string,
): Parts[] {
  if (!Number.isFinite(totalSeconds) || totalSeconds < 0) return [{ value: EMPTY, unit: '' }]
  const { days, hours, minutes } = splitDuration(totalSeconds)
  const padded = (n: number) => numberFormat(lang, { minimumIntegerDigits: 2 }).format(n)
  if (days > 0) {
    return [
      { value: formatNumber(lang, days), unit: unitLabel('d') },
      { value: padded(hours), unit: unitLabel('h') },
    ]
  }
  if (hours > 0) {
    return [
      { value: formatNumber(lang, hours), unit: unitLabel('h') },
      { value: padded(minutes), unit: unitLabel('min') },
    ]
  }
  return [{ value: formatNumber(lang, minutes), unit: unitLabel('min') }]
}

export function celsiusParts(lang: string, celsius: number | null | undefined): Parts {
  if (celsius === null || celsius === undefined || !Number.isFinite(celsius)) {
    return { value: EMPTY, unit: '' }
  }
  return { value: formatNumber(lang, celsius, 1, 1), unit: '°C' }
}

const relativeFormats = new Map<string, Intl.RelativeTimeFormat>()

/** "3 minutes ago", "il y a 3 minutes", "now". */
export function relativeTime(lang: string, tsSeconds: number, nowMs: number): string {
  let format = relativeFormats.get(lang)
  if (!format) {
    format = new Intl.RelativeTimeFormat(lang, { numeric: 'auto' })
    relativeFormats.set(lang, format)
  }
  const diff = tsSeconds - nowMs / 1000
  const abs = Math.abs(diff)
  if (abs < 45) return format.format(Math.round(diff / 10) * 10, 'second')
  if (abs < 45 * 60) return format.format(Math.round(diff / 60), 'minute')
  if (abs < 22 * 3600) return format.format(Math.round(diff / 3600), 'hour')
  if (abs < 26 * 86400) return format.format(Math.round(diff / 86400), 'day')
  if (abs < 320 * 86400) return format.format(Math.round(diff / (30 * 86400)), 'month')
  return format.format(Math.round(diff / (365 * 86400)), 'year')
}

const dateFormats = new Map<string, Intl.DateTimeFormat>()

export function dateTime(lang: string, tsSeconds: number): string {
  if (!Number.isFinite(tsSeconds) || tsSeconds <= 0) return EMPTY
  let format = dateFormats.get(lang)
  if (!format) {
    format = new Intl.DateTimeFormat(lang, { dateStyle: 'medium', timeStyle: 'short' })
    dateFormats.set(lang, format)
  }
  return format.format(new Date(tsSeconds * 1000))
}

/** Power-on hours as a rough age: 12345 h → "1.4 years" / "1,4 an". */
export function hoursAsYears(lang: string, hours: number): string {
  if (!Number.isFinite(hours) || hours < 0) return EMPTY
  return numberFormat(lang, {
    style: 'unit',
    unit: 'year',
    unitDisplay: 'long',
    maximumFractionDigits: 1,
  }).format(hours / 8766)
}

/** "an exFAT file system" → "An exFAT file system" (a sentence fragment shown on its own). */
export function capitalizeFirst(text: string): string {
  return text.charAt(0).toLocaleUpperCase() + text.slice(1)
}
