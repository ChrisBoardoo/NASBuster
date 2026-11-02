// SPDX-License-Identifier: AGPL-3.0-or-later
// One typed function per route of md/8.architecture/api-v1.md.
import { request } from './client'
import type {
  AlertsResponse,
  App,
  AppAction,
  AppContainer,
  AppSetupToken,
  AppsResponse,
  AppTemplate,
  CatalogResponse,
  CertificateView,
  ConfigBackupResult,
  ConfigBackupsResponse,
  ConfigRestoreResult,
  CreateAppRequest,
  EngineStatus,
  Job,
  JournalLevel,
  JournalResponse,
  JournalSettings,
  OrphanAction,
  OtherProject,
  AuditResponse,
  AuthSettings,
  Bootstrap,
  ChangePasswordRequest,
  ChecksResponse,
  CreateAdminRequest,
  CreatePoolRequest,
  CreateShareRequest,
  DemoFaultKind,
  Disk,
  DisksResponse,
  HealthResponse,
  Identity,
  ImportableResponse,
  ImportCopyRequest,
  ImportMounted,
  ImportProgress,
  ImportSourcesResponse,
  Language,
  LayoutsResponse,
  LoginRequest,
  MetricsResponse,
  NasUser,
  NasUserResponse,
  NotificationTestResult,
  NotificationsConfig,
  NotificationsUpdate,
  Onboarding,
  Pool,
  PoolPlan,
  PoolPlanRequest,
  PoolResponse,
  PoolDestroyResult,
  PoolsResponse,
  ReplaceRequest,
  ReplaceStarted,
  ResetPasswordRequest,
  RestoreStarted,
  Protection,
  ProtectionResponse,
  ScrubResponse,
  SessionsResponse,
  Share,
  ShareAccess,
  ShareResponse,
  SharesResponse,
  Smart,
  Snapshot,
  SnapshotsResponse,
  UsersResponse,
  SystemInfo,
  TimezonesResponse,
  UserResponse,
} from './types'

interface Opts {
  signal?: AbortSignal
}

// Go encodes nil slices and maps as null: normalize so components can rely on arrays/objects.
function normalizeChecks(res: ChecksResponse): ChecksResponse {
  return {
    checks: (res.checks ?? []).map((check) => ({ ...check, params: check.params ?? {} })),
  }
}

function normalizeDisk(disk: Disk): Disk {
  return {
    ...disk,
    usage: disk.usage ?? [],
    partitions: (disk.partitions ?? []).map((p) => ({ ...p, mountpoints: p.mountpoints ?? [] })),
    warnings: disk.warnings ?? [],
    usb: disk.usb ?? null,
  }
}

function normalizePool(pool: Pool): Pool {
  const status = pool.status
  return {
    ...pool,
    members: pool.members ?? [],
    statusError: pool.statusError ?? '',
    status: status
      ? {
          ...status,
          devices: status.devices ?? [],
          profiles: status.profiles ?? [],
          md: status.md
            ? { ...status.md, members: status.md.members ?? [], faulty: status.md.faulty ?? [], sync: status.md.sync ?? null }
            : null,
          scrub: status.scrub ?? {
            status: 'never',
            started: '',
            duration: '',
            corrected: 0,
            uncorrectable: 0,
            csumErrors: 0,
            readErrors: 0,
          },
        }
      : null,
  }
}

function normalizeOnboarding(res: Onboarding): Onboarding {
  return { ...res, completedSteps: res.completedSteps ?? [] }
}

// ---- Bootstrap

export const getHealth = (o: Opts = {}) => request<HealthResponse>('GET', '/health', o)

export const getBootstrap = (o: Opts = {}) => request<Bootstrap>('GET', '/bootstrap', o)

// ---- Setup / onboarding

export async function getSetupChecks(o: Opts = {}): Promise<ChecksResponse> {
  return normalizeChecks(await request<ChecksResponse>('GET', '/setup/checks', { ...o, authOptional: true }))
}

export const createAdmin = (body: CreateAdminRequest) =>
  request<UserResponse>('POST', '/setup/admin', { body, authOptional: true })

export async function getOnboarding(o: Opts = {}): Promise<Onboarding> {
  return normalizeOnboarding(await request<Onboarding>('GET', '/onboarding', o))
}

export async function putOnboarding(body: Onboarding): Promise<Onboarding> {
  const res = await request<Onboarding | undefined>('PUT', '/onboarding', { body })
  return normalizeOnboarding(res ?? body)
}

