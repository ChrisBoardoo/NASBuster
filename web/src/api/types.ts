// SPDX-License-Identifier: AGPL-3.0-or-later
// Types for the v1 API. Reference: md/8.architecture/api-v1.md (keep in sync).

export type Language = 'en' | 'fr'

export type HealthStatus = 'ok' | 'info' | 'warn' | 'crit' | 'unknown'

export interface ApiErrorBody {
  error: {
    code: string
    message: string
    params?: Record<string, unknown>
  }
}

// ---- Bootstrap -------------------------------------------------------------

export interface HealthResponse {
  status: string
  version: string
}

export interface User {
  id: number
  username: string
  role: string
  language: Language
  /** Unix seconds */
  createdAt: number
}

export interface UserResponse {
  user: User
}

export interface Bootstrap {
  version: string
  needsSetup: boolean
  authenticated: boolean
  user: User | null
  hostname: string
  demo: boolean
  labMode: boolean
  /** Once an admin exists: "Forgot password?" works with the machine account (ADR-0037). */
  machineReset?: boolean
}

// ---- Health checks ---------------------------------------------------------

export type CheckId =
  | 'model'
  | 'arch'
  | 'memory'
  | 'temperature'
  | 'power'
  | 'usbPower'
  | 'throttling'
  | 'bootDevice'
  | 'clock'

export interface Check {
  id: CheckId | (string & {})
  status: HealthStatus
  /** Stable code, translated with the i18n key `checks.<code>` (e.g. `bootDevice.sd_card`). */
  code: string
  params: Record<string, unknown>
}

export interface ChecksResponse {
  checks: Check[]
}

// ---- Setup / onboarding ----------------------------------------------------

/** Ownership is proven by the Pi's own account (ADR-0018) or by the setup code. */
export interface CreateAdminRequest {
  machineUser?: string
  machinePassword?: string
  setupCode?: string
  username: string
  password: string
  language: Language
}

export const ONBOARDING_STEPS = [
  'welcome',
  'health',
  'account',
  'identity',
  'disks',
  'protection',
  'confirm',
  'create',
  'share',
  'apps',
  'safety',
  'done',
] as const

export type OnboardingStep = (typeof ONBOARDING_STEPS)[number]

export interface Onboarding {
  completed: boolean
  currentStep: OnboardingStep
  completedSteps: OnboardingStep[]
}

// ---- Auth ------------------------------------------------------------------

export interface LoginRequest {
  username: string
  password: string
}

/** A forgotten admin password, reset with the machine account (ADR-0037). */
export interface ResetPasswordRequest {
  machineUser: string
  machinePassword: string
  /** Which admin, when there are several. */
  username?: string
  password: string
}

export interface ChangePasswordRequest {
  currentPassword: string
  password: string
}

export interface AuthSettings {
  /** "Forgot password?" on the login screen; off: the console command only. */
  machineReset: boolean
}

// ---- System ----------------------------------------------------------------

export type BootDeviceKind = 'sd_card' | 'ssd' | 'other' | 'unknown'

export interface SystemInfo {
  hostname: string
  model: string
  isRaspberryPi: boolean
  os: string
  kernel: string
  arch: string
  cpuCores: number
  memTotalBytes: number
  uptimeSeconds: number
  timezone: string
  bootDevice: { kind: BootDeviceKind; device: string }
}

export interface MetricsSample {
  /** Unix seconds */
  ts: number
  cpuPercent: number
  memUsedBytes: number
  memTotalBytes: number
  /** null when no sensor is available */
  tempCelsius: number | null
  load1: number
}

export interface MetricsResponse {
  current: MetricsSample
  history: MetricsSample[]
}

export interface TimezonesResponse {
  timezones: string[]
}

export interface Identity {
  hostname: string
  timezone: string
}

export interface AuditEntry {
  id: number
  ts: number
  actor: string
  action: string
  target: string
  result: string
  /** Free text: "old -> new", "automatic", the fault kind… */
  detail?: string
  remoteAddr: string
}

export interface AuditResponse {
  entries: AuditEntry[]
}

// ---- Storage ---------------------------------------------------------------

export type DiskTransport = 'usb' | 'sata' | 'nvme' | 'sd_card' | 'virtual' | 'loop' | 'unknown'
export type DiskRole = 'system' | 'in_use' | 'available' | 'protected'
export type DiskUsageKind =
  | 'mounted'
  | 'filesystem'
  | 'md_member'
  | 'btrfs_member'
  | 'lvm_member'
  | 'crypto'
  | 'swap'
  | 'partition_table'
  | 'duplicate_id'
