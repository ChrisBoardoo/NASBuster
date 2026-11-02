// SPDX-License-Identifier: AGPL-3.0-or-later
// "Install Docker": what it is, what it costs (RAM), where its data goes (a pool, never the SD
// card; the pool holding an earlier installation's data first, P9), then the installation followed
// live (GET /jobs/engine every 2 s while visible).
// Not a <form>: it also lives inside an onboarding step, which is one already.
import { Container as ContainerIcon, Download } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../../api/client'
import { getJob, installEngine } from '../../api/endpoints'
import type { EngineData, EngineStatus, Pool } from '../../api/types'
import { usePolling } from '../../api/usePolling'
import { DOCKER_RAM_MB, JOB_POLL_MS, NEW_POOL_PATH } from '../../lib/constants'
import { useErrorMessage } from '../../lib/messages'
import { Button, ButtonLink } from '../Button'
import { Icon } from '../Icon'
import { ScanLine } from '../Loading'
import { Notice } from '../Notice'
import { PoolSelect } from '../PoolSelect'
import { StatusLabel } from '../StatusLabel'
import { JobLog } from './JobLog'

interface EngineSetupProps {
  engine: EngineStatus
  pools: readonly Pool[] | undefined
  /** The installation succeeded (or Docker was already there): refresh. */
  onInstalled: () => void
  /** Offer "Create a pool" when there is none (not inside the onboarding). */
  offerPool?: boolean
}

/** Docker data an earlier installation left on mounted pools, the most containers first (P9). */
function mountedData(pools: readonly Pool[], data: readonly EngineData[]): EngineData[] {
  return data.filter((d) => pools.some((p) => p.name === d.pool && p.status?.mounted)).sort((a, b) => b.containers - a.containers)
}

/** Docker's own pool, else the one holding its data from before, else the first mounted one. */
function defaultPool(pools: readonly Pool[], preferred: string, data: readonly EngineData[]): string {
  const mounted = pools.filter((p) => p.status?.mounted)
  return mounted.find((p) => p.name === preferred)?.name ?? mountedData(pools, data)[0]?.pool ?? mounted[0]?.name ?? ''
}

export function EngineSetup({ engine, pools, onInstalled, offerPool = true }: EngineSetupProps) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [pool, setPool] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [watching, setWatching] = useState(engine.job.state === 'running')
  const poll = usePolling((signal) => getJob('engine', { signal }), JOB_POLL_MS, watching)
  const job = watching && poll.data ? poll.data : engine.job

  const poolList = pools ?? []
  const mounted = poolList.filter((p) => p.status?.mounted)
  const chosen = pool || defaultPool(poolList, engine.pool, engine.dataPools)
  const data = mountedData(poolList, engine.dataPools)
  const dataHere = data.find((d) => d.pool === chosen)
  const dataElsewhere = dataHere ? undefined : data[0]

  useEffect(() => {
    if (engine.job.state === 'running') setWatching(true)
  }, [engine.job.state])

  useEffect(() => {
    if (!watching || !poll.data) return
    if (poll.data.state === 'succeeded' || poll.data.state === 'none') {
      // Done, or the job is gone (restart): the engine's own status tells what happened.
      setWatching(false)
      onInstalled()
    } else if (poll.data.state === 'failed') {
      setWatching(false)
    }
  }, [watching, poll.data, onInstalled])

  const install = async () => {
    if (!chosen) return
    setBusy(true)
    setError(null)
    try {
      await installEngine(chosen)
      setWatching(true)
      poll.refresh()
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'apps.engine_installed') onInstalled()
      else if (caught instanceof ApiError && caught.code === 'apps.job_running') setWatching(true)
      else setError(caught)
    } finally {
      setBusy(false)
    }
  }

  if (job.state === 'running' || (watching && job.state !== 'failed')) {
    return (
      <div className="flex max-w-3xl flex-col gap-5">
        <div className="flex flex-col gap-3" role="status">
          <StatusLabel status="info" label={t('apps.engine.installing')} />
          <ScanLine className="w-40" />
        </div>
        <p className="text-muted-foreground">{t('apps.engine.installingBody')}</p>
        <JobLog lines={job.log} label={t('apps.engine.logLabel')} />
      </div>
    )
  }

  return (
    <div className="flex max-w-3xl flex-col gap-8">
      {job.state === 'failed' && (
        <div className="flex flex-col gap-4">
          <Notice status="crit" title={t('apps.engine.failedTitle')}>
            {t('apps.engine.failedBody')}
          </Notice>
          <JobLog lines={job.log} label={t('apps.engine.logLabel')} />
        </div>
      )}

      <p className="text-lg text-muted-foreground">{t('apps.engine.body')}</p>
      <dl className="grid gap-x-8 gap-y-4 sm:grid-cols-[auto_1fr]">
        <dt className="label-mono pt-1 text-muted-foreground">{t('apps.engine.ramLabel')}</dt>
        <dd>{t('apps.engine.ram', { mb: DOCKER_RAM_MB })}</dd>
        <dt className="label-mono pt-1 text-muted-foreground">{t('apps.engine.sourceLabel')}</dt>
        <dd>{t('apps.engine.source')}</dd>
        <dt className="label-mono pt-1 text-muted-foreground">{t('apps.engine.dataLabel')}</dt>
        <dd>{t('apps.engine.data')}</dd>
      </dl>

      {pools && mounted.length === 0 ? (
        <Notice
          status="warn"
          title={t('apps.engine.noPoolTitle')}
          actions={
            offerPool ? (
              <ButtonLink href={NEW_POOL_PATH} variant="ghost" size="sm" className="-ml-3">
                {t('storage.createPool')}
              </ButtonLink>
            ) : undefined
          }
        >
          {poolList.length > 0 ? t('apps.engine.noMountedPool') : t('apps.engine.noPoolBody')}
        </Notice>
      ) : (
        <div className="flex flex-col gap-6">
          <div className="max-w-md">
            <PoolSelect
              pools={poolList}
              value={chosen}
              onChange={setPool}
              disabled={busy}
              showFree
              label={t('apps.engine.poolLabel')}
              hint={t('apps.engine.poolHint')}
            />
          </div>
          {dataHere && (
            <Notice status="info" title={t('apps.engine.dataTitle', { pool: dataHere.pool })}>
              {dataHere.containers > 0 ? t('apps.engine.dataHere', { count: dataHere.containers }) : t('apps.engine.dataHereEmpty')}
            </Notice>
          )}
          {dataElsewhere && (
            <Notice status="warn" title={t('apps.engine.dataTitle', { pool: dataElsewhere.pool })}>
              {dataElsewhere.containers > 0
                ? t('apps.engine.dataElsewhere', { count: dataElsewhere.containers, pool: dataElsewhere.pool, chosen })
                : t('apps.engine.dataElsewhereEmpty', { pool: dataElsewhere.pool, chosen })}
            </Notice>
          )}
          {error !== null && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
          <div>
            <Button
              icon={<Icon icon={job.state === 'failed' ? Download : ContainerIcon} />}
              busy={busy}
              disabled={!chosen}
              onClick={() => void install()}
            >
              {job.state === 'failed' ? t('apps.engine.retry') : t('apps.engine.install')}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
