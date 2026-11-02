// SPDX-License-Identifier: AGPL-3.0-or-later
// Onboarding step "safety", two screens, one decision each:
//   1. the protection that is already ON (hourly snapshots with GFS retention, periodic
//      scrub), explained plainly and editable — GET/PUT /api/v1/protection;
//   2. OPTIONAL: alerts on a phone with ntfy (random topic, send a test), skippable.
import { Dices, History, Send, ShieldCheck } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getNotifications, getProtection, putNotifications, putProtection, testNotifications } from '../../api/endpoints'
import type { NotificationTestResult, Protection } from '../../api/types'
import { useApi } from '../../api/useApi'
import { Button } from '../../components/Button'
import { Icon } from '../../components/Icon'
import { InlineLoading } from '../../components/Loading'
import { Notice } from '../../components/Notice'
import { TestResults } from '../../components/NotificationsSettings'
import { NtfyGuide } from '../../components/NtfyGuide'
import { ProtectionEditor } from '../../components/ProtectionEditor'
import { useErrorMessage } from '../../lib/messages'
import { randomTopicUrl, splitTopicUrl } from '../../lib/notifications'
import { hasProblems, protectionProblems, sameProtection } from '../../lib/protection'
import { StepLayout } from './StepLayout'
import { useWizard } from './wizard'

export function SafetyStep() {
  const { drafts, updateDrafts, setLocalBack, persistQuietly, goNext } = useWizard()
  const screen = drafts.safety.screen
  const [busy, setBusy] = useState(false)

  const setScreen = useCallback(
    (next: 'protection' | 'phone') => updateDrafts((d) => ({ ...d, safety: { ...d.safety, screen: next } })),
    [updateDrafts],
  )

  // Back from the phone screen returns to the protection screen first.
  useEffect(() => {
    setLocalBack(screen === 'phone' ? () => setScreen('protection') : null)
    return () => setLocalBack(null)
  }, [screen, setLocalBack, setScreen])

  const finish = async () => {
    setBusy(true)
    await persistQuietly({ currentStep: 'done', completedStep: 'safety' })
    goNext()
  }

  return screen === 'phone' ? (
    <PhoneScreen onDone={() => void finish()} busy={busy} />
  ) : (
    <ProtectionScreen onNext={() => setScreen('phone')} />
  )
}

// ---- 1. Protection ---------------------------------------------------------------