export type DiskWarning = 'duplicate_serial' | 'no_stable_id' | 'usb_storage_driver' | 'usb2_link'

export interface DiskUsb {
  vendorId: string
  productId: string
  driver: string
  /** USB link speed: 480 (USB 2), 5000 or more (USB 3); 0 or absent if unknown. */
  speedMbps?: number
}

export interface DiskUsage {
  kind: DiskUsageKind
  detail: string
}

export interface Partition {
  name: string
  sizeBytes: number
  fstype: string
  label: string
  uuid: string
  mountpoints: string[]
}

export interface Disk {
  id: string
  byIdPath: string
  devPath: string
  model: string
  vendor: string
  serial: string
  wwn: string
  sizeBytes: number
  transport: DiskTransport
  rotational: boolean
  removable: boolean
  usb: DiskUsb | null
  role: DiskRole
  usage: DiskUsage[]
  partitions: Partition[]
  warnings: DiskWarning[]
}

export interface DisksResponse {
  disks: Disk[]
}

export type SmartUnavailableReason = 'usb_bridge_unsupported' | 'not_supported' | 'not_installed' | 'error'

export interface SmartAttribute {
  id: number
  name: string
  raw: number
  status: HealthStatus
}

export interface SmartNvme {
  percentageUsed: number
  mediaErrors: number
  criticalWarning: number
  availableSpare: number
}

export interface Smart {
  available: boolean
  reason: SmartUnavailableReason | ''
  deviceType?: string
  passed?: boolean
  temperatureCelsius?: number | null
  powerOnHours?: number | null
  attributes?: SmartAttribute[]
  nvme?: SmartNvme | null
}

// ---- Pools (M2, ADR-0008) --------------------------------------------------

export const POOL_LAYOUTS = ['single', 'mirror', 'raid10', 'parity1', 'parity2'] as const
export type PoolLayoutName = (typeof POOL_LAYOUTS)[number]

export interface PoolLayout {
  name: PoolLayoutName | (string & {})
  minDisks: number
  /** 0 = no limit */
  maxDisks: number
  /** Disks that can fail without data loss. */
  tolerates: number
  /** Copies repair corruption (Btrfs RAID1/10). */
  selfHealing: boolean
  /** mdadm RAID5/6 under Btrfs: corruption is detected, not repaired. */
  parity: boolean
}

export interface LayoutsResponse {
  layouts: PoolLayout[]
}

export type PlanWarning = 'no_redundancy' | 'mixed_sizes' | 'parity_detects_only'

export interface PoolPlanRequest {
  layout: string
  disks: string[]
}

/** Capacity preview: no effect on the disks. Sizes are estimates (no file system overhead). */
export interface PoolPlan {
  layout: string
  disks: number
  rawBytes: number
  usableBytes: number
  tolerates: number
  selfHealing: boolean
  warnings: Array<PlanWarning | (string & {})>
}

export interface CreatePoolRequest {
  name: string
  layout: string
  disks: string[]
  /** Must equal `name`, typed by the user. */
  confirm: string
}

export type PoolHealth = 'ok' | 'degraded' | 'errors' | 'unmounted' | 'unknown'

export interface PoolDeviceErrors {
  write: number
  read: number
  flush: number
  corruption: number
  generation: number
}

export interface PoolDevice {
  /** "/dev/sda1", or "devid:2" when the disk is missing. */
  path: string
  devId: number
  /** Partition size (0 when missing or unknown). */
  sizeBytes?: number
  missing?: boolean
  errors: PoolDeviceErrors
}

/** One line of `btrfs filesystem df` (logical bytes). */
export interface PoolProfile {
  type: string
  profile: string
  totalBytes: number
  usedBytes: number
}

export type MdSyncAction = 'resync' | 'recovery' | 'check' | 'reshape' | 'repair'

export interface MdSync {
  action: MdSyncAction | (string & {})
  percent: number
}

export interface MdStatus {
  device: string
  /** "active", "broken" (more members lost than it tolerates), "inactive" */
  state?: string
  level: string
  active: number
  expected: number
  /** Kernel names of the member partitions (sda1…). */
  members: string[]
  /** Members marked (F) in /proc/mdstat (kernel names). */
  faulty?: string[]
  sync: MdSync | null
  /** Idle, but a check or a repair stopped before its end (md/sync_min). */
  interrupted?: boolean
}

