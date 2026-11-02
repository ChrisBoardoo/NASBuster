// SPDX-License-Identifier: AGPL-3.0-or-later
// Disk failure (ADR-0012): which member is OK, missing, failing or being rebuilt; what the
// pool page should offer; honest progress estimates; the wipe confirmation suffix.
import type { Disk, MdStatus, Pool, PoolDevice, PoolStatus } from '../api/types'
import { diskTitle } from './disks'

export type MemberState = 'ok' | 'present' | 'missing' | 'failing' | 'rebuilding'

export interface MemberView {
  /** Stable by-id id from `pool.members`. */
  id: string
  /** The disk, when it is connected (a missing one isn't in the disk list). */
  disk: Disk | null
  state: MemberState
  /** Btrfs error counters of its device (0 if none or unknown). */
  errors: number
  /** Its partition size, when the pool reports it. */
  sizeBytes: number | null
}

/** "/dev/sda1" belongs to "/dev/sda"; "/dev/nvme0n1p1" to "/dev/nvme0n1". */
function partitionOf(devicePath: string, diskPath: string): boolean {
  if (!diskPath || !devicePath.startsWith(diskPath)) return false
  return /^p?\d+$/.test(devicePath.slice(diskPath.length))
}

const totalErrors = (device: PoolDevice) =>
  device.errors.write + device.errors.read + device.errors.flush + device.errors.corruption + device.errors.generation

/** What happens to the disk freshly put in by the last replacement (kept for this tab only). */
const REBUILD_KEY = 'cwnsb.rebuilding'

export function rememberRebuild(pool: string, disk: string): void {
  try {
    const all = JSON.parse(window.sessionStorage.getItem(REBUILD_KEY) ?? '{}') as Record<string, string>
    all[pool] = disk
    window.sessionStorage.setItem(REBUILD_KEY, JSON.stringify(all))
  } catch {
    /* storage unavailable: the member just won't be labelled "being rebuilt" */
  }
}

function rebuildingDisk(pool: string): string | null {
  try {
    const all = JSON.parse(window.sessionStorage.getItem(REBUILD_KEY) ?? '{}') as Record<string, string>
    return all[pool] ?? null
  } catch {
    return null
  }
}

export function isRebuilding(status: PoolStatus | null | undefined): boolean {
  return Boolean(status && (status.replace?.state === 'running' || status.md?.sync?.action === 'recovery'))
}

/** Each member of the pool with its state, mapped through the disk list (members are by-id ids). */
export function memberViews(pool: Pool, disks: readonly Disk[] | undefined): MemberView[] {
  const status = pool.status
  const byId = new Map((disks ?? []).map((disk) => [disk.id, disk]))
  const rebuilding = isRebuilding(status)
  const newDisk = rebuildingDisk(pool.name)

  return pool.members.map((id) => {
    const disk = byId.get(id) ?? null
    if (!disk) return { id, disk: null, state: disks ? 'missing' : 'present', errors: 0, sizeBytes: null }
    if (!status || !status.mounted) return { id, disk, state: 'present', errors: 0, sizeBytes: null }

    if (status.md) {
      const partitions = status.md.members.filter((name) => partitionOf(`/dev/${name}`, disk.devPath))
      const faulty = (status.md.faulty ?? []).some((name) => partitionOf(`/dev/${name}`, disk.devPath))
      // A broken array (ADR-0032) does not take back a disk that returns: it is connected,
      // waiting for the reassembly, not missing.
      const outside: MemberState = status.reassemble ? 'present' : 'missing'
      const state: MemberState = faulty
        ? 'failing'
        : rebuilding && id === newDisk
          ? 'rebuilding'
          : partitions.length === 0
            ? outside
            : 'ok'
      return { id, disk, state, errors: 0, sizeBytes: null }
    }

    const device = status.devices.find((d) => !d.missing && partitionOf(d.path, disk.devPath)) ?? null
    if (!device) {
      // Present but not (yet) part of the file system: the target of a running replace.
      const state: MemberState = rebuilding ? 'rebuilding' : (status.missingDevices ?? 0) > 0 ? 'missing' : 'present'
      return { id, disk, state, errors: 0, sizeBytes: null }
    }
    const errors = totalErrors(device)
    return {
      id,
      disk,
      state: errors > 0 ? 'failing' : 'ok',
      errors,
      sizeBytes: device.sizeBytes && device.sizeBytes > 0 ? device.sizeBytes : null,
    }
  })
}

/** Size a replacement must reach: the old member's partition, when the pool reports it. */
export function requiredBytes(member: MemberView | undefined): number | null {
  return member?.sizeBytes ?? null
}

/** Partition of a candidate disk, like the server lays it out (1 MiB + 100 MiB + backup GPT). */
export function usableOnDisk(diskBytes: number): number {
  return Math.max(0, diskBytes - (1 << 20) - 100 * (1 << 20) - 33 * 512)
}

