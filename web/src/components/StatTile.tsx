// SPDX-License-Identifier: AGPL-3.0-or-later
// Poster stat tile: mono label, huge tabular number with a mono unit, a secondary line,
// and an optional sparkline whose caption doubles as the hover read-out.
import { type ReactNode, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Parts } from '../lib/format'
import { useFormat } from '../lib/useFormat'
import { Sparkline } from './Sparkline'
import { PosterNumber } from './Typography'

export interface TileSeries {
  values: ReadonlyArray<number | null>
  /** Unix seconds, same length as values. */
  timestamps: readonly number[]
  domain: readonly [number, number]
  format: (value: number) => string
}

interface StatTileProps {
  label: string
  parts: Parts[]
  sub?: ReactNode
  status?: ReactNode
  series?: TileSeries
}

export function StatTile({ label, parts, sub, status, series }: StatTileProps) {
  return (
    <article className="@container flex min-w-0 flex-col gap-4 border-t border-border pt-5">
      <header className="flex min-h-6 items-center justify-between gap-3">
        <h3 className="label-mono text-muted-foreground">{label}</h3>
        {status}
      </header>
      <p className="flex flex-wrap items-baseline gap-x-3 text-[length:clamp(2.75rem,24cqi,6rem)] leading-none font-semibold tracking-tighter">
        {parts.map((part, index) => (
          <PosterNumber key={index} value={part.value} unit={part.unit} />
        ))}
      </p>
      <div className="min-h-6 text-sm text-muted-foreground tabular-nums">{sub}</div>
      {series && <TileChart label={label} series={series} />}
    </article>
  )
}

function TileChart({ label, series }: { label: string; series: TileSeries }) {
  const { t } = useTranslation()
  const format = useFormat()
  const [hover, setHover] = useState<number | null>(null)

  const { values, timestamps, domain } = series
  const numeric = values.filter((v): v is number => v !== null && Number.isFinite(v))
  const first = timestamps[0]
  const last = timestamps[timestamps.length - 1]
  const minutes = first !== undefined && last !== undefined ? Math.max(1, Math.round((last - first) / 60)) : 0

  if (numeric.length < 2 || last === undefined) {
    return <p className="label-mono h-[4.5rem] pt-6 text-muted-foreground">{t('metrics.waiting')}</p>
  }

  const current = numeric[numeric.length - 1] ?? 0
  const ariaLabel = t('metrics.sparklineLabel', {
    label,
    minutes,
    min: series.format(Math.min(...numeric)),
    max: series.format(Math.max(...numeric)),
    current: series.format(current),
  })

  const hoverValue = hover !== null ? values[hover] : null
  const hoverTs = hover !== null ? timestamps[hover] : undefined

  return (
    <div className="flex flex-col gap-2">
      <Sparkline values={values} domain={domain} label={ariaLabel} hoverIndex={hover} onHoverChange={setHover} />
      <p className="label-mono flex justify-between gap-3 text-muted-foreground" aria-hidden="true">
        {hoverValue !== null && hoverValue !== undefined && hoverTs !== undefined ? (
          <>
            <span className="text-foreground tabular-nums">{series.format(hoverValue)}</span>
            <span className="tabular-nums">{format.relative(hoverTs, last * 1000)}</span>
          </>
        ) : (
          <>
            <span>{t('metrics.window', { minutes })}</span>
            <span>{t('metrics.now')}</span>
          </>
        )}
      </p>
    </div>
  )
}
