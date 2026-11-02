// SPDX-License-Identifier: AGPL-3.0-or-later
// Modal dialog on the native <dialog> element: showModal() gives the top layer, an inert page
// behind it, focus kept inside, and Escape to close. Square, bordered, on a dimmed backdrop.
import { X } from 'lucide-react'
import { type ReactNode, useEffect, useId, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '../lib/cn'
import { Button } from './Button'
import { Icon } from './Icon'

interface DialogProps {
  open: boolean
  /** Called on Escape, the close button and a click on the backdrop. */
  onClose: () => void
  title: string
  children: ReactNode
  className?: string
}

export function Dialog({ open, onClose, title, children, className }: DialogProps) {
  const { t } = useTranslation()
  const ref = useRef<HTMLDialogElement>(null)
  const titleRef = useRef<HTMLHeadingElement>(null)
  const titleId = useId()

  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (open && !dialog.open) {
      dialog.showModal()
      // The title, not the close button: no ring flashes on open, and the title is read first.
      titleRef.current?.focus()
    }
    if (!open && dialog.open) dialog.close()
  }, [open])

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault() // Escape: React state decides, so the dialog never closes behind its back
        onClose()
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose() // the backdrop: clicks inside land on the content
      }}
      className={cn(
        'm-auto w-[min(32rem,calc(100vw-2rem))] border border-border bg-background p-0 text-foreground',
        // Rendered where it is used (a label-mono footer): reset the inherited type.
        'font-sans text-base font-normal tracking-normal normal-case',
        'backdrop:bg-background/80',
        className,
      )}
    >
      <div className="flex flex-col gap-6 p-6 md:p-8">
        <div className="flex items-start justify-between gap-6">
          <h2 ref={titleRef} id={titleId} tabIndex={-1} className="text-2xl leading-tight font-semibold tracking-tight">
            {title}
          </h2>
          <Button variant="ghost" size="sm" className="-mt-2 -mr-3" icon={<Icon icon={X} />} aria-label={t('common.close')} onClick={onClose} />
        </div>
        {children}
      </div>
    </dialog>
  )
}
