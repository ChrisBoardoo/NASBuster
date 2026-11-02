// SPDX-License-Identifier: AGPL-3.0-or-later
// Motion presets from uxui_chart: fast and decisive, no bounce.
// Reduced motion: no slide, no stagger, instant state changes.
import { useMemo } from 'react'
import { type Variants, useReducedMotion } from 'motion/react'

export const EASE_CRISP: [number, number, number, number] = [0.25, 0, 0, 1]

export interface RevealVariants {
  reduced: boolean
  /** Parent: staggers its children by 80 ms after a 100 ms delay. */
  container: Variants
  /** Child: fade in + slide up 20 px over 500 ms. */
  item: Variants
  /** Whole screens (wizard steps, pages): same entrance, quick exit. */
  screen: Variants
}

export function useReveal(): RevealVariants {
  const reduced = useReducedMotion() ?? false
  return useMemo<RevealVariants>(() => {
    if (reduced) {
      const still: Variants = { hidden: { opacity: 1, y: 0 }, show: { opacity: 1, y: 0 }, exit: { opacity: 1 } }
      return { reduced, container: { hidden: {}, show: {} }, item: still, screen: still }
    }
    return {
      reduced,
      container: {
        hidden: {},
        show: { transition: { staggerChildren: 0.08, delayChildren: 0.1 } },
      },
      item: {
        hidden: { opacity: 0, y: 20 },
        show: { opacity: 1, y: 0, transition: { duration: 0.5, ease: EASE_CRISP } },
      },
      screen: {
        hidden: { opacity: 0, y: 20 },
        show: {
          opacity: 1,
          y: 0,
          transition: { duration: 0.5, ease: EASE_CRISP, staggerChildren: 0.08, delayChildren: 0.1 },
        },
        exit: { opacity: 0, y: -12, transition: { duration: 0.2, ease: EASE_CRISP } },
      },
    }
  }, [reduced])
}