/** btrfs replace (ADR-0012). */
export interface ReplaceStatus {
  state: 'running' | 'finished' | 'canceled' | (string & {})
  percent: number
  writeErrors: number
  readErrors: number
  /** Unix seconds, 0 if unknown. */
  finishedAt?: number
}

/** btrfs balance (restoring redundancy after a degraded period). */
export interface BalanceStatus {
  state: 'running' | 'paused' | (string & {})
  done: number
  total: number
  percentLeft: number
}

export type ScrubState = 'never' | 'running' | 'finished' | 'aborted' | 'interrupted'

export interface PoolScrub {
  status: ScrubState | (string & {})
  /** As printed by `btrfs scrub status` (e.g. "Thu Oct  1 23:11:06 2026"), "" if never. */
  started: string
  /** "H:MM:SS", "" if never. */
  duration: string
  /** Unix seconds, 0 if unknown (preferred over the text field). */
  startedAt?: number
  /** 0 if unknown (preferred over the text field). */
  durationSeconds?: number
  corrected: number
  uncorrectable: number
  csumErrors: number
  readErrors: number
}

export interface PoolStatus {
  name: string
  layout: string
  uuid: string
  mountPoint: string
  mounted: boolean
  health: PoolHealth | (string & {})
  /** Raw device bytes (all members). */
  sizeBytes: number
  /** Raw device bytes. */
  usedBytes: number
  /** Estimated free space, accounting for the RAID profile. */
  freeBytes: number
  /** What the files take once (data + metadata, no GlobalReserve). */
  logicalUsedBytes?: number
  /** logicalUsedBytes + freeBytes: the capacity as users understand it. */
  usableBytes?: number
  missingBytes: number
  devices: PoolDevice[]
  profiles: PoolProfile[]
  md: MdStatus | null
  scrub: PoolScrub
  // ---- Disk failure (M2b, ADR-0012); absent on older servers.
  /** Expected disks that are absent, mounted or not. */
  missingDevices?: number
  /** Unmounted BECAUSE a disk is missing: offer "Mount in degraded mode". */
  canMountDegraded?: boolean
  /** Mounted with -o degraded (until the next normal mount). */
  mountedDegraded?: boolean
  /** Chunks left without redundancy (written while degraded). */
  mixedProfiles?: boolean
  replace?: ReplaceStatus | null
  balance?: BalanceStatus | null
  // ---- A disk back after being unplugged (ADR-0027); absent on older servers.
  /** A member is plugged in again but unused until the pool is remounted: offer "Take it back". */
  canResume?: boolean
  /** Degraded since the last scrub: both copies are checked by a scrub before "ok". */
  resyncNeeded?: boolean
  // ---- A parity pool that lost more disks than it tolerates (ADR-0032); absent on older servers.
  /** After a forced assembly: md owes a parity repair, started once every member is in sync, before the scrub. */
  repairNeeded?: boolean
  /** Every member is back: offer "Reassemble the pool" (forced assembly, parity repair, scrub). */
  canReassemble?: boolean
  /** What the members' own superblocks say, while the array is broken or not started. */
  reassemble?: Reassemble | null
}

export interface Reassemble {
  level: string
  expected: number
  /** Kernel names of the member partitions found. */
  members: string[]
  /** Members with the freshest superblock. */
  fresh: number
  /** Superblock updates between the freshest member and the stalest. */
  eventsGap: number
  /** "" when it can be reassembled; otherwise why not. */
  reason: '' | 'missing_disks' | 'roles' | 'events_gap' | (string & {})
}

export interface Pool {
  name: string
  layout: string
  uuid: string
  mdDevice: string
  /** Stable disk ids (same as Disk.id). */
  members: string[]
  /** Unix seconds */
  createdAt: number
  /** null when the helper can't be reached; statusError then holds an error code. */
  status: PoolStatus | null
  statusError: string
}

export interface PoolsResponse {
  pools: Pool[]
}

export interface PoolResponse {
  pool: Pool
}

export interface ScrubResponse {
  started: boolean
}

// ---- NAS users and shares (M4, ADR-0009) -------------------------------------

export type AccessMode = 'rw' | 'ro'

/** A person who opens shares from their computer (not the admin of this UI). */
export interface NasUser {
  name: string
  /** Unix seconds */
  createdAt: number
  /** Shares this user can reach. */
  shares: Array<{ share: string; mode: AccessMode | (string & {}) }>
}

export interface UsersResponse {
  users: NasUser[]
}

