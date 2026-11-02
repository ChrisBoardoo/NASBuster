// SPDX-License-Identifier: AGPL-3.0-or-later
// Settings: language (saved to the account), theme (this browser), data protection
// (snapshots, scrub), notifications (ntfy, webhook), configuration backup, account facts.
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChangePassword, PasswordResetSetting } from '../components/AccountSecurity'
import { CertificateSettings } from '../components/CertificateSettings'
import { ConfigBackups } from '../components/ConfigBackups'
import { JournalSettings } from '../components/JournalSettings'
import { Notice } from '../components/Notice'
import { NotificationsSettings } from '../components/NotificationsSettings'
import { ProtectionSettings } from '../components/ProtectionSettings'
import { LanguageToggle } from '../components/Toggles'
import { Container, PageHeader, SectionTitle } from '../components/Typography'
import { cn } from '../lib/cn'
import { useDocumentTitle, useHashScroll } from '../lib/hooks'
import { useDynamicT } from '../lib/messages'
import { THEMES, useTheme } from '../lib/theme'
import { useFormat } from '../lib/useFormat'
import { useReadySession } from '../session/SessionContext'

export default function Settings() {
  const { t } = useTranslation()
  const { ready } = useReadySession()
  const { td, has } = useDynamicT()
  const format = useFormat()
  const [theme, setTheme] = useTheme()
  const [languageNotSaved, setLanguageNotSaved] = useState(false)
  useDocumentTitle(t('nav.settings'))

  useHashScroll()

  const user = ready.boot.user
  const roleKey = `settings.roles.${user?.role ?? ''}`

  return (
    <Container>
      <PageHeader eyebrow={t('settings.eyebrow')} title={t('settings.title')} />

      <div className="flex flex-col gap-20">
        <section aria-labelledby="set-lang" className="flex flex-col gap-6">
          <SectionTitle id="set-lang" title={t('settings.languageTitle')} />
          <p className="text-muted-foreground">{t('settings.languageHint')}</p>
          <LanguageToggle variant="full" className="-ml-2" onSaveFailed={() => setLanguageNotSaved(true)} />
          {languageNotSaved && <Notice status="warn">{t('language.saveFailed')}</Notice>}
        </section>

        <section aria-labelledby="set-theme" className="flex flex-col gap-6">
          <SectionTitle id="set-theme" title={t('settings.themeTitle')} />
          <p className="text-muted-foreground">{t('settings.themeHint')}</p>
          <div role="radiogroup" aria-labelledby="set-theme" className="grid gap-4 sm:grid-cols-2">
            {THEMES.map((option) => {
              const selected = option === theme
              return (
                <button
                  key={option}
                  type="button"
                  role="radio"
                  aria-checked={selected}
                  onClick={() => setTheme(option)}
                  className={cn(
                    'flex items-center gap-6 border p-6 text-left transition-colors duration-150 ease-crisp md:p-8',
                    selected ? 'border-2 border-accent' : 'border-border hover:border-border-hover',
                  )}
                >
                  {/* A live sample: this element carries the theme's own tokens. */}
                  <span
                    data-theme={option}
                    aria-hidden="true"
                    className="flex h-20 w-24 shrink-0 flex-col justify-between border border-border bg-background p-3 text-foreground"
                  >
                    <span className="h-1 w-8 bg-accent" />
                    <span className="text-2xl leading-none font-semibold tracking-tighter">{t('theme.sample')}</span>
                  </span>
                  <span className="flex flex-col gap-1">
                    <span className="text-2xl font-semibold tracking-tight">{t(`theme.${option}`)}</span>
                    <span className="text-sm text-muted-foreground">{t(`theme.${option}Hint`)}</span>
                  </span>
                </button>
              )
            })}
          </div>
        </section>

        <section aria-labelledby="set-protection" className="flex flex-col gap-6">
          <SectionTitle id="set-protection" title={t('settings.protectionTitle')} />
          <p className="max-w-2xl text-muted-foreground">{t('settings.protectionHint')}</p>
          <ProtectionSettings />
        </section>

        <section id="notifications" aria-labelledby="set-notifications" className="flex scroll-mt-6 flex-col gap-6">
          <SectionTitle id="set-notifications" title={t('settings.notificationsTitle')} />
          <p className="max-w-2xl text-muted-foreground">{t('settings.notificationsHint')}</p>
          <NotificationsSettings />
        </section>

        <section id="config-backup" aria-labelledby="set-backup" className="flex scroll-mt-6 flex-col gap-6">
          <SectionTitle id="set-backup" title={t('settings.backupTitle')} />
          <p className="max-w-2xl text-muted-foreground">{t('settings.backupHint')}</p>
          <ConfigBackups />
        </section>

        <section id="certificate" aria-labelledby="set-certificate" className="flex scroll-mt-6 flex-col gap-6">
          <SectionTitle id="set-certificate" title={t('settings.certificateTitle')} />
          <p className="max-w-2xl text-muted-foreground">{t('settings.certificateHint')}</p>
          <CertificateSettings />
        </section>

        <section id="journal" aria-labelledby="set-journal" className="flex scroll-mt-6 flex-col gap-6">
          <SectionTitle id="set-journal" title={t('settings.journalTitle')} />
          <p className="max-w-2xl text-muted-foreground">{t('settings.journalHint')}</p>
          <JournalSettings />
        </section>

        {user && (
          <section aria-labelledby="set-account" className="flex flex-col gap-6">
            <SectionTitle id="set-account" title={t('settings.accountTitle')} />
            <dl className="border-b border-border">
              <div className="grid gap-2 border-t border-border py-5 md:grid-cols-12 md:gap-6">
                <dt className="label-mono pt-2 text-muted-foreground md:col-span-4">{t('settings.signedInAs')}</dt>
                <dd className="text-2xl font-semibold tracking-tight md:col-span-8 md:text-3xl">{user.username}</dd>
              </div>
              <div className="grid gap-2 border-t border-border py-5 md:grid-cols-12 md:gap-6">
                <dt className="label-mono pt-2 text-muted-foreground md:col-span-4">{t('settings.role')}</dt>
                <dd className="text-2xl font-semibold tracking-tight md:col-span-8 md:text-3xl">
                  {has(roleKey) ? td(roleKey) : user.role}
                </dd>
              </div>
              <div className="grid gap-2 border-t border-border py-5 md:grid-cols-12 md:gap-6">
                <dt className="label-mono pt-2 text-muted-foreground md:col-span-4">{t('settings.memberSince')}</dt>
                <dd className="text-2xl font-semibold tracking-tight md:col-span-8 md:text-3xl">
                  {format.dateTime(user.createdAt)}
                </dd>
              </div>
            </dl>
            <ChangePassword />
            <PasswordResetSetting />
          </section>
        )}
      </div>
    </Container>
  )
}
