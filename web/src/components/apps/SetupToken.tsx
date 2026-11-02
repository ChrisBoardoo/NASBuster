// SPDX-License-Identifier: AGPL-3.0-or-later
// The setup token of an app that asks for one before its admin account exists (Portainer), found
// in its logs so that nobody has to read them. The administrator still creates that account in
// the app, with the name and password they choose. The app locks itself 5 minutes after starting
// if nobody does; a restart prints a new token.
import { ExternalLink, RotateCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { appAction, getAppSetupToken } from '../../api/endpoints'
import { usePolling } from '../../api/usePolling'
import { APPS_POLL_MS } from '../../lib/constants'
import { useNow } from '../../lib/hooks'
import { useErrorMessage } from '../../lib/messages'
import { clock } from '../../lib/pools'
import { Button, ExternalButtonLink } from '../Button'
import { CopyText } from '../CopyText'
import { Icon } from '../Icon'
import { Notice } from '../Notice'

interface SetupTokenProps {
  /** The app's name in CWNSB. */
  app: string
  /** Its catalog title ("Portainer CE"). */
  title: string
  /** Its web page, when the surrounding screen has no "Open" link of its own. */
  openHref?: string
  onChanged?: () => void
}

export function SetupToken({ app, title, openHref, onChanged }: SetupTokenProps) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [state, setState] = useState<string | undefined>()
  // Once the account exists, there is nothing more to ask the app.
  const poll = usePolling((signal) => getAppSetupToken(app, { signal }), APPS_POLL_MS, state !== 'created')
  const now = useNow(1000)
  const [restarting, setRestarting] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const data = poll.data
  if (data?.state !== state) setState(data?.state)

  const restart = async () => {
    setRestarting(true)
    setError(null)
    try {
      await appAction(app, 'restart')
      poll.refresh()
      onChanged?.()
    } catch (caught) {
      setError(caught)
    } finally {
      setRestarting(false)
    }
  }

  if (data?.state === 'locked') {
    return (
      <Notice
        status="warn"
        title={t('apps.setupToken.lockedTitle', { title })}
        actions={
          <Button variant="secondary" size="sm" icon={<Icon icon={RotateCw} />} busy={restarting} onClick={() => void restart()}>
            {t('apps.setupToken.restart', { title })}
          </Button>
        }
      >
        <span className="flex flex-col gap-2">
          <span>{t('apps.setupToken.lockedBody')}</span>
          {error !== null && <span className="text-status-crit">{errorMessage(error)}</span>}
        </span>
      </Notice>
    )
  }
  if (data?.state !== 'waiting' || !data.token) return null

  const remaining = data.expiresAt ? (Date.parse(data.expiresAt) - now) / 1000 : null
  return (
    <section className="flex flex-col gap-3 border-l-2 border-accent pl-4">
      <h4 className="label-mono text-accent">{t('apps.setupToken.title')}</h4>
      <p className="max-w-2xl text-muted-foreground">{t('apps.setupToken.body', { title })}</p>
      <CopyText value={data.token} label={t('apps.setupToken.copyLabel', { title })} />
      {remaining !== null && (
        <p className="text-sm text-status-warn tabular-nums">
          {remaining > 0 ? t('apps.setupToken.expires', { title, time: clock(remaining) }) : t('apps.setupToken.expiring', { title })}
        </p>
      )}
      {openHref && (
        <div>
          <ExternalButtonLink href={openHref} icon={<Icon icon={ExternalLink} />} label={t('apps.setupToken.openLabel', { title })}>
            {t('apps.setupToken.open', { title })}
          </ExternalButtonLink>
        </div>
      )}
    </section>
  )
}
