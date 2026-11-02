// SPDX-License-Identifier: AGPL-3.0-or-later
// The footer's "CrossWax NasBuster": who makes it, opened as a small modal.
import { Heart } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Dialog } from './Dialog'
import { Logo } from './Logo'

const CROSSWAX_URL = 'https://www.crosswax.net'

export function AboutDialog({ version }: { version: string }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <>
      <button
        type="button"
        aria-haspopup="dialog"
        aria-label={t('about.open')}
        onClick={() => setOpen(true)}
        className="label-mono inline-flex min-h-11 items-center text-muted-foreground underline decoration-transparent underline-offset-4 transition-colors duration-150 ease-crisp hover:text-foreground hover:decoration-accent"
      >
        {t('app.name')}
      </button>
      <Dialog open={open} onClose={() => setOpen(false)} title={t('app.name')}>
        <Logo size={96} />
        <p className="flex flex-wrap items-center gap-x-2 text-2xl font-semibold tracking-tight">
          <span>{t('about.madeWith')}</span>
          <Heart aria-hidden="true" size={24} className="shrink-0 fill-accent text-accent" />
          <span className="sr-only">{t('about.love')}</span>
          <span>{t('about.by')}</span>
          <a
            href={CROSSWAX_URL}
            target="_blank"
            rel="noopener noreferrer"
            aria-label={t('about.crosswaxLabel')}
            className="text-accent underline decoration-accent underline-offset-4 hover:decoration-foreground"
          >
            CrossWax
          </a>
        </p>
        <p className="label-mono text-muted-foreground">
          {t('app.version', { version })} · {t('app.license')}
        </p>
      </Dialog>
    </>
  )
}
