// SPDX-License-Identifier: AGPL-3.0-or-later
// Minimal fetch wrapper for the v1 API: JSON in/out, session cookie, CSRF double submit,
// typed errors. Contract: md/8.architecture/api-v1.md.
import { readCookie } from '../lib/cookies'
import type { ApiErrorBody } from './types'

export const API_BASE = '/api/v1'
const CSRF_COOKIE = 'cwnsb_csrf'
const CSRF_HEADER = 'X-CSRF-Token'

export type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
const MUTATING: ReadonlySet<Method> = new Set<Method>(['POST', 'PUT', 'PATCH', 'DELETE'])

/** Client-side error codes (never sent by the server). */
export const NETWORK_ERROR = 'network.unreachable'
export const UNKNOWN_ERROR = 'unknown'

const GATEWAY_STATUSES: ReadonlySet<number> = new Set([502, 503, 504])

export class ApiError extends Error {
  readonly status: number
  /** Stable code, translated with the i18n key `errors.<code>`. */
  readonly code: string
  readonly params: Record<string, unknown>

  constructor(status: number, code: string, message: string, params: Record<string, unknown> = {}) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.params = params
  }
}

export interface RequestOptions {
  body?: unknown
  signal?: AbortSignal
  /** A 401 is an expected answer here (e.g. login): don't report the session as expired. */
  authOptional?: boolean
}

interface InternalOptions extends RequestOptions {
  retried?: boolean
}

let unauthorizedHandler: (() => void) | null = null

/** Registers the callback run when any call answers `401 auth.required` (session expired). */
export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

function isErrorBody(body: unknown): body is ApiErrorBody {
  if (typeof body !== 'object' || body === null || !('error' in body)) return false
  const error = (body as { error: unknown }).error
  return typeof error === 'object' && error !== null && typeof (error as { code?: unknown }).code === 'string'
}

async function toApiError(res: Response): Promise<ApiError> {
  let body: unknown = null
  try {
    body = await res.json()
  } catch {
    /* not JSON (proxy error page, empty body…) */
  }
  if (isErrorBody(body)) {
    const { code, message, params } = body.error
    return new ApiError(res.status, code, message || code, params ?? {})
  }
  // A gateway answering without our JSON body means nasbusterd itself is unreachable.
  const code = GATEWAY_STATUSES.has(res.status) ? NETWORK_ERROR : UNKNOWN_ERROR
  return new ApiError(res.status, code, res.statusText || `HTTP ${res.status}`)
}

function send(method: Method, path: string, options: InternalOptions): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (MUTATING.has(method)) {
    // Always declare JSON on mutations (the server answers 415 otherwise).
    headers['Content-Type'] = 'application/json'
    const token = readCookie(CSRF_COOKIE)
    if (token) headers[CSRF_HEADER] = token
  }
  return fetch(`${API_BASE}${path}`, {
    method,
    headers,
    credentials: 'same-origin',
    cache: 'no-store',
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal,
  })
}

async function refreshCsrfCookie(): Promise<void> {
  try {
    await fetch(`${API_BASE}/bootstrap`, {
      credentials: 'same-origin',
      cache: 'no-store',
      headers: { Accept: 'application/json' },
    })
  } catch {
    /* the retry will surface the error */
  }
}

export async function request<T>(method: Method, path: string, options: InternalOptions = {}): Promise<T> {
  let res: Response
  try {
    res = await send(method, path, options)
  } catch (error) {
    if (isAbortError(error)) throw error
    throw new ApiError(0, NETWORK_ERROR, error instanceof Error ? error.message : String(error))
  }

  if (!res.ok) {
    const error = await toApiError(res)
    // The CSRF cookie is set by GET /bootstrap; if it vanished, fetch it again and retry once.
    if (error.status === 403 && error.code === 'csrf.invalid' && MUTATING.has(method) && !options.retried) {
      await refreshCsrfCookie()
      return request<T>(method, path, { ...options, retried: true })
    }
    if (error.status === 401 && error.code === 'auth.required' && !options.authOptional) {
      unauthorizedHandler?.()
    }
    throw error
  }

  if (res.status === 204) return undefined as T
  const text = await res.text()
  if (!text) return undefined as T
  try {
    return JSON.parse(text) as T
  } catch {
    throw new ApiError(res.status, UNKNOWN_ERROR, 'Invalid JSON response')
  }
}