export interface NasUserResponse {
  user: NasUser
}

export interface ShareAccess {
  user: string
  mode: AccessMode | (string & {})
}

export interface Share {
  name: string
  pool: string
  /** /srv/cwnsb/<pool>/<share> */
  path: string
  /** Unix seconds */
  createdAt: number
  /** Empty: nobody can open the share. */
  access: ShareAccess[]
  /** Macs see it as a Time Machine destination; it gets no automatic snapshots. */
  timeMachine: boolean
  /** Time Machine size limit in GiB, 0 = no limit (always 0 when timeMachine is off). */
  timeMachineMaxGiB: number
  /** Deleted files go to a visible "#recycle" folder of the share… */
  recycle: boolean
  /** …and are purged after this many days (1–365; 0 when off). */
  recycleDays: number
}

export interface SharesResponse {
  shares: Share[]
}

export interface ShareResponse {
  share: Share
}

export interface CreateShareRequest {
  name: string
  pool: string
  access: ShareAccess[]
  timeMachine?: boolean
  /** ≤ TIME_MACHINE_MAX_GIB; a limit without Time Machine is refused (validation.time_machine). */
  timeMachineMaxGiB?: number
  recycle?: boolean
  /** 1–365 with the recycle bin, 0 without (validation.recycle). */
  recycleDays?: number
}

export interface SmbSession {
  user: string
  machine: string
  address: string
  dialect: string
  encrypted: boolean
  /** Unix seconds (RFC 3339 text from older servers). */
  since: number | string
}

export interface SmbConnection {
  share: string
  machine: string
  /** Unix seconds (RFC 3339 text from older servers). */
  since: number | string
}

export interface SessionsResponse {
  sessions: SmbSession[]
  connections: SmbConnection[]
}

// ---- Protection: snapshots and scheduled tasks (M6a, ADR-0010) ---------------

export interface RetentionPolicy {
  hourly: number
  daily: number
  weekly: number
  monthly: number
}

export interface Protection {
  snapshots: { enabled: boolean; policy: RetentionPolicy }
  scrub: { enabled: boolean; intervalDays: number }
}

export interface ProtectionState {
  /** Unix seconds, 0 if never. */
  lastSnapshotRun: number
  /** Pool name → Unix seconds. */
  lastScrub: Record<string, number>
}

export interface ProtectionResponse {
  protection: Protection
  state: ProtectionState
}

export interface Snapshot {
  /** Server-chosen, e.g. "@GMT-2026.10.01-21.00.00" (UTC). */
  name: string
  /** Unix seconds */
  time: number
}

export interface SnapshotsResponse {
  snapshots: Snapshot[]
}

// ---- Alerts and notifications (M6b, ADR-0011) --------------------------------

export type AlertLevel = 'crit' | 'warn'

/**
 * An alert is a state with a stable id (`<code>:<target>`), not an event. Text comes from
 * the UI's i18n (`alerts.codes.<code>`); `params.disk` is a full by-id disk id.
 */
export interface Alert {
  id: string
  level: AlertLevel | (string & {})
  code: string
  params: Record<string, unknown>
  /** Unix seconds */
  firstSeen: number
  lastSeen: number
  /** 0 while active */
  resolvedAt: number
  /** 0 until marked as seen */
  ackedAt: number
}

export interface AlertsResponse {
  alerts: Alert[]
}

// ---- Event journal (ADR-0031) -------------------------------------------------

export type JournalLevel = 'info' | 'warn' | 'error' | 'crit'

/**
 * One entry of the journal: the history, kept across reboots. Text comes from the UI's
 * lexicon (`journal.lexicon.<code>`, and `alerts.codes.<code>` for alert events).
 */
export interface JournalEvent {
  id: number
  /** Unix seconds */
  time: number
  /** "uncertain": taken before the clock was synchronized; "corrected": recomputed since. */
  timeState: 'exact' | 'uncertain' | 'corrected' | (string & {})
  bootId: string
  level: JournalLevel | (string & {})
  code: string
  /** "raised" / "resolved" for alerts, "" for a one-off event */
  phase: string
  /** "pool:tank", "disk:<by-id>", "system" */
  subject: string
  source: string
  params: Record<string, unknown>
}

export interface JournalResponse {
  events: JournalEvent[]
  /** id to pass as `before` for the next page, 0 at the end */
  next: number
}

