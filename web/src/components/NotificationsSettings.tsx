// SPDX-License-Identifier: AGPL-3.0-or-later
// Settings → Notifications: ntfy (random topic, optional token never shown again) and a
// generic webhook. "Send a test" saves first when needed: the server tests the SAVED setup.
import { Dices, Send } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../api/client'
import { getNotifications, putNotifications, testNotifications } from '../api/endpoints'
import type { NotificationTestResult, NotificationsConfig, NotificationsUpdate } from '../api/types'
import { useApi } from '../api/useApi'
import { useDynamicT, useErrorMessage } from '../lib/messages'
import { NTFY_TOKEN_MAX, type UrlProblem, notificationUrlProblem, randomTopicUrl } from '../lib/notifications'
import { Button } from './Button'
import { Field } from './Field'
import { Icon } from './Icon'
import { InlineLoading } from './Loading'
import { Notice } from './Notice'
import { NtfyGuide } from './NtfyGuide'
import { StatusLabel } from './StatusLabel'
import { Switch } from './Switch'

interface Form {
  ntfyEnabled: boolean
  ntfyUrl: string
  webhookEnabled: boolean
  webhookUrl: string
  /** Typed by the admin; never pre-filled, sent only when not empty. */
  token: string
}

const fromConfig = (c: NotificationsConfig, fallbackTopic: string): Form => ({
  ntfyEnabled: c.ntfy.enabled,
  ntfyUrl: c.ntfy.url || fallbackTopic,
  webhookEnabled: c.webhook.enabled,
  webhookUrl: c.webhook.url,
  token: '',
})

const sameForm = (a: Form, b: Form) =>
  a.ntfyEnabled === b.ntfyEnabled &&
  a.ntfyUrl === b.ntfyUrl &&
  a.webhookEnabled === b.webhookEnabled &&
  a.webhookUrl === b.webhookUrl &&
  a.token === b.token

