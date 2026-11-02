// SPDX-License-Identifier: AGPL-3.0-or-later
import type { LucideIcon, LucideProps } from 'lucide-react'

interface IconProps extends LucideProps {
  icon: LucideIcon
}

/** Lucide icon with the design-system stroke (1.5px). Decorative by default. */
export function Icon({ icon: Glyph, size = 16, ...rest }: IconProps) {
  return <Glyph size={size} strokeWidth={1.5} aria-hidden="true" focusable="false" {...rest} />
}
