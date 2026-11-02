// SPDX-License-Identifier: AGPL-3.0-or-later
// One decision per screen: a radio list of big choices (title, size, state, detail), with an
// optional action under a choice. Disabled choices stay visible with their reason.
import type { ReactNode } from 'react'
import { cn } from '../lib/cn'
import { PosterNumber } from './Typography'

export interface ChoiceItem {
  id: string
  title: string
  size?: { value: string; unit: string }
  meta?: ReactNode
  detail?: string | null
  disabled?: boolean
  action?: ReactNode
}

export function ChoiceList({
  label,
  name,
  items,
  value,
  onChange,
}: {
  label: string
  /** The radio group's name (one per screen). */
  name: string
  items: ChoiceItem[]
  value: string | null
  onChange: (id: string) => void
}) {
  return (
    <div role="radiogroup" aria-label={label} className="max-w-4xl border-b border-border">
      {items.map((item) => {
        const checked = item.id === value
        return (
          <div key={item.id} className="border-t border-border">
            <label
              className={cn(
                'group relative flex gap-5 py-5',
                item.disabled ? 'cursor-not-allowed' : 'cursor-pointer',
                'has-[input:focus-visible]:outline-2 has-[input:focus-visible]:outline-offset-2 has-[input:focus-visible]:outline-ring',
              )}
            >
              <input
                type="radio"
                name={name}
                value={item.id}
                checked={checked}
                disabled={item.disabled}
                onChange={() => onChange(item.id)}
                className="sr-only"
              />
              <span
                aria-hidden="true"
                className={cn(
                  'mt-1.5 flex h-5 w-5 shrink-0 items-center justify-center border',
                  checked ? 'border-accent' : item.disabled ? 'border-border-hover' : 'border-foreground',
                )}
              >
                {checked && <span className="h-2.5 w-2.5 bg-accent" />}
              </span>
              <span className="flex min-w-0 flex-1 flex-col gap-1.5">
                <span className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1">
                  <span className={cn('min-w-0 text-xl font-semibold tracking-tight break-words', item.disabled && 'text-muted-foreground')}>
                    {item.title}
                  </span>
                  {item.size && (
                    <PosterNumber
                      value={item.size.value}
                      unit={item.size.unit}
                      className={cn('text-3xl leading-none font-semibold tracking-tighter', item.disabled && 'opacity-60')}
                      unitClassName="text-xs md:text-xs"
                    />
                  )}
                </span>
                {item.meta}
                {item.detail && <span className="text-sm text-muted-foreground">{item.detail}</span>}
              </span>
            </label>
            {item.action && <div className="pb-4 pl-10">{item.action}</div>}
          </div>
        )
      })}
    </div>
  )
}