export function NotificationsSettings() {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const loaded = useApi((signal) => getNotifications({ signal }), [])
  // A random topic is offered as long as none is saved (not saved by itself).
  const generated = useMemo(() => randomTopicUrl(), [])
  const [saved, setSaved] = useState<NotificationsConfig | null>(null)
  const [form, setForm] = useState<Form | null>(null)
  const [busy, setBusy] = useState<'save' | 'test' | 'token' | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [message, setMessage] = useState('')
  const [results, setResults] = useState<NotificationTestResult[] | null>(null)

  useEffect(() => {
    if (!loaded.data) return
    setSaved(loaded.data)
    setForm(fromConfig(loaded.data, generated))
  }, [loaded.data, generated])

  if (loaded.loading && !loaded.data) return <InlineLoading />
  if (!saved || !form) {
    return (
      <Notice
        status="warn"
        title={t('notifications.loadFailed')}
        actions={
          <Button variant="ghost" size="sm" className="-ml-3" onClick={loaded.reload}>
            {t('common.retry')}
          </Button>
        }
      >
        {errorMessage(loaded.error)}
      </Notice>
    )
  }

  const baseline = fromConfig(saved, generated)
  const dirty = !sameForm(form, baseline)
  const ntfyProblem = form.ntfyEnabled || form.ntfyUrl !== baseline.ntfyUrl ? notificationUrlProblem(form.ntfyUrl) : null
  const webhookProblem = form.webhookEnabled || form.webhookUrl !== '' ? notificationUrlProblem(form.webhookUrl) : null
  const tokenTooLong = form.token.length > NTFY_TOKEN_MAX
  const invalid = Boolean(ntfyProblem || webhookProblem || tokenTooLong)

  const set = (patch: Partial<Form>) => {
    setForm({ ...form, ...patch })
    setMessage('')
    setError(null)
  }

  const payload = (base: Form, token?: string): NotificationsUpdate => ({
    ntfy: { enabled: base.ntfyEnabled, url: base.ntfyUrl, ...(token !== undefined ? { token } : {}) },
    webhook: { enabled: base.webhookEnabled, url: base.webhookUrl },
  })

  const save = async (): Promise<boolean> => {
    if (invalid) return false
    const next = await putNotifications(payload(form, form.token ? form.token : undefined))
    setSaved(next)
    setForm(fromConfig(next, generated))
    return true
  }

  const run = async (kind: 'save' | 'test') => {
    setBusy(kind)
    setError(null)
    setMessage('')
    if (kind === 'test') setResults(null)
    try {
      if (dirty && !(await save())) return
      if (kind === 'save') setMessage(t('notifications.saved'))
      else setResults(await testNotifications())
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(null)
    }
  }

  // Removes the saved token right away (with the saved channels, not unsaved edits).
  const removeToken = async () => {
    setBusy('token')
    setError(null)
    try {
      const next = await putNotifications(payload(fromConfig(saved, ''), ''))
      setSaved(next)
      setMessage(t('notifications.tokenRemoved'))
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(null)
    }
  }

  const urlError = (problem: UrlProblem | null) => (problem ? t(`notifications.urlErrors.${problem}`) : null)
  const serverDetail = error instanceof ApiError && error.code === 'validation.url' ? error.message : null
  // The server names the channel whose URL it refused: show it on that field.
  const refusedChannel = error instanceof ApiError && error.code === 'validation.url' ? error.params.channel : null

  return (
    <div className="flex flex-col gap-12">
      <section className="flex flex-col gap-6">
        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2">
          <h3 className="text-2xl font-semibold tracking-tight md:text-3xl">{t('notifications.ntfyTitle')}</h3>
          <Switch
            checked={form.ntfyEnabled}
            onChange={(ntfyEnabled) => set({ ntfyEnabled })}
            label={t('notifications.ntfyTitle')}
            disabled={busy !== null}
          />
        </div>
        <p className="max-w-2xl text-muted-foreground">{t('notifications.ntfyIntro')}</p>
        <div className="grid gap-x-12 gap-y-8 lg:grid-cols-12">
          <div className="flex flex-col gap-4 lg:col-span-7">
            <Field
              label={t('notifications.topicUrl')}
              name="ntfy-url"
              type="url"
              inputMode="url"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              className="[&_input]:font-mono"
              value={form.ntfyUrl}
              onChange={(event) => set({ ntfyUrl: event.target.value.trim() })}
              hint={t('notifications.topicHint')}
              error={urlError(ntfyProblem) ?? (refusedChannel === 'ntfy' ? errorMessage(error) : null)}
            />
            <div>
              <Button
                variant="ghost"
                size="sm"
                className="-ml-3"
                icon={<Icon icon={Dices} />}
                onClick={() => set({ ntfyUrl: randomTopicUrl() })}
                disabled={busy !== null}
              >
                {t('notifications.regenerate')}
              </Button>
            </div>
            {saved.ntfy.tokenSet ? (
              <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
                <StatusLabel status="ok" label={t('notifications.tokenSaved')} />
                <Button variant="ghost" size="sm" onClick={() => void removeToken()} busy={busy === 'token'}>
                  {t('notifications.removeToken')}
                </Button>
              </div>
            ) : (
              <Field
                label={t('notifications.token')}
                name="ntfy-token"
                type="password"
                autoComplete="new-password"
                spellCheck={false}
                value={form.token}
                onChange={(event) => set({ token: event.target.value })}
                hint={t('notifications.tokenHint')}
                error={tokenTooLong ? t('notifications.tokenTooLong', { max: NTFY_TOKEN_MAX }) : null}
              />
            )}
          </div>
          <div className="lg:col-span-5">
            <NtfyGuide url={form.ntfyUrl} />
          </div>
        </div>
      </section>

      <section className="flex flex-col gap-6">
        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2">
          <h3 className="text-2xl font-semibold tracking-tight md:text-3xl">{t('notifications.webhookTitle')}</h3>
          <Switch
            checked={form.webhookEnabled}
            onChange={(webhookEnabled) => set({ webhookEnabled })}
            label={t('notifications.webhookTitle')}
            disabled={busy !== null}
          />
        </div>
        <div className="grid gap-x-12 gap-y-6 lg:grid-cols-12">
          <Field
            label={t('notifications.webhookUrl')}
            name="webhook-url"
            type="url"
            inputMode="url"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            className="[&_input]:font-mono lg:col-span-7"
            value={form.webhookUrl}
            onChange={(event) => set({ webhookUrl: event.target.value.trim() })}
            hint={t('notifications.webhookHint')}
            error={urlError(webhookProblem) ?? (refusedChannel === 'webhook' ? errorMessage(error) : null)}
          />
          <div className="flex flex-col gap-2 lg:col-span-5">
            <span className="label-mono text-muted-foreground">{t('notifications.webhookFormat')}</span>
            <code className="block overflow-x-auto border border-border bg-muted px-3 py-2 text-xs whitespace-pre text-foreground">
              {WEBHOOK_SAMPLE}
            </code>
            <p className="text-sm text-muted-foreground">{t('notifications.webhookDisk')}</p>
          </div>
        </div>
      </section>

      {error !== null && (
        <Notice status="crit" assertive>
          <span className="block text-foreground">{errorMessage(error)}</span>
          {serverDetail && <span className="mt-1 block font-mono text-sm">{serverDetail}</span>}
        </Notice>
      )}
      <div className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center gap-x-8 gap-y-3">
          <Button onClick={() => void run('save')} busy={busy === 'save'} disabled={!dirty || invalid || busy !== null}>
            {busy === 'save' ? t('common.saving') : t('notifications.save')}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            icon={<Icon icon={Send} />}
            onClick={() => void run('test')}
            busy={busy === 'test'}
            disabled={invalid || busy !== null}
          >
            {busy === 'test' ? t('notifications.testing') : dirty ? t('notifications.saveAndTest') : t('notifications.test')}
          </Button>
          <span className="text-sm text-status-ok" role="status">
            {message}
          </span>
        </div>
        {results && <TestResults results={results} />}
      </div>
    </div>
  )
}

