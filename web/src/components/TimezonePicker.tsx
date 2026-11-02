// SPDX-License-Identifier: AGPL-3.0-or-later
// Searchable time zone picker (ARIA combobox + listbox). The selection is shown big;
// typing filters the IANA list. Enter picks the highlighted match, Esc clears the search
// (both stop there, so the wizard's Enter/Esc shortcuts don't fire at the same time).
import { Search } from 'lucide-react'
import { type KeyboardEvent, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '../lib/cn'
import { INPUT_CLASSES } from './Field'
import { Icon } from './Icon'

const MAX_RESULTS = 60

function normalize(value: string): string {
  return value.toLowerCase().replace(/[_/\s-]+/g, ' ').trim()
}

interface TimezonePickerProps {
  zones: readonly string[]
  value: string
  onChange: (zone: string) => void
  /** The browser's zone, flagged when selected. */
  detected?: string
}

export function TimezonePicker({ zones, value, onChange, detected }: TimezonePickerProps) {
  const { t } = useTranslation()
  const id = useId()
  const listId = `${id}-list`
  const labelId = `${id}-label`
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const listRef = useRef<HTMLUListElement>(null)

  const matches = useMemo(() => {
    const tokens = normalize(query).split(' ').filter(Boolean)
    if (tokens.length === 0) return []
    return zones.filter((zone) => {
      const haystack = normalize(zone)
      return tokens.every((token) => haystack.includes(token))
    })
  }, [zones, query])

  const shown = matches.slice(0, MAX_RESULTS)
  const open = query.trim().length > 0
  const activeIndex = Math.min(active, Math.max(0, shown.length - 1))

  const choose = (zone: string) => {
    onChange(zone)
    setQuery('')
    setActive(0)
  }

  const moveTo = (index: number) => {
    setActive(index)
    listRef.current?.querySelector(`[data-index="${index}"]`)?.scrollIntoView({ block: 'nearest' })
  }

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (!open) return
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      moveTo(Math.min(shown.length - 1, activeIndex + 1))
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      moveTo(Math.max(0, activeIndex - 1))
    } else if (event.key === 'Enter') {
      event.preventDefault()
      const zone = shown[activeIndex]
      if (zone) choose(zone)
    } else if (event.key === 'Escape') {
      event.preventDefault()
      setQuery('')
    }
  }

  const slash = value.lastIndexOf('/')
  const region = slash > 0 ? value.slice(0, slash + 1) : ''
  const city = (slash > 0 ? value.slice(slash + 1) : value).replace(/_/g, ' ')

  return (
    <div className="flex flex-col gap-3">
      <span id={labelId} className="label-mono text-muted-foreground">
        {t('onboarding.identity.timezone')}
      </span>
      <p className="flex flex-col gap-1" aria-live="polite">
        <span className="text-3xl leading-tight font-semibold tracking-tight break-words md:text-4xl">
          {region && <span className="text-muted-foreground">{region.replace(/_/g, ' ')}</span>}
          {city || '—'}
        </span>
        {detected && value === detected && (
          <span className="label-mono text-muted-foreground">{t('onboarding.identity.timezoneDetected')}</span>
        )}
      </p>

      <div className="relative">
        <Icon icon={Search} size={18} className="pointer-events-none absolute top-1/2 left-4 -translate-y-1/2 text-muted-foreground" />
        <input
          type="text"
          role="combobox"
          aria-labelledby={labelId}
          aria-expanded={open}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={open && shown.length > 0 ? `${id}-opt-${activeIndex}` : undefined}
          placeholder={t('onboarding.identity.timezoneSearch')}
          value={query}
          onChange={(event) => {
            setQuery(event.target.value)
            setActive(0)
          }}
          onKeyDown={onKeyDown}
          autoComplete="off"
          spellCheck={false}
          className={cn(INPUT_CLASSES, 'pl-12')}
        />
      </div>

      <ul
        ref={listRef}
        id={listId}
        role="listbox"
        aria-labelledby={labelId}
        hidden={!open}
        className="max-h-72 overflow-y-auto border border-t-0 border-border"
      >
        {shown.map((zone, index) => (
          <li
            key={zone}
            id={`${id}-opt-${index}`}
            data-index={index}
            role="option"
            aria-selected={zone === value}
            onPointerDown={(event) => event.preventDefault()}
            onClick={() => choose(zone)}
            onPointerMove={() => setActive(index)}
            className={cn(
              'flex min-h-11 cursor-pointer items-center justify-between gap-4 border-t border-border px-4 font-mono text-sm first:border-t-0',
              index === activeIndex ? 'bg-muted text-foreground' : 'text-muted-foreground',
            )}
          >
            <span className="break-all">{zone}</span>
            {zone === value && <span aria-hidden="true" className="h-2 w-2 shrink-0 bg-accent" />}
          </li>
        ))}
      </ul>
      <p className="label-mono text-muted-foreground" aria-live="polite">
        {open &&
          (matches.length === 0
            ? t('onboarding.identity.timezoneNoMatch', { query: query.trim() })
            : t('onboarding.identity.timezoneResults', { count: matches.length }))}
      </p>
    </div>
  )
}