// ---- Auth

export const login = (body: LoginRequest) =>
  request<UserResponse>('POST', '/auth/login', { body, authOptional: true })

export const logout = () => request<void>('POST', '/auth/logout', { authOptional: true })

export const getMe = (o: Opts = {}) => request<UserResponse>('GET', '/auth/me', o)

export const patchMe = (body: { language: Language }) => request<UserResponse>('PATCH', '/auth/me', { body })

export const resetPassword = (body: ResetPasswordRequest) =>
  request<UserResponse>('POST', '/auth/reset-password', { body, authOptional: true })

export const changePassword = (body: ChangePasswordRequest) =>
  request<{ closedSessions: number }>('PUT', '/auth/me/password', { body })

export const getAuthSettings = (o: Opts = {}) => request<AuthSettings>('GET', '/auth/settings', o)

export const putAuthSettings = (body: AuthSettings) => request<AuthSettings>('PUT', '/auth/settings', { body })

// ---- System

export const getSystemInfo = (o: Opts = {}) => request<SystemInfo>('GET', '/system/info', o)

export async function getSystemChecks(o: Opts = {}): Promise<ChecksResponse> {
  return normalizeChecks(await request<ChecksResponse>('GET', '/system/checks', o))
}

export async function getMetrics(o: Opts = {}): Promise<MetricsResponse> {
  const res = await request<MetricsResponse>('GET', '/system/metrics', o)
  return { ...res, history: res.history ?? [] }
}

export async function getTimezones(o: Opts = {}): Promise<TimezonesResponse> {
  const res = await request<TimezonesResponse>('GET', '/system/timezones', o)
  return { timezones: res.timezones ?? [] }
}

export const putIdentity = (body: Identity) => request<Identity>('PUT', '/system/identity', { body })

export async function getAudit(limit: number, o: Opts = {}): Promise<AuditResponse> {
  const res = await request<AuditResponse>('GET', `/audit?limit=${encodeURIComponent(String(limit))}`, o)
  return { entries: res.entries ?? [] }
}

// ---- Storage

export async function getDisks(o: Opts = {}): Promise<DisksResponse> {
  const res = await request<DisksResponse>('GET', '/storage/disks', o)
  return { disks: (res.disks ?? []).map(normalizeDisk) }
}

export async function getDiskSmart(id: string, o: Opts = {}): Promise<Smart> {
  const res = await request<Smart>('GET', `/storage/disks/${encodeURIComponent(id)}/smart`, o)
  return { ...res, attributes: res.attributes ?? [] }
}

// ---- Pools (M2)

export async function getLayouts(o: Opts = {}): Promise<LayoutsResponse> {
  const res = await request<LayoutsResponse>('GET', '/storage/layouts', o)
  return { layouts: res.layouts ?? [] }
}

/** Capacity preview, without any effect on the disks. */
export async function planPool(body: PoolPlanRequest, o: Opts = {}): Promise<PoolPlan> {
  const res = await request<PoolPlan>('POST', '/storage/pools/plan', { ...o, body })
  return { ...res, warnings: res.warnings ?? [] }
}

/**
 * DESTRUCTIVE: formats the disks. Takes seconds to minutes on slow USB disks.
 * Never retried and never aborted by the UI (the server would carry on anyway).
 */
export async function createPool(body: CreatePoolRequest): Promise<Pool> {
  const res = await request<PoolResponse>('POST', '/storage/pools', { body })
  return normalizePool(res.pool)
}

export async function getPools(o: Opts = {}): Promise<PoolsResponse> {
  const res = await request<PoolsResponse>('GET', '/storage/pools', o)
  return { pools: (res.pools ?? []).map(normalizePool) }
}

export async function getPool(name: string, o: Opts = {}): Promise<Pool> {
  const res = await request<PoolResponse>('GET', `/storage/pools/${encodeURIComponent(name)}`, o)
  return normalizePool(res.pool)
}

/** Starts a scrub in the background (202); follow its progress with getPool. */
export const startScrub = (name: string) =>
  request<ScrubResponse>('POST', `/storage/pools/${encodeURIComponent(name)}/scrub`)

// ---- NAS users and shares (M4)