/** The system journal kept across reboots (on by default). */
export interface JournalSettings {
  persistent: boolean
  /** NasBuster's journald drop-in is in place */
  active: boolean
  /** Another file sets Storage=: NasBuster leaves it alone ("" if none) */
  managedBy: string
}

/** Never contains the ntfy token: `tokenSet` only says whether one is saved. */
export interface NotificationsConfig {
  ntfy: { enabled: boolean; url: string; tokenSet: boolean }
  webhook: { enabled: boolean; url: string }
}

export interface NotificationsUpdate {
  /** token absent: keep the saved one; "": clear it; a value: replace it. */
  ntfy: { enabled: boolean; url: string; token?: string }
  webhook: { enabled: boolean; url: string }
}

export interface NotificationTestResult {
  channel: 'ntfy' | 'webhook' | (string & {})
  ok: boolean
  /** notify.http_status (+ status), notify.timeout, notify.unreachable, notify.invalid_url, notify.demo */
  code?: string
  status?: number
  /** Technical English text. */
  error?: string
}

// ---- Disk failure and replacement (M2b, ADR-0012) -------------------------------

export interface ReplaceRequest {
  /** A member's id (by-id). */
  old: string
  /** A blank disk's id. */
  new: string
  /** The pool name, typed by the user. */
  confirm: string
}

export interface ReplaceStarted {
  old: string
  new: string
  devId: number
  method: 'btrfs-replace' | 'md-add' | (string & {})
}

export interface RestoreStarted {
  resized: number
  /** false: nothing needed converting. */
  balanceStarted: boolean
  data: string
  metadata: string
}

export type DemoFaultKind = 'unplug' | 'replug' | 'errors' | 'foreign' | 'usbkey'

export interface PoolDestroyResult {
  /** The empty pool was dismantled and its disks erased (they are `available` again). */
  destroyed: boolean
  /** It was unmounted with none of its disks present: only its configuration was removed. */
  forgotten: boolean
  disks: string[]
}

// ---- Applications: Docker and Compose (M5a, ADR-0013) ---------------------------

export type JobState = 'none' | 'running' | 'succeeded' | 'failed'

/** A long task (Docker installation, app deployment) run in a transient systemd unit. */
export interface Job {
  state: JobState | (string & {})
  exitStatus: number
  /** The end of its log. */
  log: string[]
}

export interface EngineStatus {
  installed: boolean
  /** Debian package version ("26.1.5+dfsg1-9+deb13u1"). */
  version: string
  active: boolean
  /** Where Docker keeps its data; "" when CWNSB didn't configure it. */
  pool: string
  /** The installation job. */
  job: Job
  /** While Docker isn't installed: pools holding an earlier installation's data, taken back if installed there (P9). */
  dataPools: EngineData[]
}

export interface EngineData {
  pool: string
  /** Containers in that data, started again by Docker if their restart policy says so. */
  containers: number
}

export interface AppTemplate {
  name: string
  title: string
  /** By language: en, fr. */
  description: Record<string, string>
  /** The app controls Docker entirely (Portainer). */
  expert: boolean
  webPort: number
  webScheme: 'http' | 'https' | (string & {})
  /** Prints a one-time setup token that its own setup screen asks for (Portainer). */
  setupToken?: boolean
  compose: string
}

export interface CatalogResponse {
  templates: AppTemplate[]
}

export interface AppPort {
  /** Bound address ("0.0.0.0", "::"). */
  url: string
  targetPort: number
  publishedPort: number
  protocol: 'tcp' | 'udp' | (string & {})
}

export type ContainerState = 'running' | 'exited' | 'restarting' | 'created' | 'paused' | 'dead'
export type ContainerHealth = '' | 'healthy' | 'unhealthy' | 'starting'

export interface AppContainer {
  name: string
  service: string
  image: string
  state: ContainerState | (string & {})
  health: ContainerHealth | (string & {})
  /** Docker's own words ("Up 3 minutes"), English only. */
  status: string
  ports: AppPort[]
}

export interface App {
  name: string
  pool: string
  /** Catalog entry, "" for a pasted compose file. */
  template: string
  compose: string
  createdAt: number
  updatedAt: number
  /** Compose status: "running(2)", "exited(1)", "running(1), exited(1)"; "" before the first start. */
  status: string
  containers: AppContainer[]
  /** Its deployment (or update) job. */
  job: Job
}

/** A Compose project created outside CWNSB (by hand, by Portainer): listed, not managed. */
export interface OtherProject {
  name: string
  status: string
  configFiles: string
  app: string
  containers: AppContainer[]
}

