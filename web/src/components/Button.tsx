// SPDX-License-Identifier: AGPL-3.0-or-later
// Buttons per uxui_chart: primary = text-only accent with animated underline,
// secondary = outline that inverts on hover, ghost = muted text with a hairline underline.
import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { Link } from 'wouter'
import { cn } from '../lib/cn'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost'
export type ButtonSize = 'sm' | 'md' | 'lg'

const BASE =
  'group relative inline-flex min-w-11 select-none items-center justify-center whitespace-nowrap font-semibold uppercase tracking-wider ' +
  'transition-all duration-150 ease-crisp active:translate-y-px ' +
  'disabled:pointer-events-none disabled:opacity-50 aria-disabled:pointer-events-none aria-disabled:opacity-50'

const VARIANTS: Record<ButtonVariant, string> = {
  primary: 'px-0 text-accent',
  secondary: 'border border-foreground text-foreground hover:bg-foreground hover:text-background',
  ghost: 'text-muted-foreground hover:text-foreground',
}

// Every size keeps a 44px minimum touch target.
const SIZES: Record<ButtonVariant, Record<ButtonSize, string>> = {
  primary: {
    sm: 'min-h-11 py-2 text-xs',
    md: 'min-h-11 py-3 text-sm',
    lg: 'min-h-12 py-4 text-base md:min-h-14',
  },
  secondary: {
    sm: 'min-h-11 px-4 text-xs',
    md: 'min-h-12 px-6 text-sm',
    lg: 'min-h-12 px-8 text-base md:min-h-14',
  },
  ghost: {
    sm: 'min-h-11 px-3 text-xs',
    md: 'min-h-11 px-4 text-sm',
    lg: 'min-h-12 px-4 text-base md:min-h-14',
  },
}

const GAPS: Record<ButtonSize, string> = { sm: 'gap-2', md: 'gap-2.5', lg: 'gap-3' }

export function buttonClasses(variant: ButtonVariant = 'primary', size: ButtonSize = 'md', extra?: string): string {
  return cn(BASE, VARIANTS[variant], SIZES[variant][size], extra)
}

interface ContentProps {
  variant: ButtonVariant
  size: ButtonSize
  icon?: ReactNode
  iconEnd?: ReactNode
  attention?: boolean
  children: ReactNode
}

/** Label + icons, with the underline hugging the content rather than the 44px box. */
function ButtonContent({ variant, size, icon, iconEnd, attention = false, children }: ContentProps) {
  return (
    <span className={cn('relative inline-flex items-center', GAPS[size])}>
      {icon}
      <span>{children}</span>
      {iconEnd}
      {variant === 'primary' && (
        <span
          aria-hidden="true"
          className={cn(
            'absolute inset-x-0 -bottom-1.5 origin-left scale-x-100 transition-transform duration-150 ease-crisp group-hover:scale-x-110',
            attention ? 'bg-attention animate-sweep h-[3px]' : 'h-0.5 bg-accent',
          )}
        />
      )}
      {variant === 'ghost' && (
        <span
          aria-hidden="true"
          className="absolute inset-x-0 -bottom-1 h-px origin-left scale-x-0 bg-current transition-transform duration-150 ease-crisp group-hover:scale-x-100"
        />
      )}
    </span>
  )
}

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant
  size?: ButtonSize
  icon?: ReactNode
  iconEnd?: ReactNode
  /** Disables the button and marks it busy; pass the busy label as children. */
  busy?: boolean
  /**
   * Primary only: a multicolor sweep on the label and underline, for an action still waiting
   * to be done (unsaved changes). Pair it with words saying so: color alone is not enough.
   */
  attention?: boolean
}

export function Button({
  variant = 'primary',
  size = 'md',
  icon,
  iconEnd,
  busy = false,
  attention = false,
  className,
  type = 'button',
  disabled,
  children,
  ...rest
}: ButtonProps) {
  const sweeping = attention && variant === 'primary' && !busy && !disabled
  return (
    <button
      type={type}
      className={buttonClasses(variant, size, cn(sweeping && 'bg-attention animate-sweep bg-clip-text text-transparent', className))}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      {...rest}
    >
      <ButtonContent variant={variant} size={size} icon={icon} iconEnd={iconEnd} attention={sweeping}>
        {children}
      </ButtonContent>
    </button>
  )
}

export interface ButtonLinkProps {
  href: string
  variant?: ButtonVariant
  size?: ButtonSize
  icon?: ReactNode
  iconEnd?: ReactNode
  className?: string
  children: ReactNode
}

export function ButtonLink({ href, variant = 'primary', size = 'md', icon, iconEnd, className, children }: ButtonLinkProps) {
  return (
    <Link href={href} className={buttonClasses(variant, size, className)}>
      <ButtonContent variant={variant} size={size} icon={icon} iconEnd={iconEnd}>
        {children}
      </ButtonContent>
    </Link>
  )
}

export interface ExternalButtonLinkProps extends ButtonLinkProps {
  /** Accessible name when the visible text isn't enough ("Open Portainer in a new tab"). */
  label?: string
}

/** A link that leaves the UI (an app's own web page on this NAS), opened in a new tab. */
export function ExternalButtonLink({ href, variant = 'primary', size = 'md', icon, iconEnd, className, children, label }: ExternalButtonLinkProps) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" aria-label={label} className={buttonClasses(variant, size, className)}>
      <ButtonContent variant={variant} size={size} icon={icon} iconEnd={iconEnd}>
        {children}
      </ButtonContent>
    </a>
  )
}
