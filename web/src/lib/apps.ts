// SPDX-License-Identifier: AGPL-3.0-or-later
// Applications (ADR-0013): names, compose size, live state in words, and the links to open them.
import type { App, AppContainer, AppTemplate, HealthStatus, Job } from '../api/types'

/** `^[a-z][a-z0-9-]{0,29}$`, no trailing dash (the server's rule). */
export const APP_NAME_MAX = 30
export type AppNameProblem = 'empty' | 'startChar' | 'invalidChars' | 'tooLong' | 'trailingDash'

export function appNameProblem(name: string): AppNameProblem | null {
  if (name.length === 0) return 'empty'
  if (!/^[a-z]/.test(name)) return 'startChar'
  if (!/^[a-z0-9-]+$/.test(name)) return 'invalidChars'
  if (name.length > APP_NAME_MAX) return 'tooLong'
  if (name.endsWith('-')) return 'trailingDash'
  return null
}

/** The template's name, or name-2, name-3… when taken. */
export function freeAppName(base: string, taken: readonly string[]): string {
  const used = new Set(taken)
  if (!used.has(base)) return base
  for (let n = 2; n < 100; n += 1) {
    const candidate = `${base.slice(0, APP_NAME_MAX - String(n).length - 1)}-${n}`
    if (!used.has(candidate)) return candidate
  }
  return base
}

/** The server's limit for a compose file. */
export const COMPOSE_MAX_BYTES = 64 * 1024

export function utf8Bytes(text: string): number {
  return new TextEncoder().encode(text).length
}

export const isRunning = (job: Job | null | undefined): boolean => job?.state === 'running'

/** "running(1), exited(1)" → { running: 1, exited: 1 }. */
export function parseComposeStatus(status: string): Record<string, number> {
  const counts: Record<string, number> = {}
  for (const match of status.matchAll(/([a-z]+)\((\d+)\)/g)) {
    const [, state, count] = match
    if (state) counts[state] = (counts[state] ?? 0) + Number(count)
  }
  return counts
}

export type AppStateKind =
  | 'installing' // first deployment running
  | 'deploying' // redeploy or update running
  | 'failed' // the first deployment failed: nothing runs
  | 'running'
  | 'unhealthy'
  | 'partial'
  | 'restarting'
  | 'stopped'
  | 'engineDown' // Docker isn't running: no live state
  | 'unknown'

export interface AppState {
  kind: AppStateKind
  status: HealthStatus
  running: number
  total: number
}

const STATE_STATUS: Record<AppStateKind, HealthStatus> = {
  installing: 'info',
  deploying: 'info',
  failed: 'crit',
  running: 'ok',
  unhealthy: 'warn',
  partial: 'warn',
  restarting: 'warn',
  stopped: 'unknown',
  engineDown: 'warn',
  unknown: 'unknown',
}

/** One word for the whole app, from its job, its Compose status and its containers. */
export function appState(app: Pick<App, 'status' | 'containers'> & { job?: Job }, engineActive = true): AppState {
  const counts = parseComposeStatus(app.status)
  const total = app.containers.length || Object.values(counts).reduce((a, b) => a + b, 0)
  const running = app.containers.length
    ? app.containers.filter((c) => c.state === 'running').length
    : (counts.running ?? 0)
  const make = (kind: AppStateKind): AppState => ({ kind, status: STATE_STATUS[kind], running, total })

  if (app.job && isRunning(app.job)) return make(app.status === '' ? 'installing' : 'deploying')
  if (!engineActive) return make('engineDown')
  if (app.status === '') return make(app.job?.state === 'failed' ? 'failed' : 'unknown')
  if (app.containers.some((c) => c.state === 'restarting') || (counts.restarting ?? 0) > 0) return make('restarting')
  if (total > 0 && running === total) {
    return make(app.containers.some((c) => c.health === 'unhealthy') ? 'unhealthy' : 'running')
  }
  if (running > 0) return make('partial')
  return make('stopped')
}

export const CONTAINER_STATUS: Record<string, HealthStatus> = {
  running: 'ok',
  exited: 'unknown',
  restarting: 'warn',
  created: 'info',
  paused: 'info',
  dead: 'crit',
}

export const HEALTH_STATUS: Record<string, HealthStatus> = {
  healthy: 'ok',
  unhealthy: 'crit',
  starting: 'info',
}

/** The NAS as the browser reached it, ready for a URL (IPv6 in brackets). */
export function currentHost(): string {
  const host = window.location.hostname
  return host.includes(':') && !host.startsWith('[') ? `[${host}]` : host
}

export interface AppLink {
  href: string
  port: number
  /** The template's own web page (the main "Open" button). */
  main: boolean
}

/**
 * Links to the app's web pages on this NAS: the template's port first (with its scheme),
 * then one http link per other published TCP port.
 */
export function appLinks(containers: readonly AppContainer[], template: AppTemplate | undefined, host: string): AppLink[] {
  const ports = [
    ...new Set(
      containers
        .filter((c) => c.state === 'running')
        .flatMap((c) => c.ports)
        .filter((p) => p.protocol === 'tcp' && p.publishedPort > 0)
        .map((p) => p.publishedPort),
    ),
  ].sort((a, b) => a - b)
  const links: AppLink[] = []
  const mainPort = template && template.webPort > 0 && ports.includes(template.webPort) ? template.webPort : null
  if (mainPort !== null && template) {
    const scheme = template.webScheme === 'https' ? 'https' : 'http'
    links.push({ href: `${scheme}://${host}:${mainPort}/`, port: mainPort, main: true })
  }
  for (const port of ports) {
    if (port !== mainPort) links.push({ href: `http://${host}:${port}/`, port, main: false })
  }
  return links
}

export const appAnchorId = (name: string) => `app-${name}`
