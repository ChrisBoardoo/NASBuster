// SPDX-License-Identifier: AGPL-3.0-or-later
// Translation of server-provided codes (errors, health checks). These keys are built at
// runtime, so they are checked with `has()` and fall back gracefully when unknown.
import { useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, UNKNOWN_ERROR } from '../api/client'
import type { Check, HealthStatus } from '../api/types'
import { useFormat } from './useFormat'

type Translate = (key: string, options?: Record<string, unknown>) => string

export const HEALTH_STATUSES = ['ok', 'info', 'warn', 'crit', 'unknown'] as const satisfies readonly HealthStatus[]

export function normalizeStatus(status: unknown): HealthStatus {
  return (HEALTH_STATUSES as readonly unknown[]).includes(status) ? (status as HealthStatus) : 'unknown'
}

/** Untyped `t` for runtime keys, plus `has()` which only accepts leaf strings. */
export function useDynamicT(): { td: Translate; has: (key: string) => boolean } {
  const { t, i18n } = useTranslation()
  return useMemo(() => {
    const has = (key: string) => {
      const lang = i18n.resolvedLanguage ?? i18n.language
      return (
        typeof i18n.getResource(lang, 'translation', key) === 'string' ||
        typeof i18n.getResource('en', 'translation', key) === 'string'
      )
    }
    const td = t as unknown as Translate
    return { td, has }
  }, [t, i18n])
}

/** Maps any thrown value to a translated sentence (`errors.<code>`, else a generic one). */
export function useErrorMessage(): (error: unknown) => string {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()
  return useCallback(
    (error: unknown) => {
      if (error instanceof ApiError) {
        const key = `errors.${error.code}`
        if (error.code !== UNKNOWN_ERROR && has(key)) return td(key, errorOptions(error, format.bytes, td, has))
        if (error.status > 0) return t('errors.genericWithStatus', { status: error.status })
      }
      return t('errors.generic')
    },
    [t, td, has, format],
  )
}

const REASSEMBLE_REASONS: readonly string[] = ['not_broken', 'missing_disks', 'roles', 'events_gap', 'changed']

/** Raw server params → interpolation values (formatted sizes, translated roles, plural/context). */
function errorOptions(
  error: ApiError,
  bytes: (value: number) => string,
  td: Translate,
  has: (key: string) => boolean,
): Record<string, unknown> {
  const params = error.params
  const options: Record<string, unknown> = { ...params }
  if (typeof params.minBytes === 'number') options.minBytes = bytes(params.minBytes)
  if (typeof params.requiredBytes === 'number') options.requiredBytes = bytes(params.requiredBytes)
  // Lists (shares blocking a pool, programs holding it, apps blocking Docker) read as "a, b, c".
  for (const key of ['shares', 'entries', 'holders', 'apps'] as const) {
    if (Array.isArray(params[key])) options[key] = (params[key] as unknown[]).map(String).join(', ')
  }
  if (error.code === 'validation.url' && (params.channel === 'ntfy' || params.channel === 'webhook')) {
    options.context = params.channel
  }
  if (typeof params.role === 'string') {
    const roleKey = `disks.role.${params.role}`
    options.role = has(roleKey) ? td(roleKey) : params.role
  }
  switch (error.code) {
    case 'validation.disk_count': {
      // `max: 0` means "no limit"; single is exactly one disk.
      const min = typeof params.min === 'number' ? params.min : 0
      const max = typeof params.max === 'number' ? params.max : 0
      options.count = min
      if (max === 0) options.context = 'atLeast'
      else if (max === min) options.context = 'exact'
      break
    }
    case 'apps.engine_in_use':
      // Either our apps (names) or other containers (a count), made by hand or by Portainer.
      if (Array.isArray(params.apps) && params.apps.length > 0) options.context = 'apps'
      else if (typeof params.containers === 'number') {
        options.context = 'containers'
        options.count = params.containers
      }
      break
    case 'storage.disk_residual':
      // An old file system inside the new partition: name it in plain words when we can.
      if (typeof params.found === 'string' && params.found !== '') {
        options.found = has(`wipe.fs.${params.found}`) ? td(`wipe.fs.${params.found}`) : describeFound(params.found, td, has)
      }
      break
    case 'storage.cannot_reassemble':
      // Why not (ADR-0032): not_broken, missing_disks, roles, events_gap.
      if (typeof params.reason === 'string' && REASSEMBLE_REASONS.includes(params.reason)) options.context = params.reason
      break
    case 'storage.disk_too_small':
      // Replacement: the old member's size is required (params.requiredBytes).
      if (typeof params.requiredBytes === 'number') options.context = 'required'
      break
    case 'storage.disk_not_available':
      if (typeof params.disk !== 'string' || params.disk === '') {
        options.context = 'unknown'
      } else if (typeof params.role !== 'string' && typeof params.found !== 'string') {
        // Replacement: not blank, or already a member of the pool.
        options.context = 'taken'
      } else if (typeof params.found === 'string' && params.found !== '') {
        // The helper's own low-level re-check found something on the disk (ext4, gpt, a partition…).
        options.context = 'found'
        options.found = describeFound(params.found, td, has)
      }
      break
  }
  return options
}

/** What the helper found on a disk: "a partition", "a gpt partition table", "an ext4 signature"… */
function describeFound(found: string, td: Translate, has: (key: string) => boolean): string {
  const key = `disks.found.${found}`
  if (has(key)) return td(key)
  if (found === 'gpt' || found === 'dos') return td('disks.found.table', { type: found.toUpperCase() })
  return td('disks.found.signature', { type: found })
}

export function errorCode(error: unknown): string | null {
  return error instanceof ApiError ? error.code : null
}

export interface CheckText {
  name: string
  message: string
  advice: string | undefined
  status: HealthStatus
}

/** Name, message (with formatted params) and plain-language advice for a health check. */
export function useCheckText(): (check: Check) => CheckText {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()

  return useCallback(
    (check: Check) => {
      const unknown = t('common.unknown')
      const raw = check.params ?? {}
      const params: Record<string, unknown> = { ...raw }
      for (const key of ['model', 'arch', 'device'] as const) {
        if (typeof params[key] !== 'string' || params[key] === '') params[key] = unknown
      }
      params.bytes = typeof raw.bytes === 'number' ? format.bytes(raw.bytes) : unknown
      params.celsius = typeof raw.celsius === 'number' ? format.celsius(raw.celsius) : unknown

      const status = normalizeStatus(check.status)
      const messageKey = `checks.${check.code}`
      const nameKey = `checkNames.${check.id}`
      const adviceKey = `checkAdvice.${check.code}`
      return {
        status,
        name: has(nameKey) ? td(nameKey) : check.id,
        message: has(messageKey) ? td(messageKey, params) : check.code,
        advice: has(adviceKey) ? td(adviceKey) : status === 'unknown' ? t('checkAdvice.unknown') : undefined,
      }
    },
    [t, td, has, format],
  )
}

export function countByStatus(checks: readonly Check[]): Record<HealthStatus, number> {
  const counts: Record<HealthStatus, number> = { ok: 0, info: 0, warn: 0, crit: 0, unknown: 0 }
  for (const check of checks) counts[normalizeStatus(check.status)] += 1
  return counts
}
