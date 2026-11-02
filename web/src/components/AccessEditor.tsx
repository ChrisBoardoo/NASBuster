// SPDX-License-Identifier: AGPL-3.0-or-later
// Who can open a share: one row per NAS user, with a three-way choice
// (no access / read only / read & write). Radio semantics, arrow keys included.
import { type KeyboardEvent, useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { AccessMode, ShareAccess } from '../api/types'
import { cn } from '../lib/cn'
import { modeOf, withMode } from '../lib/shares'

const CHOICES = ['none', 'ro', 'rw'] as const
type Choice = (typeof CHOICES)[number]

interface AccessEditorProps {
  users: readonly string[]
  access: readonly ShareAccess[]
  onChange: (access: ShareAccess[]) => void
  disabled?: boolean
}

export function AccessEditor({ users, access, onChange, disabled = false }: AccessEditorProps) {
  return (
    <ul className="border-b border-border">
      {users.map((user) => (
        <AccessRow
          key={user}
          user={user}
          mode={modeOf(access, user)}
          disabled={disabled}
          onChange={(mode) => onChange(withMode(access, user, mode))}
        />
      ))}
    </ul>
  )
}

function AccessRow({
  user,
  mode,
  disabled,
  onChange,
}: {
  user: string
  mode: AccessMode | null
  disabled: boolean
  onChange: (mode: AccessMode | null) => void
}) {
  const { t } = useTranslation()
  const labelId = useId()
  const current: Choice = mode ?? 'none'

  const pick = (choice: Choice) => onChange(choice === 'none' ? null : choice)
  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    const delta = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 0
    if (!delta) return
    event.preventDefault()
    const next = CHOICES[(CHOICES.indexOf(current) + delta + CHOICES.length) % CHOICES.length] ?? 'none'
    pick(next)
    const group = event.currentTarget.parentElement
    window.requestAnimationFrame(() => group?.querySelector<HTMLButtonElement>(`[data-choice="${next}"]`)?.focus())
  }

  return (
    <li className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 border-t border-border py-3">
      <span id={labelId} className="min-w-0 text-lg font-semibold tracking-tight break-all">
        {user}
      </span>
      <div role="radiogroup" aria-labelledby={labelId} className="flex border border-border">
        {CHOICES.map((choice) => {
          const checked = choice === current
          return (
            <button
              key={choice}
              type="button"
              role="radio"
              aria-checked={checked}
              data-choice={choice}
              tabIndex={checked ? 0 : -1}
              disabled={disabled}
              onClick={() => pick(choice)}
              onKeyDown={onKeyDown}
              className={cn(
                'min-h-11 px-3 text-xs font-semibold tracking-wider whitespace-nowrap uppercase transition-colors duration-150 ease-crisp sm:px-4',
                'disabled:cursor-not-allowed disabled:opacity-50',
                checked
                  ? choice === 'none'
                    ? 'bg-muted text-foreground'
                    : 'bg-foreground text-background'
                  : 'text-muted-foreground hover:text-foreground',
              )}
            >
              {t(`shares.mode.${choice}`)}
            </button>
          )
        })}
      </div>
    </li>
  )
}
