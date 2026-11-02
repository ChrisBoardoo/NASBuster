// SPDX-License-Identifier: AGPL-3.0-or-later
// One disk = one full-width row (no heavy cards): huge size, identity, role, warnings,
// and a lazily loaded SMART panel.
import { Activity, CircleCheck, CircleHelp, Eraser, HardDrive, Layers, Lock, type LucideIcon, Shield } from 'lucide-react'
import { type ReactNode, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Disk, DiskRole } from '../api/types'
import { cn } from '../lib/cn'
import { diskTitle } from '../lib/disks'
import { diskAnchorId } from '../lib/pools'
import { useDynamicT } from '../lib/messages'
import { useFormat } from '../lib/useFormat'
import { Button } from './Button'
import { Icon } from './Icon'
import { LocateDisk } from './LocateDisk'
import { Notice } from './Notice'
import { SmartPanel } from './SmartPanel'
import { StatusLabel } from './StatusLabel'
import { PosterNumber } from './Typography'
import { WipePanel } from './WipePanel'

/** Re-exported for existing callers. */
export { diskTitle }

const ROLE_ICONS: Record<DiskRole, LucideIcon> = {
  system: Lock,
  protected: Shield,
  in_use: HardDrive,
  available: CircleCheck,
}

const ROLE_COLORS: Record<DiskRole, string> = {
  system: 'text-status-info',
  protected: 'text-status-info',
  in_use: 'text-status-info',
  available: 'text-status-ok',
}

function isRole(role: string): role is DiskRole {
  return role in ROLE_ICONS
}

export function RoleLabel({ role }: { role: string }) {
  const { td, has } = useDynamicT()
  const known = isRole(role)
  const key = `disks.role.${role}`
  return (
    <span
      className={cn(
        'label-mono inline-flex items-center gap-1.5',
        known ? ROLE_COLORS[role] : 'text-status-unknown',
      )}
    >
      <Icon icon={known ? ROLE_ICONS[role] : CircleHelp} size={14} className="shrink-0" />
      <span>{has(key) ? td(key) : role}</span>
    </span>
  )
}

interface DiskRowProps {
  disk: Disk
  compact?: boolean
  /** Give the row a DOM id so pool members can link to it (Storage page). */
  anchor?: boolean
  /** Name of the pool this disk belongs to, if any. */
  poolName?: string
  /** Offers "Erase…" on disks in use (Storage page); called after a wipe. */
  onChanged?: () => void
}

export function DiskRow({ disk, compact = false, anchor = false, poolName, onChanged }: DiskRowProps) {
  const { t } = useTranslation()
  const format = useFormat()
  const size = format.bytesParts(disk.sizeBytes)
  const title = diskTitle(disk, t('common.unknown'))

  if (compact) {
    return (
      <li className="grid grid-cols-[minmax(6.5rem,auto)_1fr] items-baseline gap-x-6 gap-y-1 border-t border-border py-5">
        <PosterNumber
          value={size.value}
          unit={size.unit}
          className="text-4xl leading-none font-semibold tracking-tighter"
          unitClassName="text-xs md:text-xs"
        />
        <div className="flex min-w-0 flex-col gap-1.5">
          <p className="truncate text-lg font-semibold tracking-tight">{title}</p>
          <p className="truncate font-mono text-xs text-muted-foreground">{disk.id}</p>
          <div className="flex flex-wrap gap-x-5 gap-y-1">
            <RoleLabel role={disk.role} />
            {disk.warnings.length > 0 && (
              <StatusLabel status="warn" label={t('disks.warnings', { count: disk.warnings.length })} />
            )}
          </div>
        </div>
      </li>
    )
  }

  return <FullDiskRow disk={disk} title={title} size={size} anchor={anchor} poolName={poolName} onChanged={onChanged} />
}

interface FullDiskRowProps {
  disk: Disk
  title: string
  size: { value: string; unit: string }
  anchor: boolean
  poolName?: string
  onChanged?: () => void
}

