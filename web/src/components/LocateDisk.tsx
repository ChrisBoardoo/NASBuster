// SPDX-License-Identifier: AGPL-3.0-or-later
// "Make its light blink" (POST /storage/disks/{id}/locate → 202 { seconds }): the NAS reads the
// disk for a while so its activity light blinks, to find which of several identical USB disks to
// pull. Read-only. Only one disk blinks at a time: pressing another moves the blinking, so the
// state is shared by every button on the page.
import { Lightbulb } from 'lucide-react'
import { useState, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../api/client'
import { locateDisk } from '../api/endpoints'
import { useCountdown } from '../lib/hooks'
import { useErrorMessage } from '../lib/messages'
import { cn } from '../lib/cn'
import { Button } from './Button'
import { Icon } from './Icon'

interface Blinking {
  disk: string
  /** Date.now() when it stops. */
  until: number
}

let blinking: Blinking | null = null
const listeners = new Set<() => void>()
const subscribe = (listener: () => void) => {
  listeners.add(listener)
  return () => listeners.delete(listener)
}
const setBlinking = (value: Blinking | null) => {
  blinking = value
  for (const listener of listeners) listener()
}

export function LocateDisk({ diskId, className }: { diskId: string; className?: string }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const current = useSyncExternalStore(subscribe, () => blinking)
  const mine = current?.disk === diskId ? current : null
  const secondsLeft = useCountdown(mine?.until ?? null)
  const active = mine !== null && secondsLeft > 0
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)

  const locate = async () => {
    setBusy(true)
    setError(null)
    try {
      const { seconds } = await locateDisk(diskId)
      setBlinking({ disk: diskId, until: Date.now() + (seconds > 0 ? seconds : 30) * 1000 })
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(false)
    }
  }

  const message =
    error instanceof ApiError && error.code === 'storage.disk_protected' ? t('locate.protected') : errorMessage(error)

  return (
    <div className={cn('flex flex-col items-start gap-1', className)}>
      <Button variant="ghost" size="sm" className="-ml-3" icon={<Icon icon={Lightbulb} />} busy={busy} onClick={() => void locate()}>
        {active ? t('locate.again') : t('locate.button')}
      </Button>
      <p className="text-sm empty:hidden" role="status">
        {active ? (
          <span className="flex flex-wrap items-center gap-x-2">
            <span aria-hidden="true" className="h-2 w-2 animate-blink bg-accent" />
            <span className="text-foreground">{t('locate.blinking', { count: secondsLeft })}</span>
          </span>
        ) : error !== null ? (
          <span className="text-status-crit">{message}</span>
        ) : (
          ''
        )}
      </p>
    </div>
  )
}
