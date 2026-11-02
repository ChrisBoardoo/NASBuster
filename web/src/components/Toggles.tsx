// SPDX-License-Identifier: AGPL-3.0-or-later
// Language (EN / FR) and theme switches.
import { Moon, Sun } from 'lucide-react'
import { Fragment } from 'react'
import { useTranslation } from 'react-i18next'
import type { Language } from '../api/types'
import { LANGUAGES, isLanguage } from '../i18n'
import { cn } from '../lib/cn'
import { nextTheme, useTheme } from '../lib/theme'
import { useSession } from '../session/SessionContext'
import { Icon } from './Icon'

interface LanguageToggleProps {
  /** "code" = EN / FR (top bars), "full" = English / Français (welcome screen). */
  variant?: 'code' | 'full'
  onSaveFailed?: () => void
  className?: string
}

export function LanguageToggle({ variant = 'code', onSaveFailed, className }: LanguageToggleProps) {
  const { t, i18n } = useTranslation()
  const { changeLanguage } = useSession()
  const resolved = i18n.resolvedLanguage
  const current: Language = isLanguage(resolved) ? resolved : 'en'

  const select = (lang: Language) => {
    if (lang === current) return
    void changeLanguage(lang).then((saved) => {
      if (!saved) onSaveFailed?.()
    })
  }

  return (
    <div role="group" aria-label={t('language.label')} className={cn('flex items-center', className)}>
      {LANGUAGES.map((lang, index) => {
        const active = lang === current
        return (
          <Fragment key={lang}>
            {index > 0 && (
              <span aria-hidden="true" className="px-1 text-border-hover">
                /
              </span>
            )}
            <button
              type="button"
              lang={lang}
              aria-pressed={active}
              onClick={() => select(lang)}
              className={cn(
                'group relative inline-flex min-h-11 min-w-11 items-center justify-center px-2 transition-colors duration-150 ease-crisp',
                variant === 'code'
                  ? 'font-mono text-xs font-semibold tracking-widest'
                  : 'text-xl font-semibold tracking-tight md:text-2xl',
                active ? 'text-foreground' : 'text-muted-foreground hover:text-foreground',
              )}
            >
              {variant === 'code' ? (
                <>
                  <span aria-hidden="true">{t(`language.code.${lang}`)}</span>
                  <span className="sr-only">{t(`language.code.${lang}`)} {t(`language.${lang}`)}</span>
                </>
              ) : (
                t(`language.${lang}`)
              )}
              <span
                aria-hidden="true"
                className={cn(
                  'absolute inset-x-2 bottom-1.5 h-0.5 origin-left bg-accent transition-transform duration-150 ease-crisp',
                  active ? 'scale-x-100' : 'scale-x-0',
                )}
              />
            </button>
          </Fragment>
        )
      })}
    </div>
  )
}

export function ThemeToggle({ className }: { className?: string }) {
  const { t } = useTranslation()
  const [theme, setTheme] = useTheme()
  const next = nextTheme(theme)
  return (
    <button
      type="button"
      onClick={() => setTheme(next)}
      aria-label={t('theme.switchTo', { theme: t(`theme.${next}`) })}
      title={t('theme.switchTo', { theme: t(`theme.${next}`) })}
      className={cn(
        'inline-flex min-h-11 min-w-11 items-center justify-center text-muted-foreground transition-colors duration-150 ease-crisp hover:text-foreground',
        className,
      )}
    >
      <Icon icon={theme === 'bold' ? Sun : Moon} size={20} />
    </button>
  )
}
