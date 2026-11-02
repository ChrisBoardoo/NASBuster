// SPDX-License-Identifier: AGPL-3.0-or-later
// Apps (ADR-0013): Docker (installed on demand, its data on a pool), the catalog, pasted compose
// files, the apps with their containers, the orphans Docker restarted by itself (ADR-0036), and the
// Compose projects managed elsewhere.
// GET /api/v1/apps is polled every 5 s while visible, every 2 s while a deployment runs.
import { FileCode, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'wouter'
import { getApps, getCatalog, getPools } from '../api/endpoints'
import type { App, AppsResponse } from '../api/types'
import { useApi } from '../api/useApi'
import { usePolling } from '../api/usePolling'
import { AppRow } from '../components/apps/AppRow'
import { Catalog } from '../components/apps/Catalog'
import { EngineRemove } from '../components/apps/EngineRemove'
import { EngineSetup } from '../components/apps/EngineSetup'
import { CustomApp } from '../components/apps/NewApp'
import { OrphanApps } from '../components/apps/OrphanApps'
import { OtherProjects } from '../components/apps/OtherProjects'
import { Button } from '../components/Button'
import { Icon } from '../components/Icon'
import { InlineLoading } from '../components/Loading'
import { Notice } from '../components/Notice'
import { Container, PageHeader, SectionTitle } from '../components/Typography'
import { appAnchorId, isRunning } from '../lib/apps'
import { APPS_POLL_MS, JOB_POLL_MS } from '../lib/constants'
import { useDocumentTitle, useHashScroll } from '../lib/hooks'
import { useErrorMessage } from '../lib/messages'

const anyJobRunning = (data: AppsResponse | undefined) => Boolean(data?.apps.some((app) => isRunning(app.job)))

export default function Apps() {
  const { t } = useTranslation()
  useDocumentTitle(t('nav.apps'))
  useHashScroll()
  const errorMessage = useErrorMessage()
  const [fast, setFast] = useState(false)
  const apps = usePolling((signal) => getApps({ signal }), fast ? JOB_POLL_MS : APPS_POLL_MS)
  const catalog = useApi((signal) => getCatalog({ signal }), [])
  const pools = useApi((signal) => getPools({ signal }), [])
  const [custom, setCustom] = useState(false)
  const [fresh, setFresh] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const data = apps.data
  useEffect(() => setFast(anyJobRunning(data)), [data])

  const { refresh } = apps
  const reloadPools = pools.reload
  const refreshAll = useCallback(() => {
    refresh()
    reloadPools()
  }, [refresh, reloadPools])

  // A new app: bring its row into view once the list has it.
  const freshListed = Boolean(fresh && data?.apps.some((app) => app.name === fresh))
  useEffect(() => {
    if (fresh && freshListed) document.getElementById(appAnchorId(fresh))?.scrollIntoView({ block: 'start' })
  }, [fresh, freshListed])

  const installed = (app: App) => {
    setCustom(false)
    setFresh(app.name)
    setNotice(t('apps.installStarted', { name: app.name }))
    setFast(true)
    refresh()
  }

  const engine = data?.engine
  const poolList = pools.data?.pools ?? []
  const ready = Boolean(engine?.installed && engine.active)
  const templates = catalog.data?.templates ?? []

  return (
    <Container>
      <PageHeader
        eyebrow={t('apps.eyebrow')}
        title={t('apps.title')}
        intro={t('apps.intro')}
        aside={
          <Button variant="secondary" size="sm" icon={<Icon icon={RefreshCw} />} onClick={refreshAll} busy={!data && !apps.error}>
            {t('common.refresh')}
          </Button>
        }
      />

      {!data ? (
        apps.error ? (
          <Notice
            status="warn"
            title={t('apps.loadFailed')}
            actions={
              <Button variant="ghost" size="sm" className="-ml-3" onClick={refresh}>
                {t('common.retry')}
              </Button>
            }
          >
            {errorMessage(apps.error)}
          </Notice>
        ) : (
          <InlineLoading />
        )
      ) : (
        <>
          {notice && (
            <Notice status="ok" className="mb-10">
              {notice}
            </Notice>
          )}

          {engine && !engine.installed && (
            <section aria-labelledby="apps-engine" className="mb-20 flex flex-col gap-8">
              <SectionTitle id="apps-engine" title={t('apps.engine.title')} />
              <EngineSetup
                engine={engine}
                pools={pools.data?.pools}
                onInstalled={() => {
                  setNotice(t('apps.engine.installed'))
                  refreshAll()
                }}
              />
            </section>
          )}

          {engine?.installed && (
            <p className="label-mono -mt-4 mb-10 text-muted-foreground">
              {engine.pool
                ? t('apps.engine.summary', { version: engine.version || '?', pool: engine.pool })
                : t('apps.engine.summaryNoPool', { version: engine.version || '?' })}
            </p>
          )}
          {engine?.installed && !engine.active && (
            <Notice
              status="warn"
              className="mb-10"
              title={t('apps.engine.downTitle')}
              actions={
                <Link
                  href="/storage"
                  className="inline-flex min-h-11 items-center text-foreground underline decoration-border-hover underline-offset-4 hover:decoration-accent"
                >
                  {t('nav.storage')}
                </Link>
              }
            >
              {engine.pool ? t('apps.engine.downBody', { pool: engine.pool }) : t('apps.engine.downBodyNoPool')}
            </Notice>
          )}

          {(engine?.installed || data.apps.length > 0) && (
            <section aria-labelledby="apps-list" className="flex flex-col gap-6">
              <SectionTitle id="apps-list" title={t('apps.listTitle')} />
              {data.apps.length === 0 ? (
                <div className="flex flex-col items-start gap-4 py-6">
                  <p className="text-4xl font-semibold tracking-tight md:text-5xl">{t('apps.empty.title')}</p>
                  <p className="max-w-xl text-lg text-muted-foreground">{t('apps.empty.body')}</p>
                </div>
              ) : (
                <ul className="border-b border-border">
                  {data.apps.map((app) => (
                    <AppRow
                      key={app.name}
                      app={app}
                      template={templates.find((tpl) => tpl.name === app.template)}
                      engineActive={Boolean(engine?.active)}
                      fresh={fresh === app.name}
                      onChanged={refresh}
                      onRemoved={(name, deleteData) => {
                        setFresh(null)
                        setNotice(deleteData ? t('apps.removedData', { name }) : t('apps.removedKept', { name }))
                        refreshAll()
                        window.scrollTo({ top: 0 })
                      }}
                    />
                  ))}
                </ul>
              )}
            </section>
          )}

          {data.orphans.length > 0 && (
            <section aria-labelledby="apps-orphans" className="mt-20 flex flex-col gap-6">
              <SectionTitle id="apps-orphans" title={t('apps.orphans.title')} />
              <Notice status="warn" title={t('apps.orphans.warnTitle')}>
                {t('apps.orphans.warnBody')}
              </Notice>
              <OrphanApps
                orphans={data.orphans}
                engineActive={Boolean(engine?.active)}
                onChanged={refresh}
                onRemoved={(name) => {
                  setNotice(t('apps.orphans.removed', { name }))
                  refresh()
                  window.scrollTo({ top: 0 })
                }}
              />
            </section>
          )}

          <section aria-labelledby="apps-catalog" className="mt-20 flex flex-col gap-6">
            <SectionTitle id="apps-catalog" title={t('apps.catalog.title')} />
            <p className="max-w-2xl text-muted-foreground">
              {ready ? t('apps.catalog.intro') : t('apps.catalog.needsDocker')}
            </p>
            {catalog.loading && !catalog.data ? (
              <InlineLoading />
            ) : catalog.error && !catalog.data ? (
              <Notice status="warn">{errorMessage(catalog.error)}</Notice>
            ) : (
              <Catalog
                templates={templates}
                apps={data.apps}
                pools={poolList}
                preferredPool={engine?.pool ?? ''}
                disabled={!ready}
                onInstalled={installed}
              />
            )}
          </section>

          {ready && (
            <section aria-labelledby="apps-custom" className="mt-20 flex flex-col gap-6">
              <SectionTitle id="apps-custom" title={t('apps.custom.sectionTitle')} />
              {custom ? (
                <CustomApp pools={poolList} preferredPool={engine?.pool ?? ''} onInstalled={installed} onCancel={() => setCustom(false)} />
              ) : (
                <div className="flex flex-col items-start gap-3">
                  <p className="max-w-2xl text-muted-foreground">{t('apps.custom.teaser')}</p>
                  <Button variant="secondary" size="sm" icon={<Icon icon={FileCode} />} onClick={() => setCustom(true)}>
                    {t('apps.custom.open')}
                  </Button>
                </div>
              )}
            </section>
          )}

          {data.others.length > 0 && (
            <section aria-labelledby="apps-others" className="mt-20 flex flex-col gap-6">
              <SectionTitle id="apps-others" title={t('apps.others.title')} />
              <p className="max-w-2xl text-muted-foreground">{t('apps.others.intro')}</p>
              <OtherProjects projects={data.others} />
            </section>
          )}

          {engine?.installed && (
            <EngineRemove
              engine={engine}
              apps={data.apps.filter((app) => app.containers.length > 0).map((app) => app.name)}
              kept={data.apps.filter((app) => app.containers.length === 0).map((app) => app.name)}
              onRemoved={(deleteData) => {
                setNotice(deleteData ? t('apps.engineRemove.doneData') : t('apps.engineRemove.doneKept'))
                refreshAll()
                window.scrollTo({ top: 0 })
              }}
            />
          )}
        </>
      )}
    </Container>
  )
}
