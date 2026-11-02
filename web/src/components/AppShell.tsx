// SPDX-License-Identifier: AGPL-3.0-or-later
// Authenticated shell: top bar (wordmark, language, theme, alerts, settings, logout), the nav on
// its own row (seven sections don't fit beside the controls in the 1200px column), + footer.
import { LogOut, Settings } from 'lucide-react'
import { m } from 'motion/react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useLocation } from 'wouter'
import { cn } from '../lib/cn'
import { AboutDialog } from './AboutDialog'
import { AlertBell } from './AlertBell'
import { useReveal } from '../lib/motion'
import { useReadySession } from '../session/SessionContext'
import { Icon } from './Icon'
import { LanguageToggle, ThemeToggle } from './Toggles'
import { Container } from './Typography'
import { Wordmark } from './Wordmark'

const NAV = [
  { href: '/', key: 'dashboard' },
  { href: '/storage', key: 'storage' },
  { href: '/shares', key: 'shares' },
  { href: '/users', key: 'users' },
  { href: '/apps', key: 'apps' },
  { href: '/system', key: 'system' },
  { href: '/activity', key: 'activity' },
  { href: '/journal', key: 'journal' },
] as const

function isActive(location: string, href: string): boolean {
  return href === '/' ? location === '/' : location === href || location.startsWith(`${href}/`)
}

export function AppShell({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const { ready, logout } = useReadySession()
  const [location] = useLocation()
  const { screen } = useReveal()
  const { boot } = ready

  return (
    <div className="flex min-h-dvh flex-col">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:fixed focus:top-4 focus:left-4 focus:z-50 focus:bg-background focus:px-4 focus:py-3"
      >
        {t('app.skipToContent')}
      </a>

      <header className="border-b border-border">
        <Container className="flex flex-wrap items-center justify-between gap-x-6">
          <div className="flex min-h-16 min-w-0 shrink-0 items-center gap-4 md:min-h-20">
            <Link href="/" className="inline-flex min-h-11 items-center" aria-label={t('nav.dashboard')}>
              <Wordmark />
            </Link>
            <span className="hidden max-w-48 truncate font-mono text-xs text-muted-foreground lg:inline">\\{boot.hostname}</span>
            {boot.labMode && (
              <span className="label-mono border border-accent px-2 py-0.5 text-accent" title={t('app.labModeHint')}>
                {t('app.labMode')}
              </span>
            )}
            {boot.demo && <span className="label-mono border border-foreground px-2 py-0.5">{t('app.demo')}</span>}
          </div>

          <nav
            aria-label={t('nav.label')}
            className="order-last -mx-6 w-[calc(100%+3rem)] overflow-x-auto border-t border-border px-6 md:-mx-12 md:w-[calc(100%+6rem)] md:px-12 lg:-mx-16 lg:w-[calc(100%+8rem)] lg:px-16"
          >
            <ul className="flex gap-5 sm:gap-6">
              {NAV.map((item) => {
                const active = isActive(location, item.href)
                return (
                  <li key={item.href}>
                    <Link
                      href={item.href}
                      aria-current={active ? 'page' : undefined}
                      className={cn(
                        'relative inline-flex min-h-12 min-w-11 items-center justify-center text-xs font-semibold sm:text-sm tracking-wider whitespace-nowrap uppercase transition-colors duration-150 ease-crisp',
                        active ? 'text-foreground' : 'text-muted-foreground hover:text-foreground',
                      )}
                    >
                      {t(`nav.${item.key}`)}
                      <span
                        aria-hidden="true"
                        className={cn(
                          'absolute inset-x-0 bottom-2 h-0.5 origin-left bg-accent transition-transform duration-150 ease-crisp',
                          active ? 'scale-x-100' : 'scale-x-0',
                        )}
                      />
                    </Link>
                  </li>
                )
              })}
            </ul>
          </nav>

          <div className="flex shrink-0 items-center gap-1">
            <LanguageToggle />
            <ThemeToggle />
            <AlertBell />
            <Link
              href="/settings"
              aria-label={t('nav.settings')}
              title={t('nav.settings')}
              aria-current={isActive(location, '/settings') ? 'page' : undefined}
              className={cn(
                'inline-flex min-h-11 min-w-11 items-center justify-center transition-colors duration-150 ease-crisp',
                isActive(location, '/settings') ? 'text-accent' : 'text-muted-foreground hover:text-foreground',
              )}
            >
              <Icon icon={Settings} size={20} />
            </Link>
            <button
              type="button"
              onClick={() => void logout()}
              className="inline-flex min-h-11 min-w-11 items-center justify-center gap-2 text-muted-foreground transition-colors duration-150 ease-crisp hover:text-foreground md:px-2"
            >
              <Icon icon={LogOut} size={20} />
              <span className="sr-only text-sm font-semibold tracking-wider uppercase md:not-sr-only">{t('nav.logout')}</span>
            </button>
          </div>
        </Container>
      </header>

      <main id="main" className="flex-1">
        <m.div key={location} variants={screen} initial="hidden" animate="show">
          {children}
        </m.div>
      </main>

      <footer className="mt-24 border-t border-border">
        <Container className="label-mono flex min-h-16 flex-wrap items-center justify-between gap-x-6 gap-y-2 py-4 text-muted-foreground">
          <AboutDialog version={boot.version} />
          <span>
            {t('app.version', { version: boot.version })} · {t('app.license')}
          </span>
        </Container>
      </footer>
    </div>
  )
}