function ProtectionScreen({ onNext }: { onNext: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const { drafts, updateDrafts } = useWizard()
  const loaded = useApi((signal) => getProtection({ signal }), [])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)

  const server = loaded.data?.protection ?? null
  const value = drafts.protection ?? server
  useEffect(() => {
    if (server && !drafts.protection) updateDrafts((d) => ({ ...d, protection: server }))
  }, [server, drafts.protection, updateDrafts])

  const setValue = (next: Protection) => {
    updateDrafts((d) => ({ ...d, protection: next }))
    setError(null)
  }

  const invalid = value ? hasProblems(protectionProblems(value)) : false

  const next = async () => {
    if (value && invalid) return
    setBusy(true)
    setError(null)
    try {
      if (value && server && !sameProtection(value, server)) {
        const saved = await putProtection(value)
        updateDrafts((d) => ({ ...d, protection: saved }))
      }
      setBusy(false)
      onNext()
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <StepLayout
      key="protection"
      eyebrow={t('onboarding.safety.eyebrow')}
      title={t('onboarding.safety.title')}
      intro={t('onboarding.safety.intro')}
      onContinue={() => void next()}
      continueLabel={busy ? t('common.saving') : t('common.continue')}
      busy={busy}
      continueDisabled={invalid}
    >
      {loaded.loading && !loaded.data ? (
        <InlineLoading />
      ) : !value ? (
        <Notice
          status="warn"
          className="max-w-3xl"
          title={t('protection.loadFailed')}
          actions={
            <Button variant="ghost" size="sm" className="-ml-3" onClick={loaded.reload}>
              {t('common.retry')}
            </Button>
          }
        >
          {errorMessage(loaded.error)}
        </Notice>
      ) : (
        <div className="grid max-w-6xl gap-x-16 gap-y-12 lg:grid-cols-12">
          <div className="lg:col-span-7">
            <ProtectionEditor
              value={value}
              onChange={setValue}
              disabled={busy}
              snapshotsAside={<p className="max-w-xl text-muted-foreground">{t('onboarding.safety.snapshotsBody')}</p>}
              scrubAside={<p className="max-w-xl text-muted-foreground">{t('onboarding.safety.scrubBody')}</p>}
            />
          </div>
          <aside className="flex flex-col gap-8 lg:col-span-5">
            <Point icon={History} title={t('onboarding.safety.previousTitle')} body={t('onboarding.safety.previousBody')} />
            <Point icon={ShieldCheck} title={t('onboarding.safety.survivesTitle')} body={t('onboarding.safety.survivesBody')} />
            <Notice status="warn" title={t('onboarding.safety.notBackupTitle')}>
              {t('onboarding.safety.notBackupBody')}
            </Notice>
          </aside>
        </div>
      )}
      {error !== null && (
        <Notice status="crit" assertive className="max-w-3xl">
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
    </StepLayout>
  )
}

function Point({ icon, title, body }: { icon: typeof History; title: string; body: string }) {
  return (
    <section className="flex gap-4">
      <Icon icon={icon} size={24} className="mt-1 shrink-0 text-accent" />
      <div className="flex flex-col gap-1.5">
        <h3 className="text-xl font-semibold tracking-tight">{title}</h3>
        <p className="text-muted-foreground">{body}</p>
      </div>
    </section>
  )
}

// ---- 2. Phone (optional) -----------------------------------------------------------

function PhoneScreen({ onDone, busy: leaving }: { onDone: () => void; busy: boolean }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const { drafts, updateDrafts } = useWizard()
  const loaded = useApi((signal) => getNotifications({ signal }), [])
  const [phase, setPhase] = useState<'idle' | 'saving' | 'testing'>('idle')
  const [turnedOn, setTurnedOn] = useState(false)
  const [results, setResults] = useState<NotificationTestResult[] | null>(null)
  const [error, setError] = useState<unknown>(null)

  // One random topic per wizard run, kept across Back / Continue.
  const topicUrl = drafts.safety.topicUrl
  useEffect(() => {
    if (!topicUrl) updateDrafts((d) => ({ ...d, safety: { ...d.safety, topicUrl: randomTopicUrl() } }))
  }, [topicUrl, updateDrafts])

  const current = loaded.data
  const configured = Boolean(current?.ntfy.enabled && current.ntfy.url)
  const on = configured || turnedOn
  const url = configured && current ? current.ntfy.url : topicUrl
  const busy = phase !== 'idle' || leaving

  const enable = async () => {
    await putNotifications({
      ntfy: { enabled: true, url },
      webhook: current?.webhook ?? { enabled: false, url: '' },
    })
    setTurnedOn(true)
  }

  const sendTest = async () => {
    setPhase('testing')
    setError(null)
    setResults(null)
    try {
      if (!on) await enable()
      setResults(await testNotifications())
    } catch (caught) {
      setError(caught)
    } finally {
      setPhase('idle')
    }
  }

  const turnOnAndContinue = async () => {
    if (!on) {
      setPhase('saving')
      setError(null)
      try {
        await enable()
      } catch (caught) {
        setError(caught)
        setPhase('idle')
        return
      }
      setPhase('idle')
    }
    onDone()
  }

  const topic = splitTopicUrl(url)?.topic

  return (
    <StepLayout
      key="phone"
      eyebrow={t('onboarding.phone.eyebrow')}
      title={t('onboarding.phone.title')}
      intro={t('onboarding.phone.intro')}
      onContinue={() => void turnOnAndContinue()}
      continueLabel={
        phase === 'saving' ? t('common.saving') : on ? t('common.continue') : t('onboarding.phone.turnOn')
      }
      busy={busy}
      continueDisabled={!url}
      secondaryAction={
        on ? undefined : (
          <Button variant="ghost" onClick={onDone} disabled={busy}>
            {t('onboarding.phone.notNow')}
          </Button>
        )
      }
    >
      {loaded.loading && !loaded.data ? (
        <InlineLoading />
      ) : (
        <div className="grid max-w-6xl gap-x-16 gap-y-10 lg:grid-cols-12">
          <div className="flex flex-col gap-6 lg:col-span-7">
            {configured && (
              <Notice status="ok">{t('onboarding.phone.already', { topic: topic ?? url })}</Notice>
            )}
            {url && <NtfyGuide url={url} />}
          </div>
          <div className="flex flex-col gap-5 lg:col-span-5">
            <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
              <Button variant="secondary" icon={<Icon icon={Send} />} onClick={() => void sendTest()} busy={phase === 'testing'} disabled={!url || busy}>
                {phase === 'testing' ? t('notifications.testing') : t('notifications.test')}
              </Button>
              {!on && (
                <Button
                  variant="ghost"
                  size="sm"
                  icon={<Icon icon={Dices} />}
                  onClick={() => updateDrafts((d) => ({ ...d, safety: { ...d.safety, topicUrl: randomTopicUrl() } }))}
                  disabled={busy}
                >
                  {t('notifications.regenerate')}
                </Button>
              )}
            </div>
            {results && <TestResults results={results} />}
            {results?.some((r) => r.ok && r.code !== 'notify.demo') && <p className="text-foreground">{t('onboarding.phone.checkPhone')}</p>}
            <p className="text-sm text-muted-foreground">{t('onboarding.phone.later')}</p>
          </div>
        </div>
      )}
      {error !== null && (
        <Notice status="crit" assertive className="max-w-3xl">
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
    </StepLayout>
  )
}