/** Members an array of this level can lose and still serve its data (storage.MDTolerance; unknown: 0). */
function mdTolerance(level: string, members: number): number {
  switch (level) {
    case 'raid1':
      return members - 1
    case 'raid4':
    case 'raid5':
    case 'raid10':
      return 1
    case 'raid6':
      return 2
  }
  return 0
}

/**
 * Lost more members than it tolerates (storage.MDStatus.Broken: kernel 5.15 still says
 * "active"), or held without running after a reboot ("inactive").
 */
export function mdDown(md: MdStatus): boolean {
  if (md.state === 'broken' || md.state === 'inactive') return true
  return md.expected > 0 && md.expected - md.active > mdTolerance(md.level, md.expected)
}

export type RepairKind =
  | 'reassemble'
  | 'outOfService'
  | 'mountDegraded'
  | 'resume'
  | 'replace'
  | 'progress'
  | 'restore'
  | 'errors'
  | 'resync'
  | 'leaveDegraded'
  | null

/** The one thing the pool page should put forward, from most to least urgent. */
export function repairKind(status: PoolStatus | null | undefined, members: readonly MemberView[]): RepairKind {
  if (!status) return null
  // More disks lost than the parity tolerates (ADR-0032): all back → reassemble;
  // otherwise out of service, never "take back" disk by disk.
  if (status.canReassemble) return 'reassemble'
  // A stopped array is out of service even when its superblocks could not be read
  // (`reassemble` null): the helper refuses "take back" and "replace" on it.
  const down = Boolean(status.reassemble?.reason) || (status.md !== null && mdDown(status.md))
  if (!status.mounted) {
    if (status.canMountDegraded) return 'mountDegraded'
    return down ? 'outOfService' : null
  }
  if (down) return 'outOfService'
  // After a reassembly, md repairs the parity before the scrub can check the files.
  if (isRebuilding(status) || status.balance || status.md?.sync?.action === 'repair') return 'progress'
  // The disk is back: taking it back beats replacing it (ADR-0027).
  if (status.canResume) return 'resume'
  const broken =
    (status.missingDevices ?? 0) > 0 || (status.md?.faulty?.length ?? 0) > 0 || members.some((m) => m.state === 'missing')
  if (broken) return 'replace'
  if (status.mixedProfiles) return 'restore'
  if (status.health === 'errors') return 'errors'
  // The two marks go together; either one alone still owes the check.
  if (status.resyncNeeded || status.repairNeeded) return 'resync'
  if (status.mountedDegraded && status.health === 'ok') return 'leaveDegraded'
  return null
}

/** Progress of the background work, 0–100, with what it is. */
export function backgroundWork(
  status: PoolStatus,
): { kind: 'replace' | 'recovery' | 'parity' | 'balance'; percent: number } | null {
  if (status.replace?.state === 'running') return { kind: 'replace', percent: status.replace.percent }
  if (status.md?.sync?.action === 'recovery') return { kind: 'recovery', percent: status.md.sync.percent }
  if (status.md?.sync?.action === 'repair') return { kind: 'parity', percent: status.md.sync.percent }
  if (status.balance) {
    const fromChunks = status.balance.total > 0 ? (status.balance.done / status.balance.total) * 100 : null
    return { kind: 'balance', percent: fromChunks ?? 100 - status.balance.percentLeft }
  }
  return null
}

export interface ProgressSample {
  /** ms */
  at: number
  percent: number
}

/**
 * Seconds left, from the observed rate only: at least 2 samples 10 s apart with progress
 * between them. No guess otherwise.
 */
export function estimateSecondsLeft(samples: readonly ProgressSample[]): number | null {
  const first = samples[0]
  const last = samples[samples.length - 1]
  if (!first || !last || last.at - first.at < 10_000) return null
  const rate = (last.percent - first.percent) / ((last.at - first.at) / 1000)
  if (!(rate > 0) || last.percent >= 100) return null
  return (100 - last.percent) / rate
}

/** A clean scrub that finished less than 48 h ago allows resetting the counters. */
export function scrubAllowsClear(status: PoolStatus, nowMs: number): boolean {
  const started = status.scrub.startedAt ?? 0
  return (
    status.scrub.status === 'finished' &&
    status.scrub.uncorrectable === 0 &&
    started > 0 &&
    nowMs / 1000 - started < 48 * 3600
  )
}

/**
 * The 4 characters typed before erasing a disk: the end of its id, without the USB LUN
 * suffix ("…ABCDEF-0:0" → "CDEF", "wwn-0x…4567" → "4567").
 */
export function wipeSuffix(id: string): string {
  const base = id.replace(/-\d+:\d+$/, '')
  return base.slice(-4)
}

/** 'Seagate ST4000VN008-2DR166 (…4567)', or the full id when the disk isn't connected. */
export function memberLabel(member: { id: string; disk: Disk | null }, unknown: string): string {
  return member.disk ? `${diskTitle(member.disk, unknown)} (…${wipeSuffix(member.id)})` : member.id
}
