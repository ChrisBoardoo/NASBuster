// SPDX-License-Identifier: AGPL-3.0-or-later
// Step 5: read-only inventory of the disks (nothing is written during setup).
import { RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getDisks } from '../../api/endpoints'
import { useApi } from '../../api/useApi'
import { Button } from '../../components/Button'
import { DiskList } from '../../components/DiskList'
import { Icon } from '../../components/Icon'
import { StepLayout } from './StepLayout'
import { useWizard } from './wizard'

export function DisksStep() {
  const { t } = useTranslation()
  const { goNext, persistQuietly } = useWizard()
  const disks = useApi((signal) => getDisks({ signal }), [])
  const [busy, setBusy] = useState(false)

  const next = async () => {
    setBusy(true)
    await persistQuietly({ currentStep: 'protection', completedStep: 'disks' })
    goNext()
  }

  return (
    <StepLayout
      eyebrow={t('onboarding.disks.eyebrow')}
      title={t('onboarding.disks.title')}
      intro={t('onboarding.disks.intro')}
      onContinue={() => void next()}
      continueLabel={t('common.continue')}
      busy={busy}
      secondaryAction={
        disks.data ? (
          // Wrapper: the Button's own inline-flex would override a "hidden" class on it.
          <span className="hidden sm:contents">
            <Button variant="ghost" icon={<Icon icon={RefreshCw} />} onClick={disks.reload} disabled={disks.loading}>
              {t('common.refresh')}
            </Button>
          </span>
        ) : undefined
      }
    >
      <DiskList state={disks} />
    </StepLayout>
  )
}