const normalizeUser = (user: NasUser): NasUser => ({ ...user, shares: user.shares ?? [] })
const normalizeShare = (share: Share): Share => ({
  ...share,
  access: share.access ?? [],
  timeMachine: share.timeMachine ?? false,
  timeMachineMaxGiB: share.timeMachineMaxGiB ?? 0,
  recycle: share.recycle ?? false,
  recycleDays: share.recycleDays ?? 0,
})
const seg = (value: string) => encodeURIComponent(value)

export async function getUsers(o: Opts = {}): Promise<UsersResponse> {
  const res = await request<UsersResponse>('GET', '/users', o)
  return { users: (res.users ?? []).map(normalizeUser) }
}

export async function createUser(body: { name: string; password: string }): Promise<NasUser> {
  const res = await request<NasUserResponse>('POST', '/users', { body })
  return normalizeUser(res.user)
}

export const setUserPassword = (name: string, password: string) =>
  request<void>('PUT', `/users/${seg(name)}/password`, { body: { password } })

export const deleteUser = (name: string) => request<void>('DELETE', `/users/${seg(name)}`)

export async function getShares(o: Opts = {}): Promise<SharesResponse> {
  const res = await request<SharesResponse>('GET', '/shares', o)
  return { shares: (res.shares ?? []).map(normalizeShare) }
}

export async function createShare(body: CreateShareRequest): Promise<Share> {
  const res = await request<ShareResponse>('POST', '/shares', { body })
  return normalizeShare(res.share)
}

/** Replaces the whole access list. */
export async function setShareAccess(name: string, access: ShareAccess[]): Promise<Share> {
  const res = await request<ShareResponse>('PUT', `/shares/${seg(name)}/access`, { body: { access } })
  return normalizeShare(res.share)
}

/**
 * Makes a share a Time Machine destination (or not). Changing `enabled` restarts Samba:
 * connected computers are disconnected for a moment. `maxSizeGiB` 0 = no limit.
 */
export async function setShareTimeMachine(name: string, enabled: boolean, maxSizeGiB: number): Promise<Share> {
  const res = await request<ShareResponse>('PUT', `/shares/${seg(name)}/timemachine`, { body: { enabled, maxSizeGiB } })
  return normalizeShare(res.share)
}

/** The share's recycle bin: deleted files kept `days` days (1–365) in its "#recycle" folder. */
export async function setShareRecycle(name: string, enabled: boolean, days: number): Promise<Share> {
  const res = await request<ShareResponse>('PUT', `/shares/${seg(name)}/recycle`, {
    body: { enabled, days: enabled ? days : 0 },
  })
  return normalizeShare(res.share)
}

/** Current SMB sessions and share connections (empty when Samba is stopped). */
export async function getSessions(o: Opts = {}): Promise<SessionsResponse> {
  const res = await request<SessionsResponse>('GET', '/shares/sessions', o)
  return { sessions: res.sessions ?? [], connections: res.connections ?? [] }
}

// ---- Protection (M6a)

export async function getSnapshots(share: string, o: Opts = {}): Promise<SnapshotsResponse> {
  const res = await request<SnapshotsResponse>('GET', `/shares/${seg(share)}/snapshots`, o)
  return { snapshots: res.snapshots ?? [] }
}

export async function createSnapshot(share: string): Promise<Snapshot> {
  const res = await request<{ snapshot: Snapshot }>('POST', `/shares/${seg(share)}/snapshots`)
  return res.snapshot
}

/** The snapshot name ("@GMT-…") is encoded as one path segment. */
export const deleteSnapshot = (share: string, snapshot: string) =>
  request<void>('DELETE', `/shares/${seg(share)}/snapshots/${seg(snapshot)}`)

export async function getProtection(o: Opts = {}): Promise<ProtectionResponse> {
  const res = await request<ProtectionResponse>('GET', '/protection', o)
  return { ...res, state: { lastSnapshotRun: res.state?.lastSnapshotRun ?? 0, lastScrub: res.state?.lastScrub ?? {} } }
}

export async function putProtection(protection: Protection): Promise<Protection> {
  const res = await request<{ protection: Protection }>('PUT', '/protection', { body: protection })
  return res.protection
}

// ---- Alerts and notifications (M6b)

/** Active alerts, then those resolved in the last `resolvedDays` days (0–365). */
export async function getAlerts(resolvedDays = 7, o: Opts = {}): Promise<AlertsResponse> {
  const res = await request<AlertsResponse>('GET', `/alerts?resolvedDays=${encodeURIComponent(String(resolvedDays))}`, o)
  return { alerts: (res.alerts ?? []).map((alert) => ({ ...alert, params: alert.params ?? {} })) }
}