const WEBHOOK_SAMPLE = `{
  "nas": "raspberrypi",
  "title": "…",
  "level": "crit",
  "code": "pool.degraded",
  "resolved": false,
  "params": { "pool": "tank" },
  "time": 1790888400
}`

/** One line per channel: Sent, or why not (translated from `code`, the server's text as detail). */
export function TestResults({ results }: { results: NotificationTestResult[] }) {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  if (results.length === 0) {
    return (
      <p className="text-muted-foreground" role="status">
        {t('notifications.noChannel')}
      </p>
    )
  }
  return (
    <ul className="flex flex-col gap-3" role="status">
      {results.map((result) => {
        const demo = result.code === 'notify.demo'
        const key = result.code ? `notifications.codes.${result.code.replace(/^notify./, '')}` : ''
        const reason = key && has(key) ? td(key, { status: result.status ?? '' }) : null
        return (
          <li key={result.channel} className="flex flex-col gap-1">
            <span className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
              <span className="w-24 shrink-0 font-semibold">{result.channel === 'ntfy' ? 'ntfy' : t('notifications.webhookTitle')}</span>
              <StatusLabel
                status={demo ? 'info' : result.ok ? 'ok' : 'crit'}
                label={demo ? t('notifications.demo') : result.ok ? t('notifications.sent') : t('notifications.failed')}
              />
              {reason && <span className="text-sm">{reason}</span>}
            </span>
            {!result.ok && result.error && <span className="font-mono text-xs break-all text-muted-foreground">{result.error}</span>}
          </li>
        )
      })}
    </ul>
  )
}
