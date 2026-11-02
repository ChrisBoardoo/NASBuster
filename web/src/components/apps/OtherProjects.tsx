// SPDX-License-Identifier: AGPL-3.0-or-later
// Compose projects created outside NasBuster (by hand, or by Portainer): shown, never managed.
import { ExternalLink } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { OtherProject } from '../../api/types'
import { appLinks, appState, currentHost } from '../../lib/apps'
import { ExternalButtonLink } from '../Button'
import { Icon } from '../Icon'
import { AppStatus, Containers } from './AppRow'

export function OtherProjects({ projects }: { projects: readonly OtherProject[] }) {
  const { t } = useTranslation()
  return (
    <ul className="border-b border-border">
      {projects.map((project) => {
        const state = appState(project)
        const links = appLinks(project.containers, undefined, currentHost())
        return (
          <li key={project.name} className="grid gap-x-12 gap-y-6 border-t border-border py-8 lg:grid-cols-12">
            <div className="flex min-w-0 flex-col gap-3 lg:col-span-5">
              <h3 className="text-3xl font-semibold tracking-tight break-all">{project.name}</h3>
              <AppStatus kind={state.kind} status={state.status} running={state.running} total={state.total} />
              {project.configFiles && <p className="font-mono text-xs break-all text-muted-foreground">{project.configFiles}</p>}
              {links.length > 0 && (
                <div className="flex flex-wrap items-center gap-x-6 gap-y-1">
                  {links.map((link) => (
                    <ExternalButtonLink
                      key={link.port}
                      href={link.href}
                      variant="ghost"
                      size="sm"
                      className="-ml-3"
                      icon={<Icon icon={ExternalLink} />}
                      label={t('apps.row.openPortLabel', { name: project.name, port: link.port })}
                    >
                      {t('apps.row.port', { port: link.port })}
                    </ExternalButtonLink>
                  ))}
                </div>
              )}
            </div>
            <div className="min-w-0 lg:col-span-7">
              <Containers containers={project.containers} />
            </div>
          </li>
        )
      })}
    </ul>
  )
}