/** A CWNSB project whose app isn't recorded (ADR-0036): Docker restarted its containers by itself. */
export interface OrphanApp extends OtherProject {
  /** Pool of its compose file, "" when it isn't where NasBuster puts it. */
  pool: string
}

export type OrphanAction = 'stop' | 'remove'

export interface AppsResponse {
  engine: EngineStatus
  apps: App[]
  others: OtherProject[]
  orphans: OrphanApp[]
}

export interface CreateAppRequest {
  name: string
  pool: string
  /** A catalog entry… */
  template?: string
  /** …or a pasted compose file (64 KiB max). */
  compose?: string
}

export type AppAction = 'start' | 'stop' | 'restart' | 'update'

/**
 * GET /apps/{name}/setup-token: an app (Portainer) waiting for its admin account, which the
 * administrator creates in the app itself. The token is only sent while it can still be used.
 */
export interface AppSetupToken {
  state: 'none' | 'waiting' | 'locked' | 'created'
  token?: string
  /** When the app locks itself (ISO time); absent if unknown. */
  expiresAt?: string
}

// ---- Losing the SD card: pool import, configuration backup (ADR-0014) ------------------------

/** A CWNSB pool found on disks this NAS doesn't know (reinstall, disks from another NAS). */
export interface ImportCandidate {
  name: string
  kind: 'btrfs' | 'md' | (string & {})
  /** Best guess before import. */
  layout: string
  present: number
  expected: number
  complete: boolean
  disks: string[]
}

export interface ImportableResponse {
  pools: ImportCandidate[]
}

export interface ConfigBackupResult {
  /** "2026-10-02T01-30-00Z" */
  name: string
  /** Where it was stored. */
  pools: string[]
}

/** One copy of a backup on one pool (the same name on several pools = copies). */
export interface ConfigBackup {
  pool: string
  name: string
  /** Unix seconds */
  time: number
  hostname: string
  /** NAS users in it. */
  users: number
  sizeBytes: number
  /** Installation that made it; "" for backups older than installation ids (P6). */
  installation: string
  /** Admin account a restore brings back ("" when unknown). */
  admin: string
  /** Shares in it; -1 when unknown (older backups). */
  shares: number
  /** Made by this installation (its rotation keeps 7); others are never rotated. */
  thisInstallation: boolean
}

export interface ConfigBackupsResponse {
  backups: ConfigBackup[]
}

export interface ConfigRestoreResult {
  /** NAS accounts restored (or already right). */
  users: string[]
  /** Skipped, with the reason ("user alice: UID 1001 is taken by bob"). */
  conflicts: string[]
  /** The daemon restarts to load the backup: every session ends. */
  restart: boolean
}

// ---- Copying data from a disk: OMV, USB keys (M2e, ADR-0015) ------------------------------------

/** A file system with data to copy (never the system, a CWNSB pool, or one mounted elsewhere). */
export interface ImportSource {
  /** File system UUID: names the source in every call. */
  uuid: string
  fstype: string
  label: string
  /** Kernel name (sdb1, md127). */
  device: string
  sizeBytes: number
  /** by-id of the disks holding it (several for an md array). */
  disks: string[]
  /** Mounted read-only by CWNSB (under /run/cwnsb/import). */
  mounted: boolean
  mountPoint: string
}

export interface ImportSourcesResponse {
  sources: ImportSource[]
}

export interface ImportMounted {
  mountPoint: string
  /** Top-level names, 200 at most. */
  entries: string[]
}

export interface ImportCopyRequest {
  uuid: string
  share: string
  /** "" = everything; else a top-level folder of the source. */
  subdir: string
}

export interface ImportProgress {
  job: Job
  /** The source's UUID. */
  source: string
  /** The new folder in the share ("Import USB-KEY 2026-10-02 0905"). */
  dest: string
  /** 0 = unknown (huge tree): no percentage. */
  totalBytes: number
  copiedBytes: number
  files: number
}

// ---- HTTPS certificate (ADR-0019) -------------------------------------------

export interface CertificateView {
  /** False when the NAS serves plain HTTP (demo on this computer). */
  enabled: boolean
  /** SHA-256 of the certificate, as browsers show it ("8E:8F:69:…"). */
  fingerprint?: string
  dnsNames?: string[]
  ipAddresses?: string[]
  notBefore?: string
  notAfter?: string
  hostname?: string
  /** False after a rename: renewed at the next start, or now on request. */
  coversHostname: boolean
}