/** Marks an alert as seen (it stays listed while the problem lasts). */
export const ackAlert = (id: string) => request<void>('POST', `/alerts/${seg(id)}/ack`)

/** The event journal, newest first (ADR-0031). `level` keeps that level and worse. */
export async function getJournal(
  q: { before?: number; level?: JournalLevel; limit?: number } = {},
  o: Opts = {},
): Promise<JournalResponse> {
  const params = new URLSearchParams()
  if (q.limit) params.set('limit', String(q.limit))
  if (q.before) params.set('before', String(q.before))
  if (q.level) params.set('level', q.level)
  const qs = params.toString()
  const res = await request<JournalResponse>('GET', `/journal${qs ? `?${qs}` : ''}`, o)
  return { events: (res.events ?? []).map((e) => ({ ...e, params: e.params ?? {} })), next: res.next ?? 0 }
}

export const getJournalSettings = (o: Opts = {}) => request<JournalSettings>('GET', '/journal/settings', o)
export const putJournalSettings = (persistent: boolean) =>
  request<JournalSettings>('PUT', '/journal/settings', { body: { persistent } })

export const getNotifications = (o: Opts = {}) => request<NotificationsConfig>('GET', '/notifications', o)

export const putNotifications = (body: NotificationsUpdate) =>
  request<NotificationsConfig>('PUT', '/notifications', { body })

/** Sends a test on every enabled channel, with the SAVED configuration. */
export async function testNotifications(): Promise<NotificationTestResult[]> {
  const res = await request<{ results: NotificationTestResult[] | null }>('POST', '/notifications/test')
  return res.results ?? []
}

// ---- Disk failure and replacement (M2b)

/** Reads the disk for a while (202 { seconds }) so its activity light blinks. Read-only. */
export const locateDisk = (id: string) => request<{ seconds: number }>('POST', `/storage/disks/${seg(id)}/locate`)

/** Erases a disk nothing uses (it becomes `available`). `confirm` is the full disk id. */
export const wipeDisk = (id: string, confirm: string) =>
  request<{ wiped: boolean }>('POST', `/storage/disks/${seg(id)}/wipe`, { body: { confirm } })

/** Explicit, never automatic: mounts a pool that didn't start because a disk is missing. */
export async function mountDegraded(name: string, confirm: string): Promise<Pool> {
  const res = await request<PoolResponse>('POST', `/storage/pools/${seg(name)}/mount-degraded`, { body: { confirm } })
  return normalizePool(res.pool)
}

/** Starts a replacement in the background (202); follow it with getPool. */
export async function replaceDisk(name: string, body: ReplaceRequest): Promise<ReplaceStarted> {
  const res = await request<{ replace: ReplaceStarted }>('POST', `/storage/pools/${seg(name)}/replace`, { body })
  return res.replace
}

export async function restoreRedundancy(name: string): Promise<RestoreStarted> {
  const res = await request<{ restore: RestoreStarted }>('POST', `/storage/pools/${seg(name)}/restore-redundancy`)
  return res.restore
}

/**
 * Takes back a disk that was unplugged and came back (ADR-0027): closes the pool's shares,
 * remounts it, then converts and scrubs in the background (202). 409 storage.pool_in_use names
 * who keeps the pool busy.
 */
export async function resumePool(name: string): Promise<Pool> {
  const res = await request<PoolResponse>('POST', `/storage/pools/${seg(name)}/resume`, { body: { confirm: name } })
  return normalizePool(res.pool)
}

/** Restarts a parity pool whose disks all came back (ADR-0032). 409 storage.cannot_reassemble. */
export async function reassemblePool(name: string, confirm: string): Promise<Pool> {
  const res = await request<PoolResponse>('POST', `/storage/pools/${seg(name)}/reassemble`, { body: { confirm } })
  return normalizePool(res.pool)
}

/** Stops a running scrub; btrfs keeps its progress (status "aborted"). 409 storage.scrub_not_running. */
export const cancelScrub = (name: string) =>
  request<{ canceled: boolean }>('POST', `/storage/pools/${seg(name)}/scrub/cancel`)

/** Goes on with an aborted or interrupted scrub (202). 409 storage.scrub_not_stopped. */
export const resumeScrub = (name: string) =>
  request<{ resumed: boolean }>('POST', `/storage/pools/${seg(name)}/scrub/resume`)

