// SPDX-License-Identifier: AGPL-3.0-or-later
// System: editorial definition list of the machine + the full health check with advice.
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { getSystemChecks, getSystemInfo } from '../api/endpoints'
import { useApi } from '../api/useApi'
import { Button } from '../components/Button'
import { CheckList } from '../components/CheckList'
import { InlineLoading } from '../components/Loading'
import { Notice } from '../components/Notice'
import { Container, PageHeader, SectionTitle } from '../components/Typography'
import { useDocumentTitle, useNow } from '../lib/hooks'
import { useDynamicT, useErrorMessage } from '../lib/messages'
import { useFormat } from '../lib/useFormat'
import { useReadySession } from '../session/SessionContext'

function Entry({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-2 border-t border-border py-5 md:grid-cols-12 md:gap-6">
      <dt className="label-mono pt-2 text-muted-foreground md:col-span-4">{label}</dt>
      <dd className="min-w-0 text-2xl font-semibold tracking-tight break-words md:col-span-8 md:text-3xl">{children}</dd>
    </div>
  )
}

export default function System() {
  const { t } = useTranslation()
  const { ready } = useReadySession()
  const { td, has } = useDynamicT()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const now = useNow(30_000)
  useDocumentTitle(t('nav.system'))

  const info = useApi(
    (signal) => getSystemInfo({ signal }).then((data) => ({ data, bootTs: Date.now() / 1000 - data.uptimeSeconds })),
    [],
  )
  const checks = useApi((signal) => getSystemChecks({ signal }), [])
  const unknown = t('common.unknown')
  const text = (value: string) => value || unknown

  return (
    <Container>
      <PageHeader eyebrow={t('system.eyebrow')} title={t('system.title')} />

      <section aria-labelledby="sys-info" className="flex flex-col gap-6">
        <SectionTitle id="sys-info" title={t('system.infoTitle')} />
        {info.data ? (
          (() => {
            const { data, bootTs } = info.data
            const bootKey = `system.bootKind.${data.bootDevice?.kind}`
            return (
              <dl className="border-b border-border">
                <Entry label={t('system.fields.hostname')}>{text(data.hostname)}</Entry>
                <Entry label={t('system.fields.model')}>
                  {text(data.model)}
                  <span className="label-mono mt-2 block font-normal text-muted-foreground">
                    {t('system.fields.raspberryPi')} · {data.isRaspberryPi ? t('common.yes') : t('common.no')}
                  </span>
                </Entry>
                <Entry label={t('system.fields.os')}>{text(data.os)}</Entry>
                <Entry label={t('system.fields.kernel')}>
                  <span className="font-mono text-xl md:text-2xl">{text(data.kernel)}</span>
                </Entry>
                <Entry label={t('system.fields.arch')}>
                  <span className="font-mono text-xl md:text-2xl">{text(data.arch)}</span>
                </Entry>
                <Entry label={t('system.fields.cpuCores')}>
                  <span className="tabular-nums">{data.cpuCores ? format.number(data.cpuCores) : unknown}</span>
                </Entry>
                <Entry label={t('system.fields.memory')}>
                  <span className="tabular-nums">{data.memTotalBytes ? format.bytes(data.memTotalBytes) : unknown}</span>
                </Entry>
                <Entry label={t('system.fields.uptime')}>
                  <span className="tabular-nums">{format.duration(now / 1000 - bootTs)}</span>
                  <span className="label-mono mt-2 block font-normal text-muted-foreground">
                    {t('metrics.since', { date: format.dateTime(bootTs) })}
                  </span>
                </Entry>
                <Entry label={t('system.fields.timezone')}>{text(data.timezone)}</Entry>
                <Entry label={t('system.fields.bootDevice')}>
                  {has(bootKey) ? td(bootKey) : unknown}
                  {data.bootDevice?.device && (
                    <span className="ml-3 font-mono text-base font-normal text-muted-foreground">
                      {data.bootDevice.device}
                    </span>
                  )}
                </Entry>
                <Entry label={t('system.fields.version')}>
                  <span className="font-mono text-xl md:text-2xl">{ready.boot.version}</span>
                </Entry>
              </dl>
            )
          })()
        ) : info.error ? (
          <Notice
            status="warn"
            title={t('system.loadFailed')}
            actions={
              <Button variant="ghost" size="sm" className="-ml-3" onClick={info.reload}>
                {t('common.retry')}
              </Button>
            }
          >
            {errorMessage(info.error)}
          </Notice>
        ) : (
          <InlineLoading />
        )}
      </section>

      <section aria-labelledby="sys-health" className="mt-20 flex flex-col gap-6">
        <SectionTitle id="sys-health" title={t('system.healthTitle')} />
        {checks.data ? (
          <CheckList checks={checks.data.checks} />
        ) : checks.error ? (
          <Notice
            status="warn"
            actions={
              <Button variant="ghost" size="sm" className="-ml-3" onClick={checks.reload}>
                {t('common.retry')}
              </Button>
            }
          >
            {errorMessage(checks.error)}
          </Notice>
        ) : (
          <InlineLoading />
        )}
      </section>
    </Container>
  )
}
