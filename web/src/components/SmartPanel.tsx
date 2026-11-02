// SPDX-License-Identifier: AGPL-3.0-or-later
// SMART details for one disk, fetched lazily (the call goes through nbhelper / smartctl).
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { getDiskSmart } from '../api/endpoints'
import type { Smart, SmartAttribute } from '../api/types'
import { useApi } from '../api/useApi'
import { normalizeStatus, useDynamicT, useErrorMessage } from '../lib/messages'
import { useFormat } from '../lib/useFormat'
import { Button } from './Button'
import { InlineLoading } from './Loading'
import { Notice } from './Notice'
import { StatusLabel } from './StatusLabel'

const CRC_ATTRIBUTE = 199

export function SmartPanel({ diskId }: { diskId: string }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const smart = useApi((signal) => getDiskSmart(diskId, { signal }), [diskId])

  return (
    <section aria-label={t('smart.title')} className="border-t border-border pt-6">
      {smart.loading && !smart.data ? (
        <InlineLoading label={t('smart.loading')} className="py-2" />
      ) : smart.error ? (
        <Notice
          status="warn"
          assertive
          actions={
            <Button variant="ghost" size="sm" className="-ml-3" onClick={smart.reload}>
              {t('common.retry')}
            </Button>
          }
        >
          {errorMessage(smart.error)}
        </Notice>
      ) : smart.data ? (
        <SmartDetails smart={smart.data} />
      ) : null}
    </section>
  )
}

function SmartDetails({ smart }: { smart: Smart }) {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()

  if (!smart.available) {
    const reasonKey = `smart.unavailable.${smart.reason}`
    return (
      <Notice status="info" title={t('smart.unavailable.title')}>
        {smart.reason && has(reasonKey) ? td(reasonKey) : null}
      </Notice>
    )
  }

  const attributes = smart.attributes ?? []
  const facts: Array<{ label: string; value: ReactNode }> = []
  if (smart.passed !== undefined) {
    facts.push({
      label: t('smart.overall'),
      value: smart.passed ? (
        <StatusLabel status="ok" label={t('smart.passed')} className="text-sm" />
      ) : (
        <StatusLabel status="crit" label={t('smart.failed')} className="text-sm" />
      ),
    })
  }
  if (typeof smart.temperatureCelsius === 'number') {
    facts.push({ label: t('smart.temperature'), value: format.celsius(smart.temperatureCelsius) })
  }
  if (typeof smart.powerOnHours === 'number') {
    facts.push({
      label: t('smart.powerOnHours'),
      value: (
        <>
          {format.number(smart.powerOnHours)}&nbsp;{t('units.hours')}
          <span className="label-mono mt-1 block font-normal text-muted-foreground">
            {format.hoursAsYears(smart.powerOnHours)}
          </span>
        </>
      ),
    })
  }
  if (smart.deviceType) {
    facts.push({ label: t('smart.deviceType'), value: <span className="font-mono text-base">{smart.deviceType}</span> })
  }

  return (
    <div className="flex flex-col gap-8">
      <dl className="grid grid-cols-1 gap-x-8 gap-y-5 sm:grid-cols-2 lg:grid-cols-4">
        {facts.map((fact) => (
          <div key={fact.label} className="flex flex-col gap-1">
            <dt className="label-mono text-muted-foreground">{fact.label}</dt>
            <dd className="text-xl font-semibold tracking-tight tabular-nums">{fact.value}</dd>
          </div>
        ))}
      </dl>

      {smart.nvme && (
        <div className="flex flex-col gap-3">
          <h4 className="label-mono text-muted-foreground">{t('smart.nvme.title')}</h4>
          <dl className="grid grid-cols-2 gap-x-8 gap-y-4 lg:grid-cols-4">
            <SmartFact label={t('smart.nvme.percentageUsed')} value={`${format.number(smart.nvme.percentageUsed)} ${t('units.percent')}`} />
            <SmartFact label={t('smart.nvme.availableSpare')} value={`${format.number(smart.nvme.availableSpare)} ${t('units.percent')}`} />
            <SmartFact label={t('smart.nvme.mediaErrors')} value={format.number(smart.nvme.mediaErrors)} />
            <SmartFact label={t('smart.nvme.criticalWarning')} value={format.number(smart.nvme.criticalWarning)} />
          </dl>
        </div>
      )}

      {!smart.nvme && (
        <div className="flex flex-col gap-3">
          <h4 className="label-mono text-muted-foreground">{t('smart.attributes')}</h4>
          {attributes.length === 0 ? (
            <p className="text-muted-foreground">{t('smart.noAttributes')}</p>
          ) : (
            <AttributeTable attributes={attributes} />
          )}
        </div>
      )}
    </div>
  )
}

function SmartFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1">
      <dt className="label-mono text-muted-foreground">{label}</dt>
      <dd className="text-xl font-semibold tracking-tight tabular-nums">{value}</dd>
    </div>
  )
}

function AttributeTable({ attributes }: { attributes: SmartAttribute[] }) {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()

  return (
    <ul className="border-b border-border">
      {attributes.map((attr) => {
        const status = normalizeStatus(attr.status)
        const friendlyKey = `smart.attr.${attr.id}`
        const flagged = attr.raw > 0 && (status === 'warn' || status === 'crit')
        return (
          <li key={attr.id} className="grid grid-cols-[3rem_1fr_auto] items-baseline gap-x-4 gap-y-1 border-t border-border py-3">
            <span className="font-mono text-sm text-muted-foreground tabular-nums">{attr.id}</span>
            <span className="min-w-0">
              <span className="block">{has(friendlyKey) ? td(friendlyKey) : attr.name}</span>
              <span className="block truncate font-mono text-xs text-muted-foreground">{attr.name}</span>
            </span>
            <span className="flex flex-col items-end gap-1">
              <span className="text-xl font-semibold tabular-nums">{format.number(attr.raw)}</span>
              <StatusLabel status={status} />
            </span>
            {flagged && (
              <p className="col-span-3 col-start-1 max-w-2xl text-sm text-muted-foreground sm:col-span-2 sm:col-start-2">
                {attr.id === CRC_ATTRIBUTE ? t('smart.attrCrc') : t('smart.attrWarn')}
              </p>
            )}
          </li>
        )
      })}
    </ul>
  )
}