/** Resets the Btrfs error counters; only after a clean scrub (409 storage.scrub_required). */
export const clearErrors = (name: string) =>
  request<{ cleared: boolean }>('POST', `/storage/pools/${seg(name)}/clear-errors`)

/**
 * DESTRUCTIVE: deletes an empty pool and erases its disks (`destroyed`), or, when it is
 * unmounted and none of its disks is present, only forgets its configuration (`forgotten`).
 */
export async function destroyPool(name: string, confirm: string): Promise<PoolDestroyResult> {
  const res = await request<{ result: PoolDestroyResult }>('DELETE', `/storage/pools/${seg(name)}`, { body: { confirm } })
  return { ...res.result, disks: res.result.disks ?? [] }
}

/** DESTRUCTIVE: deletes a share with its files and snapshots, for good. */
export const deleteShare = (name: string, confirm: string) =>
  request<void>('DELETE', `/shares/${seg(name)}`, { body: { confirm } })

/** Demo mode only (404 otherwise): simulates a hardware event. */
export const demoFault = (kind: DemoFaultKind, disk: string) =>
  request<void>('POST', '/demo/faults', { body: { kind, disk } })

// ---- Applications (M5a)

const normalizeJob = (job: Job | null | undefined): Job => ({
  state: job?.state ?? 'none',
  exitStatus: job?.exitStatus ?? 0,
  log: job?.log ?? [],
})

const normalizeEngine = (engine: EngineStatus): EngineStatus => ({
  ...engine,
  job: normalizeJob(engine.job),
  dataPools: engine.dataPools ?? [],
})

const normalizeContainers = (containers: AppContainer[] | null | undefined): AppContainer[] =>
  (containers ?? []).map((c) => ({ ...c, ports: c.ports ?? [] }))

const normalizeApp = (app: App): App => ({
  ...app,
  status: app.status ?? '',
  containers: normalizeContainers(app.containers),
  job: normalizeJob(app.job),
})

const normalizeOther = (project: OtherProject): OtherProject => ({
  ...project,
  containers: normalizeContainers(project.containers),
})

export async function getEngine(o: Opts = {}): Promise<EngineStatus> {
  return normalizeEngine(await request<EngineStatus>('GET', '/apps/engine', o))
}

/** Starts the Docker installation (202); follow it with getJob('engine'). */
export const installEngine = (pool: string) =>
  request<{ started: boolean }>('POST', '/apps/engine/install', { body: { pool } })

/** DESTRUCTIVE: uninstalls Docker (synchronous, up to a minute). `confirm` must be "docker". */
export const removeEngine = (confirm: string, deleteData: boolean) =>
  request<{ removed: boolean }>('POST', '/apps/engine/remove', { body: { confirm, deleteData } })

/** `engine` or `app-<name>`. */
export async function getJob(job: string, o: Opts = {}): Promise<Job> {
  return normalizeJob(await request<Job>('GET', `/jobs/${seg(job)}`, o))
}

export async function getCatalog(o: Opts = {}): Promise<CatalogResponse> {
  const res = await request<CatalogResponse>('GET', '/apps/catalog', o)
  return {
    templates: (res.templates ?? []).map((tpl: AppTemplate) => ({ ...tpl, description: tpl.description ?? {} })),
  }
}

export async function getApps(o: Opts = {}): Promise<AppsResponse> {
  const res = await request<AppsResponse>('GET', '/apps', o)
  return {
    engine: normalizeEngine(res.engine),
    apps: (res.apps ?? []).map(normalizeApp),
    others: (res.others ?? []).map(normalizeOther),
    orphans: (res.orphans ?? []).map((orphan) => ({ ...normalizeOther(orphan), pool: orphan.pool ?? '' })),
  }
}

/** Stops or removes an orphan's containers (ADR-0036); its folder is never touched. */
export const orphanAction = (name: string, action: OrphanAction) =>
  request<{ ok: boolean }>('POST', `/apps/orphans/${seg(name)}/${action}`)

/** Validates the compose file, records the app and deploys it in the background (202). */
export async function createApp(body: CreateAppRequest): Promise<App> {
  const res = await request<{ app: App }>('POST', '/apps', { body })
  return normalizeApp(res.app)
}

