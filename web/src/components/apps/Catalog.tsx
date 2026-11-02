// SPDX-License-Identifier: AGPL-3.0-or-later
// The built-in catalog (GET /api/v1/apps/catalog): one card per template, installed in place.
import { Download } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { App, AppTemplate, Pool } from '../../api/types'
import { appAnchorId } from '../../lib/apps'
import { Button } from '../Button'
import { Icon } from '../Icon'
import { StatusLabel } from '../StatusLabel'
import { InstallTemplate } from './NewApp'
import { PortainerWarning } from './PortainerWarning'

interface CatalogProps {
  templates: readonly AppTemplate[]
  apps: readonly App[]
  pools: readonly Pool[]
  preferredPool: string
  /** Docker isn't running: nothing can be installed. */
  disabled?: boolean
  /** Installed apps link to their row (on the Apps page; not in the onboarding). */
  linkInstalled?: boolean
  onInstalled: (app: App) => void
}

export function Catalog({ templates, apps, pools, preferredPool, disabled = false, linkInstalled = true, onInstalled }: CatalogProps) {
  const [open, setOpen] = useState<string | null>(null)
  return (
    <ul className="grid gap-px border border-border bg-border md:grid-cols-2">
      {templates.map((template) => (
        <TemplateCard
          key={template.name}
          template={template}
          installed={apps.filter((app) => app.template === template.name)}
          takenNames={apps.map((app) => app.name)}
          pools={pools}
          preferredPool={preferredPool}
          disabled={disabled}
          linkInstalled={linkInstalled}
          open={open === template.name}
          onOpen={() => setOpen(template.name)}
          onClose={() => setOpen(null)}
          onInstalled={(app) => {
            setOpen(null)
            onInstalled(app)
          }}
        />
      ))}
    </ul>
  )
}

interface TemplateCardProps {
  template: AppTemplate
  installed: App[]
  takenNames: string[]
  pools: readonly Pool[]
  preferredPool: string
  disabled: boolean
  linkInstalled: boolean
  open: boolean
  onOpen: () => void
  onClose: () => void
  onInstalled: (app: App) => void
}

function TemplateCard({
  template,
  installed,
  takenNames,
  pools,
  preferredPool,
  disabled,
  linkInstalled,
  open,
  onOpen,
  onClose,
  onInstalled,
}: TemplateCardProps) {
  const { t, i18n } = useTranslation()
  const lang = i18n.resolvedLanguage ?? i18n.language
  const description = template.description[lang] ?? template.description.en ?? ''

  return (
    <li className="flex min-w-0 flex-col gap-5 bg-background p-6 md:p-8">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
        <h3 className="text-3xl font-semibold tracking-tight">{template.title}</h3>
        {template.expert && (
          <span className="label-mono border border-status-warn px-2 py-0.5 text-status-warn">{t('apps.expert.badge')}</span>
        )}
      </div>
      <p className="text-muted-foreground">{description}</p>
      {template.webPort > 0 && (
        <p className="label-mono text-muted-foreground">
          {t('apps.catalog.webPage', { scheme: template.webScheme || 'http', port: template.webPort })}
        </p>
      )}
      {template.expert && !open && <PortainerWarning template={template} short />}
      {installed.length > 0 && (
        <p className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <StatusLabel status="ok" label={t('apps.catalog.installed')} />
          {installed.map((app) =>
            linkInstalled ? (
              <a
                key={app.name}
                href={`#${appAnchorId(app.name)}`}
                className="inline-flex min-h-11 min-w-11 items-center font-mono text-sm text-foreground underline decoration-border-hover underline-offset-4 hover:decoration-accent"
              >
                {app.name}
              </a>
            ) : (
              <span key={app.name} className="font-mono text-sm text-foreground">
                {app.name}
              </span>
            ),
          )}
        </p>
      )}
      {installed.length === 0 && (
        <div className="mt-auto">
          {open ? (
            <InstallTemplate
              template={template}
              pools={pools}
              preferredPool={preferredPool}
              takenNames={takenNames}
              onInstalled={onInstalled}
              onCancel={onClose}
            />
          ) : (
            <Button variant="secondary" size="sm" icon={<Icon icon={Download} />} disabled={disabled} onClick={onOpen}>
              {t('apps.catalog.install')}
            </Button>
          )}
        </div>
      )}
    </li>
  )
}
