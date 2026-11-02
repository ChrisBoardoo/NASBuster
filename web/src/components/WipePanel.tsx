// SPDX-License-Identifier: AGPL-3.0-or-later
// Erase a disk nothing uses (a new disk that came pre-formatted, an old pool member):
// shows what it contains in plain words, then the user types the last 4 characters of its id.
// POST /storage/disks/{id}/wipe { confirm: <full id> }. The server refuses anything in use.
import { Eraser } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { wipeDisk } from '../api/endpoints'
import type { Disk } from '../api/types'
import { diskTitle } from '../lib/disks'
import { useDynamicT, useErrorMessage } from '../lib/messages'
import { wipeSuffix } from '../lib/repair'
import { useFormat } from '../lib/useFormat'
import { Button } from './Button'
import { Field } from './Field'
import { Icon } from './Icon'
import { Notice } from './Notice'

interface WipePanelProps {
  disk: Disk
  /** What the helper found inside a new partition (pool creation refused): hidden from the disk view. */
  residual?: string
  onCancel: () => void
  /** Called after a successful wipe: refresh the disk list. */
  onWiped: () => void
}

export function WipePanel({ disk, residual, onCancel, onWiped }: WipePanelProps) {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const headingId = useId()
  const inputRef = useRef<HTMLInputElement>(null)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  // The id with the 4 characters to type highlighted (the USB LUN suffix "-0:0" stays after).
  const suffix = wipeSuffix(disk.id)
  const end = disk.id.replace(/-\d+:\d+$/, '').length
  const head = disk.id.slice(0, end - suffix.length)
  const tail = disk.id.slice(end)
  const confirmed = typed.trim().toLowerCase() === suffix.toLowerCase()

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  const erase = async () => {
    if (!confirmed) return
    setBusy(true)
    setError(null)
    try {
      await wipeDisk(disk.id, disk.id)
      onWiped()
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-5 border-l-2 border-status-crit pl-5">
      <h4 id={headingId} className="text-2xl font-semibold tracking-tight">
        {t('wipe.title', { disk: diskTitle(disk, t('common.unknown')) })}
      </h4>

      <div className="flex flex-col gap-2">
        <p className="label-mono text-muted-foreground">{t('wipe.contains')}</p>
        {residual && disk.partitions.length === 0 ? (
          <p>
            {t('wipe.residual', {
              found: has(`wipe.fs.${residual}`) ? td(`wipe.fs.${residual}`) : residual,
            })}
          </p>
        ) : disk.partitions.length === 0 && disk.usage.length === 0 ? (
          <p className="text-muted-foreground">{t('wipe.nothingVisible')}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {disk.partitions.map((partition) => (
              <li key={partition.name} className="flex flex-col">
                <span>
                  {partition.fstype
                    ? t('wipe.partitionFs', {
                        size: format.bytes(partition.sizeBytes),
                        fstype: has(`wipe.fs.${partition.fstype}`) ? td(`wipe.fs.${partition.fstype}`) : partition.fstype,
                      })
                    : t('wipe.partitionEmpty', { size: format.bytes(partition.sizeBytes) })}
                  {partition.label && <span className="text-muted-foreground"> · “{partition.label}”</span>}
                </span>
                {partition.mountpoints.length > 0 && (
                  <span className="font-mono text-xs break-all text-status-warn">
                    {t('wipe.mountedAt', { mount: partition.mountpoints.join(', ') })}
                  </span>
                )}
              </li>
            ))}
            {disk.partitions.length === 0 &&
              disk.usage.map((usage, index) => {
                const key = `disks.usage.${usage.kind}`
                return (
                  <li key={`${usage.kind}-${index}`}>
                    {has(key) ? td(key) : usage.kind}
                    {usage.detail && <span className="font-mono text-sm text-muted-foreground"> {usage.detail}</span>}
                  </li>
                )
              })}
          </ul>
        )}
      </div>

      <Notice status="crit" title={t('wipe.warningTitle')}>
        {t('wipe.warningBody')}
      </Notice>

      {/* Not a <form>: the panel also opens inside wizard steps, which are forms already.
          Destructive: Enter in the field never erases (nor submits the enclosing step);
          only the button does. */}
      <div
        role="group"
        aria-labelledby={headingId}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && event.target instanceof HTMLInputElement) event.preventDefault()
        }}
        className="flex flex-col gap-5"
      >
        <div className="flex flex-col gap-2">
          <span className="label-mono text-muted-foreground">{t('wipe.diskId')}</span>
          <code className="font-mono text-sm break-all">
            {head}
            <mark className="bg-accent px-0.5 text-accent-foreground">{suffix}</mark>
            {tail}
          </code>
        </div>
        <Field
          inputRef={inputRef}
          label={t('wipe.typeSuffix', { count: suffix.length })}
          name={`wipe-${disk.id}`}
          mono
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          maxLength={8}
          className="max-w-xs [&_input]:text-xl"
          value={typed}
          onChange={(event) => setTyped(event.target.value)}
          success={confirmed ? t('wipe.confirmed') : null}
        />
        {error !== null && (
          <Notice status="crit" assertive>
            <span className="text-foreground">{errorMessage(error)}</span>
          </Notice>
        )}
        <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
          <Button icon={<Icon icon={Eraser} />} busy={busy} disabled={!confirmed} onClick={() => void erase()}>
            {busy ? t('wipe.erasing') : t('wipe.erase')}
          </Button>
          <Button variant="ghost" className="-ml-4" onClick={onCancel} disabled={busy}>
            {t('common.cancel')}
          </Button>
        </div>
      </div>
    </section>
  )
}
