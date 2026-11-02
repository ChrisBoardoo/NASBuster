// SPDX-License-Identifier: AGPL-3.0-or-later
// The end of a long task's log (Docker installation, image pulls), in a small mono box that
// follows the last line. Scrolls inside itself, never the page.
import { useEffect, useRef } from 'react'
import { cn } from '../../lib/cn'

interface JobLogProps {
  lines: readonly string[]
  /** Accessible name ("Installation log"). */
  label: string
  className?: string
}

export function JobLog({ lines, label, className }: JobLogProps) {
  const ref = useRef<HTMLPreElement>(null)
  const text = lines.join('\n')

  useEffect(() => {
    const el = ref.current
    if (el) el.scrollTop = el.scrollHeight
  }, [text])

  if (lines.length === 0) return null
  return (
    <pre
      ref={ref}
      // Focusable so the keyboard can scroll it.
      tabIndex={0}
      aria-label={label}
      className={cn(
        'max-h-48 max-w-full overflow-auto border border-border bg-muted px-4 py-3 font-mono text-xs leading-relaxed text-muted-foreground',
        className,
      )}
    >
      {text}
    </pre>
  )
}