function FullDiskRow({ disk, title, size, anchor, poolName, onChanged }: FullDiskRowProps) {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()
  const smartId = useId()
  const [smartOpen, setSmartOpen] = useState(false)
  const [smartLoaded, setSmartLoaded] = useState(false)

  const roleHintKey = `disks.roleHint.${disk.role}`
  const transportKey = `disks.transport.${disk.transport}`
  const busParts = [
    has(transportKey) ? td(transportKey) : disk.transport,
    // usb-storage reports every stick as rotational: a removable USB disk is a stick.
    disk.transport === 'usb' && disk.removable ? t('disks.usbStick') : disk.rotational ? t('disks.spinning') : t('disks.solidState'),
    disk.removable && disk.transport !== 'usb' ? t('disks.removable') : null,
  ].filter(Boolean)

  const driver = disk.usb?.driver ?? ''
  const driverLabel = driver === 'uas' ? t('disks.uas') : driver === 'usb-storage' ? t('disks.usbStorage') : driver

  const [wiping, setWiping] = useState(false)
  // Disks in use can be erased, except a member of a pool (the server refuses it anyway).
  const canWipe = Boolean(onChanged) && disk.role === 'in_use' && !poolName

  const toggleSmart = () => {
    setSmartOpen((open) => !open)
    setSmartLoaded(true)
  }

  return (
    <li
      id={anchor ? diskAnchorId(disk.id) : undefined}
      tabIndex={anchor ? -1 : undefined}
      className="grid scroll-mt-6 gap-x-12 gap-y-6 border-t border-border py-8 transition-colors duration-500 ease-crisp focus:bg-muted md:py-10 lg:grid-cols-12"
    >
      <div className="flex flex-col gap-4 lg:col-span-4">
        <PosterNumber
          value={size.value}
          unit={size.unit}
          className="text-6xl leading-none font-semibold tracking-tighter lg:text-7xl"
        />
        <div className="flex flex-col gap-2">
          <RoleLabel role={disk.role} />
          {poolName && (
            <span className="label-mono inline-flex items-center gap-1.5 text-foreground">
              <Icon icon={Layers} size={14} className="shrink-0" />
              {t('pools.memberOf', { name: poolName })}
            </span>
          )}
          {has(roleHintKey) && <p className="max-w-sm text-sm text-muted-foreground">{td(roleHintKey)}</p>}
        </div>
      </div>

      <div className="flex min-w-0 flex-col gap-6 lg:col-span-8">
        <h3 className="text-2xl font-semibold tracking-tight break-words md:text-3xl">{title}</h3>

        <dl className="grid grid-cols-1 gap-x-6 gap-y-3 text-sm sm:grid-cols-[minmax(8rem,auto)_1fr]">
          <DetailRow label={t('disks.stableId')}>
            <span className="font-mono break-all">{disk.id}</span>
          </DetailRow>
          {disk.devPath && (
            <DetailRow label={t('disks.device')}>
              <span className="font-mono">{disk.devPath}</span>
            </DetailRow>
          )}
          <DetailRow label={t('disks.bus')}>{busParts.join(' · ')}</DetailRow>
          {disk.usb && (
            <DetailRow label={t('disks.usbDriver')}>
              <span>{driverLabel || t('common.unknown')}</span>
              {(disk.usb.vendorId || disk.usb.productId) && (
                <span className="text-muted-foreground">
                  {' '}
                  · {t('disks.usbBridge')}{' '}
                  <span className="font-mono">
                    {disk.usb.vendorId}:{disk.usb.productId}
                  </span>
                </span>
              )}
            </DetailRow>
          )}
          {disk.serial && (
            <DetailRow label={t('disks.serial')}>
              <span className="font-mono break-all">{disk.serial}</span>
            </DetailRow>
          )}
        </dl>

        {disk.usage.length > 0 && (
          <div className="flex flex-col gap-2">
            <h4 className="label-mono text-muted-foreground">{t('disks.usageTitle')}</h4>
            <ul className="flex flex-col gap-1">
              {disk.usage.map((usage, index) => {
                const key = `disks.usage.${usage.kind}`
                return (
                  <li key={`${usage.kind}-${index}`} className="flex flex-wrap gap-x-3">
                    <span>{has(key) ? td(key) : usage.kind}</span>
                    {usage.detail && <span className="font-mono text-sm break-all text-muted-foreground">{usage.detail}</span>}
                  </li>
                )
              })}
            </ul>
          </div>
        )}

        {disk.partitions.length > 0 && (
          <div className="flex flex-col gap-2">
            <h4 className="label-mono text-muted-foreground">{t('disks.partitions')}</h4>
            <ul className="flex flex-col gap-2">
              {disk.partitions.map((partition) => (
                <li key={partition.name} className="flex flex-col">
                  <span className="font-mono text-sm">
                    {t('disks.partitionLine', {
                      name: partition.name,
                      size: format.bytes(partition.sizeBytes),
                      fstype: partition.fstype || t('disks.noFs'),
                    })}
                    {partition.label && <span className="text-muted-foreground"> · {partition.label}</span>}
                  </span>
                  {partition.mountpoints.length > 0 && (
                    <span className="font-mono text-xs break-all text-muted-foreground">
                      {t('disks.mountedAt')} {partition.mountpoints.join(', ')}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}

        {disk.warnings.length > 0 && (
          <div className="flex flex-col gap-3">
            {disk.warnings.map((warning) => {
              const key = `disks.warning.${warning}`
              return (
                <Notice key={warning} status="warn">
                  {has(key) ? td(key) : warning}
                </Notice>
              )
            })}
          </div>
        )}

        {disk.role !== 'system' && <LocateDisk diskId={disk.id} />}
        <div>
          <Button
            variant="ghost"
            size="sm"
            className="-ml-3"
            icon={<Icon icon={Activity} />}
            aria-expanded={smartOpen}
            aria-controls={smartId}
            onClick={toggleSmart}
          >
            {smartOpen ? t('smart.hide') : t('smart.button')}
          </Button>
        </div>
        {/* Fetched on first open, then kept mounted so toggling doesn't re-query smartctl. */}
        <div id={smartId} hidden={!smartOpen}>
          {smartLoaded && <SmartPanel diskId={disk.id} />}
        </div>
        {canWipe &&
          (wiping ? (
            <WipePanel
              disk={disk}
              onCancel={() => setWiping(false)}
              onWiped={() => {
                setWiping(false)
                onChanged?.()
              }}
            />
          ) : (
            <div>
              <Button variant="ghost" size="sm" className="-ml-3" icon={<Icon icon={Eraser} />} onClick={() => setWiping(true)}>
                {t('wipe.button')}
              </Button>
            </div>
          ))}
      </div>
    </li>
  )
}

function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="label-mono pt-0.5 text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  )
}
