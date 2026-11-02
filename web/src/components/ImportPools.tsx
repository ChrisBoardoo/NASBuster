// SPDX-License-Identifier: AGPL-3.0-or-later
// "Pools found on the disks" (ADR-0014): CWNSB pools this NAS doesn't know, after a reinstall
// (dead SD card) or on disks from another CWNSB NAS. Importing reads a pool as it is: nothing
// is written to its disks. A pool with missing disks waits until they're plugged back.
// Not a <form>: it also opens inside the onboarding's pool step, which is one already.
import { HardDriveDownload, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../api/client'
import { importPool } from '../api/endpoints'
import type { Disk, ImportCandidate, Pool } from '../api/types'
import { diskTitle } from '../lib/disks'
import { useErrorMessage } from '../lib/messages'
import { wipeSuffix } from '../lib/repair'
import { usePoolText } from '../lib/usePoolText'
import { Button } from './Button'
import { ConfirmName } from './ConfirmName'
import { Icon } from './Icon'
import { Notice } from './Notice'
import { StatusLabel } from './StatusLabel'

interface ImportPoolsProps {
  candidates: readonly ImportCandidate[]
  /** To name the disks (model + end of the id); ids alone otherwise. */
  disks?: readonly Disk[]
  onImported: (pool: Pool) => void
  /** Looks for the disks again (after plugging one back). */
  onRefresh?: () => void
  refreshing?: boolean
}

export function ImportPools({ candidates, disks, onImported, onRefresh, refreshing = false }: ImportPoolsProps) {
  const [open, setOpen] = useState<string | null>(null)
  return (
    <ul className="border-b border-border">
      {candidates.map((candidate) => (
        <Candidate
          key={candidate.name}
          candidate={candidate}
          disks={disks}
          open={open === candidate.name}
          onOpen={() => setOpen(candidate.name)}
          onClose={() => setOpen(null)}
          onImported={onImported}
          onRefresh={onRefresh}
          refreshing={refreshing}
        />
      ))}
    </ul>
  )
}

interface CandidateProps {
  candidate: ImportCandidate
  disks?: readonly Disk[]
  open: boolean
  onOpen: () => void
  onClose: () => void
  onImported: (pool: Pool) => void
  onRefresh?: () => void
  refreshing: boolean
}

function Candidate({ candidate, disks, open, onOpen, onClose, onImported, onRefresh, refreshing }: CandidateProps) {
  const { t } = useTranslation()
  const text = usePoolText()
  const errorMessage = useErrorMessage()
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const confirmed = typed === candidate.name
  const tech = text.layoutTech(candidate.layout)
  const byId = new Map((disks ?? []).map((disk) => [disk.id, disk]))

  const runImport = async () => {
    if (!confirmed || busy) return
    setBusy(true)
    setError(null)
    try {
      onImported(await importPool(candidate.name, typed))
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  const message =
    error instanceof ApiError && error.code === 'storage.pool_exists'
      ? t('import.exists', { name: candidate.name })
      : errorMessage(error)

  return (
    <li className="grid gap-x-12 gap-y-6 border-t border-border py-8 lg:grid-cols-12">
      <div className="flex min-w-0 flex-col gap-3 lg:col-span-5">
        <h3 className="text-4xl leading-none font-semibold tracking-tighter break-all md:text-5xl">{candidate.name}</h3>
        <p className="text-muted-foreground">
          {text.layoutTitle(candidate.layout)}
          {/* The technical name says it all ("Btrfs RAID1"); the kind only when it's unknown. */}
          <span className="font-mono text-sm">
            {' · '}
            {tech || (candidate.kind === 'md' ? t('import.kindMd') : t('import.kindBtrfs'))}
          </span>
        </p>
        <StatusLabel
          status={candidate.complete ? 'ok' : 'warn'}
          label={t('import.present', { present: candidate.present, count: candidate.expected })}
        />
      </div>

      <div className="flex min-w-0 flex-col gap-5 lg:col-span-7">
        <ul className="flex flex-col">
          {candidate.disks.map((id) => {
            const disk = byId.get(id)
            return (
              <li key={id} className="flex flex-col border-t border-border py-2 first:border-t-0 first:pt-0">
                <span className="font-semibold">{disk ? diskTitle(disk, t('common.unknown')) : `…${wipeSuffix(id)}`}</span>
                <span className="font-mono text-xs break-all text-muted-foreground">{id}</span>
              </li>
            )
          })}
        </ul>

        {!candidate.complete ? (
          <Notice
            status="warn"
            title={t('import.incompleteTitle', { missing: candidate.expected - candidate.present, count: candidate.expected - candidate.present })}
            actions={
              onRefresh ? (
                <Button variant="ghost" size="sm" className="-ml-3" icon={<Icon icon={RefreshCw} />} busy={refreshing} onClick={onRefresh}>
                  {t('import.lookAgain')}
                </Button>
              ) : undefined
            }
          >
            {t('import.incompleteBody')}
          </Notice>
        ) : !open ? (
          <div>
            <Button variant="secondary" size="sm" icon={<Icon icon={HardDriveDownload} />} onClick={onOpen}>
              {t('import.button')}
            </Button>
          </div>
        ) : (
          <div
            role="group"
            aria-label={t('import.title', { name: candidate.name })}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && event.target instanceof HTMLInputElement) {
                event.preventDefault()
                void runImport()
              }
            }}
            className="flex flex-col gap-5 border-l-2 border-accent pl-5"
          >
            <p className="text-2xl font-semibold tracking-tight">{t('import.title', { name: candidate.name })}</p>
            <Notice status="info" title={t('import.nothingWritten')}>
              {t('import.nothingWrittenBody')}
            </Notice>
            <ConfirmName name={candidate.name} value={typed} onChange={setTyped} />
            {error !== null && (
              <Notice status="crit" assertive>
                <span className="text-foreground">{message}</span>
              </Notice>
            )}
            <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
              <Button icon={<Icon icon={HardDriveDownload} />} busy={busy} disabled={!confirmed} onClick={() => void runImport()}>
                {busy ? t('import.importing') : t('import.confirm', { name: candidate.name })}
              </Button>
              <Button variant="ghost" className="-ml-4" onClick={onClose} disabled={busy}>
                {t('common.cancel')}
              </Button>
            </div>
          </div>
        )}
      </div>
    </li>
  )
}