/** Redeploys with a new compose file (202); the pool never changes. */
export async function updateAppCompose(name: string, compose: string): Promise<App> {
  const res = await request<{ app: App }>('PUT', `/apps/${seg(name)}`, { body: { compose } })
  return normalizeApp(res.app)
}

/** start / stop / restart answer 200 when done; update (pull + recreate) answers 202 and runs as a job. */
export const appAction = (name: string, action: AppAction) =>
  request<{ ok: boolean }>('POST', `/apps/${seg(name)}/${action}`)

export const getAppSetupToken = (name: string, o: Opts = {}) =>
  request<AppSetupToken>('GET', `/apps/${seg(name)}/setup-token`, o)

export async function getAppLogs(name: string, tail = 200, o: Opts = {}): Promise<string> {
  const res = await request<{ logs: string }>('GET', `/apps/${seg(name)}/logs?tail=${tail}`, o)
  return res.logs ?? ''
}

/** DESTRUCTIVE: stops and removes the app; with deleteData, its folder (compose file, ./ data) too. */
export const removeApp = (name: string, confirm: string, deleteData: boolean) =>
  request<void>('DELETE', `/apps/${seg(name)}`, { body: { confirm, deleteData } })

// ---- Losing the SD card (ADR-0014)

/** CWNSB pools on disks this NAS doesn't know. Read-only. */
export async function getImportable(o: Opts = {}): Promise<ImportableResponse> {
  const res = await request<ImportableResponse>('GET', '/storage/pools/importable', o)
  return { pools: (res.pools ?? []).map((p) => ({ ...p, disks: p.disks ?? [] })) }
}

/** Adopts a complete pool as it is: nothing is written to its disks (201). */
export async function importPool(name: string, confirm: string): Promise<Pool> {
  const res = await request<PoolResponse>('POST', '/storage/pools/import', { body: { name, confirm } })
  return normalizePool(res.pool)
}

/** Saves the configuration on every mounted pool now (409 backup.no_pool without one). */
export async function backupConfig(): Promise<ConfigBackupResult> {
  const res = await request<ConfigBackupResult>('POST', '/system/config-backup')
  return { ...res, pools: res.pools ?? [] }
}

export async function getConfigBackups(o: Opts = {}): Promise<ConfigBackupsResponse> {
  const res = await request<ConfigBackupsResponse>('GET', '/system/config-backups', o)
  return { backups: res.backups ?? [] }
}

/**
 * Replaces the configuration with a backup's (202), then the daemon restarts: every session
 * ends. `confirm` must be "restore". 503 backup.unavailable in demo mode.
 */
export async function restoreConfig(pool: string, name: string, confirm: string): Promise<ConfigRestoreResult> {
  const res = await request<ConfigRestoreResult>('POST', '/system/config-restore', { body: { pool, name, confirm } })
  return { ...res, users: res.users ?? [], conflicts: res.conflicts ?? [] }
}

// ---- Copying data from a disk (M2e)

export async function getImportSources(o: Opts = {}): Promise<ImportSourcesResponse> {
  const res = await request<ImportSourcesResponse>('GET', '/import/sources', o)
  return { sources: (res.sources ?? []).map((src) => ({ ...src, disks: src.disks ?? [] })) }
}

/** Mounts a source read-only (never modified). */
export async function mountImport(uuid: string): Promise<ImportMounted> {
  const res = await request<ImportMounted>('POST', '/import/mount', { body: { uuid } })
  return { ...res, entries: res.entries ?? [] }
}

/** 409 apps.job_running while a copy reads the source. */
export const unmountImport = (uuid: string) => request<{ unmounted: boolean }>('POST', '/import/unmount', { body: { uuid } })

/** Starts the copy into a new folder of the share (202). */
export const startImportCopy = (body: ImportCopyRequest) => request<{ dest: string }>('POST', '/import/copy', { body })

export async function getImportProgress(share: string, o: Opts = {}): Promise<ImportProgress> {
  const res = await request<ImportProgress>('GET', `/import/progress/${seg(share)}`, o)
  return { ...res, job: normalizeJob(res.job) }
}

/** SSE stream of `event: metrics` (EventSource needs a URL, not a fetch). */
export const EVENTS_URL = '/api/v1/events'

export const getCertificate = (o: Opts = {}) => request<CertificateView>('GET', '/system/certificate', o)

export const renewCertificate = () => request<CertificateView>('POST', '/system/certificate/renew')
