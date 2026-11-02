// SPDX-License-Identifier: AGPL-3.0-or-later
// Layout and typographic primitives shared by every screen.
import { type ReactNode, useEffect, useRef } from 'react'
import { cn } from '../lib/cn'

/** 1200px column with the uxui_chart gutters (24 / 48 / 64px). */
export function Container({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn('mx-auto w-full max-w-page px-6 md:px-12 lg:px-16', className)}>{children}</div>
}

/** Small mono label preceded by an accent bar. */
export function Eyebrow({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <p className={cn('label-mono flex items-center gap-3 text-muted-foreground', className)}>
      <span aria-hidden="true" className="h-0.5 w-8 shrink-0 bg-accent" />
      <span>{children}</span>
    </p>
  )
}

interface PageHeaderProps {
  eyebrow: string
  title: string
  intro?: ReactNode
  aside?: ReactNode
}

/** Poster-scale page title. Takes focus on mount so keyboard and screen-reader users land on it. */
export function PageHeader({ eyebrow, title, intro, aside }: PageHeaderProps) {
  const headingRef = useRef<HTMLHeadingElement>(null)
  useEffect(() => {
    headingRef.current?.focus({ preventScroll: true })
  }, [])

  return (
    <header className="flex flex-col gap-6 pt-12 pb-10 md:pt-20 md:pb-14">
      <Eyebrow>{eyebrow}</Eyebrow>
      <div className="flex flex-col gap-6 lg:flex-row lg:items-end lg:justify-between">
        <h1
          ref={headingRef}
          tabIndex={-1}
          className="text-5xl leading-none font-semibold tracking-tighter text-balance md:text-7xl lg:text-8xl"
        >
          {title}
        </h1>
        {aside && <div className="shrink-0">{aside}</div>}
      </div>
      {intro && <p className="max-w-2xl text-lg text-muted-foreground md:text-xl">{intro}</p>}
    </header>
  )
}

/** Section heading over a full-width rule. */
export function SectionTitle({ title, aside, id }: { title: string; aside?: ReactNode; id?: string }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-2 border-b border-border pb-4">
      <h2 id={id} className="text-2xl font-semibold tracking-tight md:text-3xl">
        {title}
      </h2>
      {aside}
    </div>
  )
}

/** A value and its unit, set as a poster number (tabular figures + mono unit). */
export function PosterNumber({
  value,
  unit,
  className,
  unitClassName,
}: {
  value: string
  unit?: string
  className?: string
  unitClassName?: string
}) {
  return (
    <span className={cn('inline-flex items-baseline gap-1.5 tabular-nums', className)}>
      <span>{value}</span>
      {unit && (
        <span
          className={cn(
            'font-mono text-sm font-medium tracking-widest text-muted-foreground uppercase md:text-base',
            unitClassName,
          )}
        >
          {unit}
        </span>
      )}
    </span>
  )
}
