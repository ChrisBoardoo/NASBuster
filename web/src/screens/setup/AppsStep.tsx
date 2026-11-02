// SPDX-License-Identifier: AGPL-3.0-or-later
// Onboarding step "apps", optional: install Docker (its data on the pool, about 150 MB of RAM),
// then, once it runs, pick apps from the catalog. Everything here can be done later from Apps.
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getApps, getCatalog, getEngine, getPools } from '../../api/endpoints'
import type { App } from '../../api/types'
import { useApi } from '../../api/useApi'
import { Catalog } from '../../components/apps/Catalog'
import { EngineSetup } from '../../components/apps/EngineSetup'
import { claimsAdminOnFirstVisit } from '../../components/apps/PortainerWarning'
import { SetupToken } from '../../components/apps/SetupToken'
import { Button } from '../../components/Button'
import { InlineLoading } from '../../components/Loading'
import { Notice } from '../../components/Notice'
import { currentHost, isRunning } from '../../lib/apps'
import { useErrorMessage } from '../../lib/messages'
import { StepLayout } from './StepLayout'
import { useWizard } from './wizard'

export function AppsStep() {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const { persistQuietly, goNext } = useWizard()
  const engine = useApi((signal) => getEngine({ signal }), [])
  const pools = useApi((signal) => getPools({ signal }), [])
  const [busy, setBusy] = useState(false)
  const [started, setStarted] = useState<App[]>([])

  const status = engine.data
  const ready = Boolean(status?.installed && status.active)
  const installing = Boolean(status && !status.installed && isRunning(status.job))

  const leave = async () => {
    setBusy(true)
    await persistQuietly({ currentStep: 'safety', completedStep: status?.installed || installing ? 'apps' : undefined })
    goNext()
  }

  return (
    <StepLayout
      eyebrow={t('onboarding.apps.eyebrow')}
      title={t('onboarding.apps.title')}
      intro={t('onboarding.apps.intro')}
      onContinue={() => void leave()}
      continueLabel={status && !status.installed && !installing ? t('onboarding.apps.notNow') : t('common.continue')}
      busy={busy}
    >
      {engine.loading && !status ? (
        <InlineLoading />
      ) : !status ? (
        <Notice
          status="warn"
          className="max-w-3xl"
          title={t('apps.loadFailed')}
          actions={
            <Button variant="ghost" size="sm" className="-ml-3" onClick={engine.reload}>
              {t('common.retry')}
            </Button>
          }
        >
          {errorMessage(engine.error)}
        </Notice>
      ) : !status.installed ? (
        <EngineSetup engine={status} pools={pools.data?.pools} onInstalled={engine.reload} offerPool={false} />
      ) : (
        <ReadyContent ready={ready} started={started} pool={status.pool} onStarted={(app) => setStarted((list) => [...list, app])} />
      )}
    </StepLayout>
  )
}

function ReadyContent({ ready, started, pool, onStarted }: { ready: boolean; started: App[]; pool: string; onStarted: (app: App) => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const catalog = useApi((signal) => getCatalog({ signal }), [])
  const apps = useApi((signal) => getApps({ signal }), [])
  const pools = useApi((signal) => getPools({ signal }), [])
  const templates = catalog.data?.templates ?? []
  const portainer = started.find((app) => claimsAdminOnFirstVisit({ name: app.template }))
  const portainerTemplate = templates.find((template) => template.name === portainer?.template)

  if (!ready) {
    return (
      <Notice status="warn" className="max-w-3xl" title={t('apps.engine.downTitle')}>
        {pool ? t('apps.engine.downBody', { pool }) : t('apps.engine.downBodyNoPool')}
      </Notice>
    )
  }

  return (
    <div className="flex max-w-5xl flex-col gap-8">
      <Notice status="ok" title={t('onboarding.apps.ready')}>
        {t('onboarding.apps.readyBody')}
      </Notice>
      {started.length > 0 && (
        <Notice status="info" title={t('onboarding.apps.started', { names: started.map((app) => app.name).join(', ') })}>
          <span className="flex flex-col gap-2">
            <span>{t('onboarding.apps.startedBody')}</span>
            {portainer && <span className="text-foreground">{t('onboarding.apps.portainerSoon', { name: portainer.name })}</span>}
          </span>
        </Notice>
      )}
      {portainerTemplate?.setupToken && portainer && (
        <SetupToken
          app={portainer.name}
          title={portainerTemplate.title}
          openHref={`${portainerTemplate.webScheme}://${currentHost()}:${portainerTemplate.webPort}`}
        />
      )}
      {catalog.loading && !catalog.data ? (
        <InlineLoading />
      ) : catalog.error && !catalog.data ? (
        <Notice status="warn">{errorMessage(catalog.error)}</Notice>
      ) : (
        <Catalog
          templates={templates}
          apps={[...(apps.data?.apps ?? []), ...started.filter((app) => !apps.data?.apps.some((a) => a.name === app.name))]}
          pools={pools.data?.pools ?? []}
          preferredPool={pool}
          linkInstalled={false}
          onInstalled={(app) => {
            onStarted(app)
            apps.reload()
          }}
        />
      )}
    </div>
  )
}
