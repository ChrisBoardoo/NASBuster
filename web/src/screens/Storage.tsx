// SPDX-License-Identifier: AGPL-3.0-or-later
// Storage: pools found on the disks (to import) and disks with data to copy, when there are
// any; the pools (health, capacity, members, scrub); then every disk the Pi can see.
import { Plus, RefreshCw } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'wouter'
import { getDisks, getImportSources, getImportable, getPools } from '../api/endpoints'
import { useApi } from '../api/useApi'
import { Button, ButtonLink } from '../components/Button'
import { CopySources } from '../components/CopySources'
import { DiskList } from '../components/DiskList'
import { DemoFaults } from '../components/DemoFaults'
import { Icon } from '../components/Icon'
import { ImportPools } from '../components/ImportPools'
import { Notice } from '../components/Notice'
import { NEW_POOL_PATH, PoolList } from '../components/PoolList'
import { Container, PageHeader, SectionTitle } from '../components/Typography'
import { useDocumentTitle, useHashScroll } from '../lib/hooks'
import type { PoolDestroyResult } from '../api/types'
import { useReadySession } from '../session/SessionContext'

export default function Storage() {
  const { t } = useTranslation()
  const { ready } = useReadySession()
  useDocumentTitle(t('nav.storage'))
  useHashScroll()
  const disks = useApi((signal) => getDisks({ signal }), [])
  const pools = useApi((signal) => getPools({ signal }), [])
  // Read-only scan; a failure only hides the card.
  const importable = useApi((signal) => getImportable({ signal }), [])
  const copySources = useApi((signal) => getImportSources({ signal }), [])
  const [imported, setImported] = useState<string | null>(null)

  // Disk id → its pool, shown on the disk rows.
  const poolOf = useMemo(() => {
    const map = new Map<string, string>()
    for (const pool of pools.data?.pools ?? []) for (const id of pool.members) map.set(id, pool.name)
    return map
  }, [pools.data])

  const [notice, setNotice] = useState<string | null>(null)
  const refresh = () => {
    disks.reload()
    pools.reload()
    importable.reload()
    copySources.reload()
  }
  const destroyed = (name: string, result: PoolDestroyResult) => {
    setImported(null)
    setNotice(
      result.forgotten
        ? t('destroy.forgotten', { name })
        : t('destroy.done', { name, count: result.disks.length }),
    )
    refresh()
    window.scrollTo({ top: 0 })
  }
  const hasPools = (pools.data?.pools.length ?? 0) > 0

  return (
    <Container>
      <PageHeader
        eyebrow={t('storage.eyebrow')}
        title={t('storage.title')}
        intro={t('storage.intro')}
        aside={
          <Button
            variant="secondary"
            size="sm"
            icon={<Icon icon={RefreshCw} />}
            onClick={refresh}
            busy={disks.loading || pools.loading}
          >
            {t('common.refresh')}
          </Button>
        }
      />

      {(importable.data?.pools.length ?? 0) > 0 && (
        <section aria-labelledby="storage-importable" className="mb-20 flex flex-col gap-6 border border-accent p-6 md:p-8">
          <div className="flex flex-col gap-2">
            <h2 id="storage-importable" className="text-3xl font-semibold tracking-tight">
              {t('import.cardTitle')}
            </h2>
            <p className="max-w-2xl text-muted-foreground">{t('import.cardIntro')}</p>
          </div>
          <ImportPools
            candidates={importable.data?.pools ?? []}
            disks={disks.data?.disks}
            onRefresh={refresh}
            refreshing={importable.loading}
            onImported={(pool) => {
              setNotice(null)
              setImported(pool.name)
              refresh()
            }}
          />
        </section>
      )}

      {(copySources.data?.sources.length ?? 0) > 0 && (
        <section aria-labelledby="storage-copy" className="mb-20 flex flex-col gap-6 border border-border p-6 md:p-8">
          <div className="flex flex-col gap-2">
            <h2 id="storage-copy" className="text-3xl font-semibold tracking-tight">
              {t('copy.cardTitle')}
            </h2>
            <p className="max-w-2xl text-muted-foreground">{t('copy.cardIntro')}</p>
          </div>
          <CopySources sources={copySources.data?.sources ?? []} disks={disks.data?.disks} />
        </section>
      )}

      <section aria-labelledby="storage-pools" className="flex flex-col gap-6">
        <SectionTitle
          id="storage-pools"
          title={t('storage.poolsTitle')}
          aside={
            hasPools ? (
              <ButtonLink href={NEW_POOL_PATH} variant="ghost" size="sm" className="-mr-3" icon={<Icon icon={Plus} />}>
                {t('storage.newPool')}
              </ButtonLink>
            ) : undefined
          }
        />
        {notice && <Notice status="ok">{notice}</Notice>}
        {imported && (
          <Notice
            status="ok"
            title={t('import.done', { name: imported })}
            actions={
              <Link
                href="/settings#config-backup"
                className="inline-flex min-h-11 items-center text-foreground underline decoration-border-hover underline-offset-4 hover:decoration-accent"
              >
                {t('import.restoreLink')}
              </Link>
            }
          >
            {t('import.restoreNext')}
          </Notice>
        )}
        <PoolList state={pools} disks={disks.data?.disks} onChanged={refresh} onDestroyed={destroyed} />
      </section>

      <section aria-labelledby="storage-disks" className="mt-20 flex flex-col gap-6">
        <SectionTitle id="storage-disks" title={t('storage.disksTitle')} />
        <DiskList state={disks} anchors poolOf={poolOf} onChanged={refresh} />
      </section>
      {ready.boot.demo && (
        <DemoFaults pools={pools.data?.pools} disks={disks.data?.disks} onDone={refresh} />
      )}
    </Container>
  )
}
