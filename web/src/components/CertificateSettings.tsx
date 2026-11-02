// SPDX-License-Identifier: AGPL-3.0-or-later
// Settings → HTTPS certificate (ADR-0019). The NAS signs its own certificate, so
// browsers warn: the fingerprint lets the user check, in the browser's
// certificate viewer, that the page really comes from this NAS. After a rename
// the certificate is renewed at the next start; "Renew now" does it at once,
// with the same private key, and warns that the browser will ask once more.
import { RefreshCw, ShieldCheck } from 'lucide-react'
import { Fragment, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getCertificate, renewCertificate } from '../api/endpoints'
import type { CertificateView } from '../api/types'
import { useApi } from '../api/useApi'
import { useErrorMessage } from '../lib/messages'
import { useFormat } from '../lib/useFormat'
import { Button } from './Button'
import { Icon } from './Icon'
import { InlineLoading } from './Loading'
import { Notice } from './Notice'

export function CertificateSettings() {
  const { t } = useTranslation()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const view = useApi((signal) => getCertificate({ signal }), [])
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [renewed, setRenewed] = useState<CertificateView | null>(null)
  const [error, setError] = useState<unknown>(null)

  if (!view.data) {
    if (view.error) return <Notice status="crit">{errorMessage(view.error)}</Notice>
    return <InlineLoading />
  }
  const cert = renewed ?? view.data
  if (!cert.enabled) {
    return (
      <Notice status="info">
        <span className="text-foreground">{t('certificate.disabled')}</span>
      </Notice>
    )
  }

  const renew = async () => {
    setBusy(true)
    setError(null)
    try {
      setRenewed(await renewCertificate())
      setConfirming(false)
    } catch (caught) {
      setError(caught)
    } finally {
      setBusy(false)
    }
  }

  const names = [...(cert.dnsNames ?? []), ...(cert.ipAddresses ?? [])]
  const notAfter = cert.notAfter ? Math.floor(Date.parse(cert.notAfter) / 1000) : null

  return (
    <div className="flex flex-col gap-6">
      <dl className="border-b border-border">
        <div className="grid gap-2 border-t border-border py-5 md:grid-cols-12 md:gap-6">
          <dt className="label-mono pt-1 text-muted-foreground md:col-span-4">{t('certificate.fingerprint')}</dt>
          <dd className="md:col-span-8">
            <code className="block font-mono text-sm leading-relaxed text-foreground md:text-base">
              {/* Line breaks only after a colon, never inside a byte. */}
              {(cert.fingerprint ?? '').split(':').map((byte, i) => (
                <Fragment key={i}>
                  {i > 0 && ':'}
                  {i > 0 && <wbr />}
                  {byte}
                </Fragment>
              ))}
            </code>
          </dd>
        </div>
        <div className="grid gap-2 border-t border-border py-5 md:grid-cols-12 md:gap-6">
          <dt className="label-mono pt-1 text-muted-foreground md:col-span-4">{t('certificate.names')}</dt>
          <dd className="flex flex-wrap gap-x-3 gap-y-1 font-mono text-sm text-foreground md:col-span-8">
            {names.map((name) => (
              <span key={name} className="whitespace-nowrap">
                {name}
              </span>
            ))}
          </dd>
        </div>
        {notAfter !== null && (
          <div className="grid gap-2 border-t border-border py-5 md:grid-cols-12 md:gap-6">
            <dt className="label-mono pt-1 text-muted-foreground md:col-span-4">{t('certificate.validUntil')}</dt>
            <dd className="text-foreground md:col-span-8">{format.dateTime(notAfter)}</dd>
          </div>
        )}
      </dl>

      <p className="max-w-2xl text-sm text-muted-foreground">{t('certificate.howToCheck')}</p>

      {!cert.coversHostname && !renewed && (
        <Notice status="warn">
          <span className="text-foreground">{t('certificate.outdated', { hostname: cert.hostname })}</span>
        </Notice>
      )}

      {renewed ? (
        <Notice
          status="ok"
          actions={
            <Button variant="ghost" size="sm" className="-ml-3" onClick={() => window.location.reload()}>
              {t('certificate.reload')}
            </Button>
          }
        >
          <span className="text-foreground">{t('certificate.renewed')}</span>
        </Notice>
      ) : confirming ? (
        <Notice
          status="warn"
          actions={
            <div className="flex flex-wrap gap-2">
              <Button variant="secondary" size="sm" busy={busy} icon={<Icon icon={RefreshCw} />} onClick={() => void renew()}>
                {busy ? t('certificate.renewing') : t('certificate.renewConfirm')}
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setConfirming(false)} disabled={busy}>
                {t('common.cancel')}
              </Button>
            </div>
          }
        >
          <span className="text-foreground">{t('certificate.renewWarning')}</span>
        </Notice>
      ) : (
        <div>
          <Button variant="secondary" size="sm" icon={<Icon icon={ShieldCheck} />} onClick={() => setConfirming(true)}>
            {t('certificate.renew')}
          </Button>
        </div>
      )}

      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
    </div>
  )
}
