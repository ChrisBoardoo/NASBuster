// SPDX-License-Identifier: AGPL-3.0-or-later
// Step 2: the Pi's health check. Before the account exists it uses GET /setup/checks
// (public while needsSetup), afterwards GET /system/checks.
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { getSetupChecks, getSystemChecks } from '../../api/endpoints'
import { useApi } from '../../api/useApi'
import { Button } from '../../components/Button'
import { CheckList, CheckSummary } from '../../components/CheckList'
import { InlineLoading } from '../../components/Loading'
import { Notice } from '../../components/Notice'
import { useErrorMessage } from '../../lib/messages'
import { useReadySession } from '../../session/SessionContext'
import { StepLayout } from './StepLayout'
import { useWizard } from './wizard'

export function HealthStep() {
  const { t } = useTranslation()
  const { ready } = useReadySession()
  const { goNext, drafts, updateDrafts } = useWizard()
  const errorMessage = useErrorMessage()
  const needsSetup = ready.boot.needsSetup
  const cached = drafts.checks

  const checks = useApi(
    (signal) =>
      cached ? Promise.resolve(cached) : needsSetup ? getSetupChecks({ signal }) : getSystemChecks({ signal }),
    [needsSetup],
  )

  useEffect(() => {
    const data = checks.data
    if (data && data !== cached) updateDrafts((d) => ({ ...d, checks: data }))
  }, [checks.data, cached, updateDrafts])

  return (
    <StepLayout
      eyebrow={t('onboarding.health.eyebrow')}
      title={t('onboarding.health.title')}
      intro={t('onboarding.health.intro')}
      onContinue={goNext}
      continueLabel={t('common.continue')}
    >
      {checks.loading && !checks.data ? (
        <InlineLoading label={t('onboarding.health.loading')} />
      ) : checks.error && !checks.data ? (
        <Notice
          status="warn"
          title={t('onboarding.health.loadFailed')}
          actions={
            <Button
              variant="ghost"
              size="sm"
              className="-ml-3"
              onClick={() => {
                updateDrafts((d) => ({ ...d, checks: null }))
                checks.reload()
              }}
            >
              {t('common.retry')}
            </Button>
          }
        >
          {errorMessage(checks.error)}
        </Notice>
      ) : checks.data ? (
        <div className="flex max-w-5xl flex-col gap-8">
          <CheckSummary checks={checks.data.checks} />
          <CheckList checks={checks.data.checks} />
        </div>
      ) : null}
    </StepLayout>
  )
}
