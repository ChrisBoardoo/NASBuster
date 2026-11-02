// SPDX-License-Identifier: AGPL-3.0-or-later
// Demo mode only (bootstrap.demo): simulate hardware events to walk every failure state.
// POST /api/v1/demo/faults { kind: unplug | replug | errors | foreign, disk } — 404 outside demo.
// "foreign" puts a blank disk in an old mirror "oldnas" (two make it importable); "usbkey" turns a
// blank disk into an exFAT key with data to copy.
import { FlaskConical } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { demoFault } from '../api/endpoints'
import type { DemoFaultKind, Disk, Pool } from '../api/types'
import { diskTitle } from '../lib/disks'
import { useErrorMessage } from '../lib/messages'
import { wipeSuffix } from '../lib/repair'
import { Button } from './Button'
import { Icon } from './Icon'
import { Notice } from './Notice'

const UNPLUGGED_KEY = 'cwnsb.demo.unplugged'

function readUnplugged(): string[] {
  try {
    const list: unknown = JSON.parse(window.sessionStorage.getItem(UNPLUGGED_KEY) ?? '[]')
    return Array.isArray(list) ? list.map(String) : []
  } catch {
    return []
  }
}

interface DemoFaultsProps {
  pools: readonly Pool[] | undefined
  disks: readonly Disk[] | undefined
  onDone: () => void
}

export function DemoFaults({ pools, disks, onDone }: DemoFaultsProps) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [message, setMessage] = useState('')
  // Unplugged disks vanish from the disk list: remember them (for this tab, across pages)
  // so they can be plugged back even once they are no longer pool members.
  const [unplugged, setUnpluggedState] = useState<string[]>(readUnplugged)
  const setUnplugged = (update: (list: string[]) => string[]) =>
    setUnpluggedState((list) => {
      const next = update(list)
      try {
        window.sessionStorage.setItem(UNPLUGGED_KEY, JSON.stringify(next))
      } catch {
        /* storage unavailable: the list lives as long as the page */
      }
      return next
    })

  const members = new Set((pools ?? []).flatMap((pool) => pool.members))
  const present = (disks ?? []).filter((disk) => disk.role !== 'system')
  const presentIds = new Set(present.map((disk) => disk.id))
  const absent = [...new Set([...unplugged, ...members])].filter((id) => !presentIds.has(id))

  const run = async (kind: DemoFaultKind, id: string) => {
    setBusy(`${kind}:${id}`)
    setError(null)
    setMessage('')
    try {
      await demoFault(kind, id)
      if (kind === 'unplug') setUnplugged((list) => [...new Set([...list, id])])
      if (kind === 'replug') setUnplugged((list) => list.filter((x) => x !== id))
      setMessage(t(`demoFaults.done.${kind}`, { disk: `…${wipeSuffix(id)}` }))
      onDone()
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(null)
    }
  }

  return (
    <section aria-labelledby="demo-faults" className="mt-20 flex flex-col gap-5 border border-dashed border-border-hover p-6 md:p-8">
      <h2 id="demo-faults" className="label-mono flex items-center gap-2 text-muted-foreground">
        <Icon icon={FlaskConical} size={14} />
        {t('demoFaults.title')}
      </h2>
      <p className="max-w-2xl text-sm text-muted-foreground">{t('demoFaults.body')}</p>
      <ul className="border-b border-border">
        {present.map((disk) => (
          <li key={disk.id} className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 border-t border-border py-3">
            <span className="flex min-w-0 flex-col">
              <span className="font-semibold">{diskTitle(disk, t('common.unknown'))}</span>
              <span className="font-mono text-xs break-all text-muted-foreground">{disk.id}</span>
            </span>
            <span className="flex flex-wrap gap-x-2">
              <Button variant="ghost" size="sm" busy={busy === `unplug:${disk.id}`} disabled={busy !== null} onClick={() => void run('unplug', disk.id)}>
                {t('demoFaults.unplug')}
              </Button>
              {members.has(disk.id) && (
                <Button variant="ghost" size="sm" busy={busy === `errors:${disk.id}`} disabled={busy !== null} onClick={() => void run('errors', disk.id)}>
                  {t('demoFaults.errors')}
                </Button>
              )}
              {disk.role === 'available' && (
                <Button variant="ghost" size="sm" busy={busy === `foreign:${disk.id}`} disabled={busy !== null} onClick={() => void run('foreign', disk.id)}>
                  {t('demoFaults.foreign')}
                </Button>
              )}
              {disk.role === 'available' && (
                <Button variant="ghost" size="sm" busy={busy === `usbkey:${disk.id}`} disabled={busy !== null} onClick={() => void run('usbkey', disk.id)}>
                  {t('demoFaults.usbkey')}
                </Button>
              )}
            </span>
          </li>
        ))}
        {absent.map((id) => (
          <li key={id} className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 border-t border-border py-3">
            <span className="flex min-w-0 flex-col">
              <span className="font-semibold text-muted-foreground">{t('demoFaults.unpluggedDisk')}</span>
              <span className="font-mono text-xs break-all text-muted-foreground">{id}</span>
            </span>
            <Button variant="ghost" size="sm" busy={busy === `replug:${id}`} disabled={busy !== null} onClick={() => void run('replug', id)}>
              {t('demoFaults.replug')}
            </Button>
          </li>
        ))}
      </ul>
      <p className="text-sm text-foreground" role="status">
        {message}
      </p>
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
    </section>
  )
}
