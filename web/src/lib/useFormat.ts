// SPDX-License-Identifier: AGPL-3.0-or-later
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  type ByteUnit,
  type Parts,
  bytesParts,
  celsiusParts,
  dateTime,
  durationParts,
  formatNumber,
  hoursAsYears,
  joinParts,
  relativeTime,
} from './format'

export interface Formatters {
  lang: string
  number: (value: number, maxFraction?: number, minFraction?: number) => string
  bytesParts: (bytes: number) => Parts
  bytes: (bytes: number) => string
  celsiusParts: (celsius: number | null | undefined) => Parts
  celsius: (celsius: number | null | undefined) => string
  durationParts: (seconds: number) => Parts[]
  duration: (seconds: number) => string
  relative: (tsSeconds: number, nowMs: number) => string
  dateTime: (tsSeconds: number) => string
  hoursAsYears: (hours: number) => string
}

/** Formatters bound to the active UI language (re-created when it changes). */
export function useFormat(): Formatters {
  const { t, i18n } = useTranslation()
  const lang = i18n.resolvedLanguage ?? i18n.language ?? 'en'

  return useMemo<Formatters>(() => {
    const byteUnit = (unit: ByteUnit) => t(`units.bytes.${unit}`)
    const durationUnit = (unit: 'd' | 'h' | 'min') => t(`units.duration.${unit}`)
    return {
      lang,
      number: (value, maxFraction = 0, minFraction = 0) => formatNumber(lang, value, maxFraction, minFraction),
      bytesParts: (bytes) => bytesParts(lang, bytes, byteUnit),
      bytes: (bytes) => joinParts(bytesParts(lang, bytes, byteUnit)),
      celsiusParts: (celsius) => celsiusParts(lang, celsius),
      celsius: (celsius) => joinParts(celsiusParts(lang, celsius)),
      durationParts: (seconds) => durationParts(lang, seconds, durationUnit),
      duration: (seconds) => durationParts(lang, seconds, durationUnit).map(joinParts).join(' '),
      relative: (tsSeconds, nowMs) => relativeTime(lang, tsSeconds, nowMs),
      dateTime: (tsSeconds) => dateTime(lang, tsSeconds),
      hoursAsYears: (hours) => hoursAsYears(lang, hours),
    }
  }, [lang, t])
}
