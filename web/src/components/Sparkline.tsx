// SPDX-License-Identifier: AGPL-3.0-or-later
// Inline-SVG sparkline (no chart library). De-emphasized 1.5px line, the latest value
// marked in the accent, a hairline + dot follow the pointer. Gaps (null) break the line.
import { type PointerEvent, useRef } from 'react'
import { useElementWidth } from '../lib/hooks'

interface SparklineProps {
  values: ReadonlyArray<number | null>
  domain: readonly [number, number]
  /** Accessible summary (min, max, current). */
  label: string
  hoverIndex: number | null
  onHoverChange: (index: number | null) => void
  height?: number
}

const PAD_Y = 4

export function Sparkline({ values, domain, label, hoverIndex, onHoverChange, height = 48 }: SparklineProps) {
  const wrapperRef = useRef<HTMLDivElement>(null)
  const width = useElementWidth(wrapperRef)
  const count = values.length
  const [lo, hi] = domain
  const span = hi - lo || 1

  const x = (i: number) => (count <= 1 ? width : (i / (count - 1)) * width)
  const y = (v: number) => {
    const clamped = Math.min(hi, Math.max(lo, v))
    return PAD_Y + (1 - (clamped - lo) / span) * (height - PAD_Y * 2)
  }

  let path = ''
  let penDown = false
  let lastIndex = -1
  values.forEach((value, i) => {
    if (value === null || !Number.isFinite(value)) {
      penDown = false
      return
    }
    path += `${penDown ? 'L' : 'M'}${x(i).toFixed(1)} ${y(value).toFixed(1)}`
    penDown = true
    lastIndex = i
  })

  const hoverValue = hoverIndex !== null ? values[hoverIndex] : null
  const lastValue = lastIndex >= 0 ? values[lastIndex] : null

  const onPointerMove = (event: PointerEvent<SVGSVGElement>) => {
    if (count === 0 || width === 0) return
    const rect = event.currentTarget.getBoundingClientRect()
    const ratio = Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width))
    onHoverChange(Math.round(ratio * (count - 1)))
  }

  return (
    <div ref={wrapperRef} className="w-full">
      {width > 0 && (
        <svg
          width={width}
          height={height}
          viewBox={`0 0 ${width} ${height}`}
          role="img"
          aria-label={label}
          className="block touch-pan-y overflow-visible"
          onPointerMove={onPointerMove}
          onPointerLeave={() => onHoverChange(null)}
        >
          <line x1={0} x2={width} y1={height - 0.5} y2={height - 0.5} className="stroke-border" strokeWidth={1} />
          <path
            d={path}
            fill="none"
            className="stroke-chart-line"
            strokeWidth={1.5}
            strokeLinejoin="round"
            strokeLinecap="round"
          />
          {hoverIndex !== null && hoverValue !== null && hoverValue !== undefined && (
            <>
              <line
                x1={x(hoverIndex)}
                x2={x(hoverIndex)}
                y1={0}
                y2={height}
                className="stroke-border-hover"
                strokeWidth={1}
              />
              <circle cx={x(hoverIndex)} cy={y(hoverValue)} r={3.5} className="fill-foreground stroke-background" strokeWidth={2} />
            </>
          )}
          {lastValue !== null && lastValue !== undefined && hoverIndex === null && (
            <circle cx={x(lastIndex)} cy={y(lastValue)} r={4} className="fill-chart-current stroke-background" strokeWidth={2} />
          )}
        </svg>
      )}
    </div>
  )
}
