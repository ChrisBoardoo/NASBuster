// SPDX-License-Identifier: AGPL-3.0-or-later
// Step 4: hostname (RFC 1123, validated live) + time zone → PUT /system/identity (via nbhelper).
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../../api/client'
import { getTimezones, putIdentity } from '../../api/endpoints'
import { useApi } from '../../api/useApi'
import { Button } from '../../components/Button'
import { Field } from '../../components/Field'
import { InlineLoading } from '../../components/Loading'
import { Notice } from '../../components/Notice'
import { TimezonePicker } from '../../components/TimezonePicker'
import { useErrorMessage } from '../../lib/messages'
import { hostnameProblem } from '../../lib/validation'
import { useReadySession } from '../../session/SessionContext'
import { StepLayout } from './StepLayout'
import { useWizard } from './wizard'

function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? ''
  } catch {
    return ''
  }
}

export function IdentityStep() {
  const { t } = useTranslation()
  const { setHostname } = useReadySession()
  const { goNext, drafts, updateDrafts, persistQuietly } = useWizard()
  const errorMessage = useErrorMessage()
  const { hostname, timezone } = drafts.identity
  const hostnameRef = useRef<HTMLInputElement>(null)

  const zones = useApi((signal) => getTimezones({ signal }), [])
  const detected = useMemo(browserTimezone, [])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  const [submitted, setSubmitted] = useState(false)

  // Default time zone once the list is known: the browser's, else UTC.
  useEffect(() => {
    const list = zones.data?.timezones
    if (!list || list.length === 0 || timezone) return
    const fallback = list.includes(detected) ? detected : list.includes('UTC') ? 'UTC' : (list[0] ?? '')
    updateDrafts((d) => ({ ...d, identity: { ...d.identity, timezone: fallback } }))
  }, [zones.data, timezone, detected, updateDrafts])

  const problem = hostnameProblem(hostname)
  const serverHostnameError = error?.code === 'validation.hostname'
  const hostnameError = serverHostnameError
    ? errorMessage(error)
    : problem && (hostname.length > 0 || submitted)
      ? t(`onboarding.identity.hostnameErrors.${problem}`)
      : null

  const setField = (field: 'hostname' | 'timezone', value: string) => {
    updateDrafts((d) => ({ ...d, identity: { ...d.identity, [field]: value } }))
    setError(null)
  }

  const skip = async () => {
    setBusy(true)
    await persistQuietly({ currentStep: 'disks' })
    goNext()
  }

  const submit = async () => {
    setSubmitted(true)
    if (problem) {
      hostnameRef.current?.focus()
      return
    }
    if (!timezone) return
    setBusy(true)
    setError(null)
    try {
      const saved = await putIdentity({ hostname, timezone })
      setHostname(saved?.hostname ?? hostname)
      await persistQuietly({ currentStep: 'disks', completedStep: 'identity' })
      goNext()
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : new ApiError(0, 'unknown', String(caught)))
      setBusy(false)
    }
  }

  const helperDown = error?.code === 'helper.unavailable'
  const timezoneError = error?.code === 'validation.timezone' ? error : null
  const otherError = error && !helperDown && !serverHostnameError && !timezoneError ? error : null
  const canSkip = helperDown || otherError !== null || Boolean(zones.error && !zones.data)

  return (
    <StepLayout
      eyebrow={t('onboarding.identity.eyebrow')}
      title={t('onboarding.identity.title')}
      intro={t('onboarding.identity.intro')}
      onContinue={() => void submit()}
      continueLabel={busy ? t('common.saving') : t('onboarding.identity.submit')}
      busy={busy}
      secondaryAction={
        canSkip ? (
          <Button variant="ghost" onClick={() => void skip()} disabled={busy}>
            {t('common.skip')}
          </Button>
        ) : undefined
      }
    >
      <div className="grid max-w-5xl gap-x-16 gap-y-10 lg:grid-cols-2">
        <Field
          data-autofocus=""
          inputRef={hostnameRef}
          label={t('onboarding.identity.hostname')}
          name="hostname"
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          maxLength={63}
          className="[&_input]:font-mono"
          value={hostname}
          onChange={(event) => setField('hostname', event.target.value.toLowerCase().replace(/\s/g, '-'))}
          hint={t('onboarding.identity.hostnameHint')}
          error={hostnameError}
        >
          <div className="mt-4 flex flex-col gap-1 border-l-2 border-accent pl-4">
            <span className="label-mono text-muted-foreground">{t('onboarding.identity.preview')}</span>
            <span className="font-mono text-2xl break-all md:text-3xl" aria-live="polite">
              \\{hostname || '—'}
            </span>
          </div>
        </Field>

        <div className="flex flex-col gap-4">
          {zones.loading && !zones.data ? (
            <InlineLoading />
          ) : zones.data ? (
            <TimezonePicker
              zones={zones.data.timezones}
              value={timezone}
              onChange={(zone) => setField('timezone', zone)}
              detected={detected}
            />
          ) : (
            <Notice
              status="warn"
              title={t('onboarding.identity.timezonesFailed')}
              actions={
                <Button variant="ghost" size="sm" className="-ml-3" onClick={zones.reload}>
                  {t('common.retry')}
                </Button>
              }
            >
              {errorMessage(zones.error)}
            </Notice>
          )}
          {timezoneError && <Notice status="crit">{errorMessage(timezoneError)}</Notice>}
        </div>
      </div>

      {helperDown && (
        <Notice status="warn" title={t('onboarding.identity.helperTitle')} className="max-w-3xl" assertive>
          {t('onboarding.identity.helperBody')}
        </Notice>
      )}
      {otherError && (
        <Notice status="crit" className="max-w-3xl" assertive>
          <span className="text-foreground">{errorMessage(otherError)}</span>
        </Notice>
      )}
    </StepLayout>
  )
}
