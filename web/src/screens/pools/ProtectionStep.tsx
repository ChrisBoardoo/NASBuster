// SPDX-License-Identifier: AGPL-3.0-or-later
// Pool step 1: how the data is protected (layout) and which blank disks hold it, with a live
// capacity preview (POST /storage/pools/plan: debounced, no effect on the disks).
// In the onboarding, pools of a previous CWNSB NAS found on the disks can be imported instead.
import { Check, Eraser, Lock, RefreshCw } from 'lucide-react'
import { useEffect, useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, isAbortError } from '../../api/client'
import { getDisks, getImportable, getLayouts, planPool } from '../../api/endpoints'
import type { Disk, Pool, PoolLayout, PoolPlan } from '../../api/types'
import { useApi } from '../../api/useApi'
import { Button } from '../../components/Button'
import { ConfigBackups } from '../../components/ConfigBackups'
import { sortDisks } from '../../components/DiskList'
import { RoleLabel, diskTitle } from '../../components/DiskRow'
import { Icon } from '../../components/Icon'
import { ImportPools } from '../../components/ImportPools'
import { InlineLoading } from '../../components/Loading'
import { Notice } from '../../components/Notice'
import { StatusLabel } from '../../components/StatusLabel'
import { WipePanel } from '../../components/WipePanel'
import { PosterNumber } from '../../components/Typography'
import { cn } from '../../lib/cn'
import { PLAN_DEBOUNCE_MS } from '../../lib/constants'
import { useDynamicT, useErrorMessage } from '../../lib/messages'
import {
  defaultSelection,
  isLayoutPossible,
  orderLayouts,
  recommendedLayout,
  selectionFits,
} from '../../lib/pools'
import { useFormat } from '../../lib/useFormat'
import { usePoolText } from '../../lib/usePoolText'
import { StepLayout } from '../setup/StepLayout'
import { planKey, useWizard } from '../setup/wizard'

/** Focus ring drawn around a label whose visually-hidden input has keyboard focus. */
const FOCUS_WITHIN =
  'has-[input:focus-visible]:outline-2 has-[input:focus-visible]:outline-offset-2 has-[input:focus-visible]:outline-ring'

export function ProtectionStep() {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const { mode, goNext, goTo, drafts, updateDrafts, persistQuietly, skipPool } = useWizard()
  const draft = drafts.pool

  const layouts = useApi((signal) => getLayouts({ signal }), [])
  const disks = useApi((signal) => getDisks({ signal }), [])
  // Pools of a previous CWNSB NAS on these disks (after a reinstall): offered in the onboarding.
  const importable = useApi(
    (signal) => (mode === 'onboarding' ? getImportable({ signal }) : Promise.resolve({ pools: [] })),
    [mode],
  )
  const [importOpen, setImportOpen] = useState(false)
  const [imported, setImported] = useState<Pool | null>(null)
  const [busy, setBusy] = useState(false)
  const [wiping, setWiping] = useState<Disk | null>(null)

  const allDisks = useMemo(() => sortDisks(disks.data?.disks ?? []), [disks.data])
  const available = useMemo(() => allDisks.filter((disk) => disk.role === 'available'), [allDisks])
  const layoutList = layouts.data?.layouts
  const ordered = useMemo(() => orderLayouts(layoutList ?? [], available.length), [layoutList, available.length])
  const recommended = useMemo(
    () => recommendedLayout(layoutList ?? [], available.length),
    [layoutList, available.length],
  )
  const layout = ordered.find((l) => l.name === draft.layout) ?? null

  // Once both lists are known: pick the recommended default, drop disks that aren't blank anymore.
  useEffect(() => {
    const list = layoutList
    const data = disks.data
    if (!list || !data) return
    const blank = sortDisks(data.disks).filter((disk) => disk.role === 'available')
    const blankIds = new Set(blank.map((disk) => disk.id))
    updateDrafts((d) => {
      const current = list.find((l) => l.name === d.pool.layout)
      const kept = d.pool.disks.filter((id) => blankIds.has(id))
      if (current && isLayoutPossible(current, blank.length)) {
        return kept.length === d.pool.disks.length ? d : { ...d, pool: { ...d.pool, disks: kept } }
      }
      const fallback = recommendedLayout(list, blank.length)
      return {
        ...d,
        pool: { ...d.pool, layout: fallback?.name ?? null, disks: fallback ? defaultSelection(fallback, blank) : [] },
      }
    })
  }, [layoutList, disks.data, updateDrafts])

  // ---- Capacity preview
  const key = planKey(draft.layout, draft.disks)
  const fits = layout ? selectionFits(layout, draft.disks.length) : false
  const layoutName = layout?.name ?? null
  const [planError, setPlanError] = useState<{ key: string; error: unknown } | null>(null)
  const [planLoading, setPlanLoading] = useState(false)
  const cached = draft.plan?.key === key

  useEffect(() => {
    if (!layoutName || !fits || cached) {
      setPlanLoading(false)
      return
    }
    const selection = key.slice(key.indexOf('|') + 1).split(',')
    const controller = new AbortController()
    setPlanLoading(true)
    const timer = window.setTimeout(() => {
      planPool({ layout: layoutName, disks: selection }, { signal: controller.signal }).then(
        (value) => {
          updateDrafts((d) =>
            planKey(d.pool.layout, d.pool.disks) === key ? { ...d, pool: { ...d.pool, plan: { key, value } } } : d,
          )
          setPlanError(null)
          setPlanLoading(false)
        },
        (error: unknown) => {
          if (controller.signal.aborted || isAbortError(error)) return
          setPlanError({ key, error })
          setPlanLoading(false)
        },
      )
    }, PLAN_DEBOUNCE_MS)
    return () => {
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [key, layoutName, fits, cached, updateDrafts])

  const plan = cached ? (draft.plan?.value ?? null) : null
  const ready = Boolean(layout && fits && plan)

  // ---- Choices
  const chooseLayout = (next: PoolLayout) => {
    updateDrafts((d) => {
      if (d.pool.layout === next.name) return d
      const keep = selectionFits(next, d.pool.disks.length)
      return {
        ...d,
        pool: { ...d.pool, layout: next.name, disks: keep ? d.pool.disks : defaultSelection(next, available) },
      }
    })
  }

  const toggleDisk = (id: string) => {
    if (!layout) return
    updateDrafts((d) => {
      const selected = new Set(d.pool.disks)
      if (selected.has(id)) selected.delete(id)
      else {
        if (layout.maxDisks === 1) selected.clear()
        selected.add(id)
      }
      // Keep the list order, whatever the click order.
      return { ...d, pool: { ...d.pool, disks: available.map((disk) => disk.id).filter((x) => selected.has(x)) } }
    })
  }

  const noDisks = Boolean(disks.data) && available.length === 0

  const next = async () => {
    if (noDisks) {
      skipPool()
      return
    }
    if (!ready) return
    setBusy(true)
    await persistQuietly({ currentStep: 'confirm', completedStep: 'protection' })
    goNext()
  }

  const continueLabel = noDisks
    ? mode === 'standalone'
      ? t('onboarding.protection.backToStorage')
      : t('onboarding.protection.continueWithout')
    : t('common.continue')

  const loadError = (layouts.error && !layouts.data) || (disks.error && !disks.data)
  const candidates = importable.data?.pools ?? []

  // A pool was imported: no pool to create, on to the shares.
  if (imported) {
    return (
      <StepLayout
        eyebrow={t('onboarding.protection.eyebrow')}
        title={t('import.onboardingDone', { name: imported.name })}
        intro={t('import.onboardingDoneBody')}
        onContinue={() => {
          setBusy(true)
          void persistQuietly({ currentStep: 'share', completedStep: 'create' }).then(() => goTo('share'))
        }}
        continueLabel={t('common.continue')}
        busy={busy}
      >
        {/* Restore before any account is created (P3): the old users come back with their
            numbers, and nothing new can take them. Continue: start from scratch. */}
        <div className="flex max-w-4xl flex-col gap-6">
          <Notice status="info" title={t('import.restoreLater')}>
            {t('import.restoreLaterBody')}
          </Notice>
          <ConfigBackups previousOnly />
        </div>
      </StepLayout>
    )
  }

  const importBlock =
    candidates.length > 0 ? (
      <div className="flex max-w-5xl flex-col gap-6">
        <Notice
          status="info"
          title={t('import.onboardingTitle', { count: candidates.length })}
          actions={
            !importOpen ? (
              <Button variant="secondary" size="sm" onClick={() => setImportOpen(true)}>
                {t('import.onboardingCta')}
              </Button>
            ) : undefined
          }
        >
          {t('import.onboardingBody')}
        </Notice>
        {importOpen && (
          <ImportPools
            candidates={candidates}
            disks={disks.data?.disks}
            onRefresh={() => {
              importable.reload()
              disks.reload()
            }}
            refreshing={importable.loading}
            onImported={setImported}
          />
        )}
      </div>
    ) : null

  return (
    <StepLayout
      eyebrow={t('onboarding.protection.eyebrow')}
      title={t('onboarding.protection.title')}
      intro={t('onboarding.protection.intro')}
      onContinue={() => void next()}
      continueLabel={continueLabel}
      busy={busy}
      continueDisabled={!noDisks && !ready}
      secondaryAction={
        mode === 'onboarding' && !noDisks ? (
          <Button variant="ghost" onClick={skipPool} disabled={busy}>
            {t('common.skip')}
          </Button>
        ) : undefined
      }
    >
      {importBlock}
      {loadError ? (
        <Notice
          status="warn"
          title={layouts.error ? t('onboarding.protection.previewFailed') : t('disks.loadFailed')}
          actions={
            <Button
              variant="ghost"
              size="sm"
              className="-ml-3"
              onClick={() => {
                layouts.reload()
                disks.reload()
              }}
            >
              {t('common.retry')}
            </Button>
          }
        >
          {errorMessage(layouts.error ?? disks.error)}
        </Notice>
      ) : !layouts.data || !disks.data ? (
        <InlineLoading label={t('disks.loading')} />
      ) : noDisks ? (
        <div className="flex max-w-5xl flex-col gap-10">
          {/* Nothing but the system disk: a connection problem, not a policy one. */}
          {allDisks.every((disk) => disk.role === 'system' || disk.role === 'protected') ? (
            <Notice status="info" title={t('onboarding.protection.noDataDisks.title')}>
              {t('onboarding.protection.noDataDisks.body')}
            </Notice>
          ) : (
            <Notice status="info" title={t('onboarding.protection.noDisks.title')}>
              {t('onboarding.protection.noDisks.body')}
            </Notice>
          )}
          {allDisks.some((disk) => disk.role === 'in_use') && !wiping && (
            <p className="max-w-2xl text-muted-foreground">{t('wipe.preformatted')}</p>
          )}
          {wiping ? (
            <WipePanel
              disk={wiping}
              onCancel={() => setWiping(null)}
              onWiped={() => {
                setWiping(null)
                disks.reload()
              }}
            />
          ) : (
            <DiskPicker
              disks={allDisks}
              available={0}
              selected={[]}
              single={false}
              onToggle={() => undefined}
              onRefresh={disks.reload}
              refreshing={disks.loading}
              onWipe={setWiping}
            />
          )}
        </div>
      ) : (
        <div className="grid gap-x-12 gap-y-14 lg:grid-cols-12">
          <div className="flex min-w-0 flex-col gap-14 lg:col-span-7">
            <LayoutPicker
              layouts={ordered}
              selected={draft.layout}
              recommended={recommended?.name ?? null}
              available={available.length}
              onSelect={chooseLayout}
            />
            <DiskPicker
              disks={allDisks}
              available={available.length}
              selected={draft.disks}
              single={layout?.maxDisks === 1}
              onToggle={toggleDisk}
              onRefresh={disks.reload}
              refreshing={disks.loading}
            />
          </div>
          <div className="min-w-0 lg:col-span-5">
            <div className="lg:sticky lg:top-10">
              <PlanPreview
                layout={layout}
                plan={plan}
                stale={draft.plan?.value ?? null}
                loading={planLoading}
                error={planError?.key === key ? planError.error : null}
                selected={draft.disks.length}
              />
            </div>
          </div>
        </div>
      )}
    </StepLayout>
  )
}

// ---- Layouts ----------------------------------------------------------------

interface LayoutPickerProps {
  layouts: PoolLayout[]
  selected: string | null
  recommended: string | null
  available: number
  onSelect: (layout: PoolLayout) => void
}

function LayoutPicker({ layouts, selected, recommended, available, onSelect }: LayoutPickerProps) {
  const { t } = useTranslation()
  const labelId = useId()
  return (
    <section className="flex flex-col gap-5">
      <h2 id={labelId} className="label-mono text-muted-foreground">
        {t('onboarding.protection.layoutsLabel')}
      </h2>
      <div role="radiogroup" aria-labelledby={labelId} className="border-b border-border">
        {layouts.map((layout) => (
          <LayoutOption
            key={layout.name}
            layout={layout}
            checked={layout.name === selected}
            recommended={layout.name === recommended}
            available={available}
            onSelect={onSelect}
          />
        ))}
      </div>
    </section>
  )
}

interface LayoutOptionProps {
  layout: PoolLayout
  checked: boolean
  recommended: boolean
  available: number
  onSelect: (layout: PoolLayout) => void
}

function LayoutOption({ layout, checked, recommended, available, onSelect }: LayoutOptionProps) {
  const { t } = useTranslation()
  const text = usePoolText()
  const id = useId()
  const possible = isLayoutPossible(layout, available)
  const body = text.layoutBody(layout.name)

  return (
    <label
      className={cn(
        'group relative flex gap-5 border-t border-border py-6 transition-colors duration-150 ease-crisp md:py-7',
        possible ? 'cursor-pointer' : 'cursor-not-allowed',
        FOCUS_WITHIN,
      )}
    >
      <input
        type="radio"
        name="pool-layout"
        value={layout.name}
        checked={checked}
        disabled={!possible}
        onChange={() => onSelect(layout)}
        aria-labelledby={`${id}-title`}
        aria-describedby={`${id}-desc`}
        className="sr-only"
      />
      {/* Accent bar on the top rule marks the choice (plus the filled mark: never color alone). */}
      <span
        aria-hidden="true"
        className={cn(
          'absolute -top-px left-0 h-0.5 w-16 origin-left bg-accent transition-transform duration-150 ease-crisp',
          checked ? 'scale-x-100' : 'scale-x-0',
        )}
      />
      <span
        aria-hidden="true"
        className={cn(
          'mt-1.5 flex h-5 w-5 shrink-0 items-center justify-center border transition-colors duration-150 ease-crisp',
          checked ? 'border-accent' : possible ? 'border-foreground' : 'border-border-hover',
        )}
      >
        {checked && <span className="h-2.5 w-2.5 bg-accent" />}
      </span>
      <span className="flex min-w-0 flex-col gap-3">
        <span className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <span
            id={`${id}-title`}
            className={cn(
              'text-3xl leading-none font-semibold tracking-tighter transition-colors duration-150 ease-crisp md:text-4xl',
              checked
                ? 'text-foreground'
                : possible
                  ? 'text-muted-foreground group-hover:text-foreground'
                  : 'text-muted-foreground opacity-60',
            )}
          >
            {text.layoutTitle(layout.name)}
          </span>
          {recommended && <span className="label-mono text-accent">{t('onboarding.protection.recommended')}</span>}
        </span>
        <span id={`${id}-desc`} className="flex flex-col gap-2">
          <span className="label-mono flex flex-wrap gap-x-3 gap-y-1 text-muted-foreground">
            <span>{text.survives(layout.tolerates)}</span>
            <span aria-hidden="true">·</span>
            <span>{layout.selfHealing ? t('pools.selfHealing') : t('pools.detectOnly')}</span>
            <span aria-hidden="true">·</span>
            <span>{text.diskRange(layout)}</span>
          </span>
          {body && (
            <span className={cn('max-w-xl', checked ? 'text-foreground' : 'text-muted-foreground')}>{body}</span>
          )}
          {!possible && (
            <span className="flex items-center gap-2 text-sm text-muted-foreground">
              <Icon icon={Lock} size={14} className="shrink-0" />
              <span>
                {t('onboarding.protection.needsDisks', { count: layout.minDisks })}{' '}
                {t('onboarding.protection.youHave', { count: available })}
              </span>
            </span>
          )}
        </span>
      </span>
    </label>
  )
}

// ---- Disks ------------------------------------------------------------------

interface DiskPickerProps {
  disks: Disk[]
  available: number
  selected: readonly string[]
  /** Single: exactly one disk, picking another replaces it. */
  single: boolean
  onToggle: (id: string) => void
  onRefresh: () => void
  refreshing: boolean
  /** Offers "Erase…" on disks in use (when no blank disk is left). */
  onWipe?: (disk: Disk) => void
}

function DiskPicker({ disks, available, selected, single, onToggle, onRefresh, refreshing, onWipe }: DiskPickerProps) {
  const { t } = useTranslation()
  const labelId = useId()
  const hintId = useId()
  return (
    <section className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-2">
        <h2 id={labelId} className="label-mono text-muted-foreground">
          {t('onboarding.protection.disksLabel')}
        </h2>
        <Button
          variant="ghost"
          size="sm"
          className="-mr-3"
          icon={<Icon icon={RefreshCw} />}
          onClick={onRefresh}
          busy={refreshing}
        >
          {t('common.refresh')}
        </Button>
      </div>
      <div className="flex flex-col gap-2">
        {available > 0 && (
          <p className="flex items-center gap-3 text-lg font-semibold tracking-tight md:text-xl" aria-live="polite">
            <span aria-hidden="true" className="h-0.5 w-8 shrink-0 bg-accent" />
            <span className="tabular-nums">
              {t('onboarding.protection.selected', { count: selected.length, total: available })}
            </span>
          </p>
        )}
        <p id={hintId} className="max-w-2xl text-sm text-muted-foreground">
          {single ? t('onboarding.protection.disksHintSingle') : t('onboarding.protection.disksHint')}
        </p>
      </div>
      <div role="group" aria-labelledby={labelId} aria-describedby={hintId}>
        <ul className="border-b border-border">
          {disks.map((disk) => (
            <DiskOption key={disk.id} disk={disk} checked={selected.includes(disk.id)} onToggle={onToggle} onWipe={onWipe} />
          ))}
        </ul>
      </div>
    </section>
  )
}

function DiskOption({
  disk,
  checked,
  onToggle,
  onWipe,
}: {
  disk: Disk
  checked: boolean
  onToggle: (id: string) => void
  onWipe?: (disk: Disk) => void
}) {
  const { t } = useTranslation()
  const { td, has } = useDynamicT()
  const format = useFormat()
  const id = useId()
  const selectable = disk.role === 'available'
  const size = format.bytesParts(disk.sizeBytes)
  const roleHintKey = `disks.roleHint.${disk.role}`

  return (
    <li className="border-t border-border">
      <label
        className={cn(
          'group flex min-h-11 gap-4 py-5',
          selectable ? 'cursor-pointer' : 'cursor-not-allowed',
          FOCUS_WITHIN,
        )}
      >
        <input
          type="checkbox"
          checked={checked}
          disabled={!selectable}
          onChange={() => onToggle(disk.id)}
          aria-labelledby={`${id}-title`}
          aria-describedby={`${id}-desc`}
          className="sr-only"
        />
        <span
          aria-hidden="true"
          className={cn(
            'mt-1 flex h-5 w-5 shrink-0 items-center justify-center border transition-colors duration-150 ease-crisp',
            checked
              ? 'border-accent bg-accent text-accent-foreground'
              : selectable
                ? 'border-foreground group-hover:border-accent'
                : 'border-border-hover',
          )}
        >
          {checked && <Icon icon={Check} size={14} strokeWidth={2.5} />}
          {!selectable && <Icon icon={Lock} size={12} className="text-muted-foreground" />}
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-1.5">
          <span className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1">
            <span
              id={`${id}-title`}
              className={cn(
                'min-w-0 text-lg font-semibold tracking-tight break-words md:text-xl',
                !selectable && 'text-muted-foreground',
              )}
            >
              {diskTitle(disk, t('common.unknown'))}
              <span className="sr-only">, {format.bytes(disk.sizeBytes)}</span>
            </span>
            <PosterNumber
              value={size.value}
              unit={size.unit}
              className={cn('text-3xl leading-none font-semibold tracking-tighter', !selectable && 'opacity-60')}
              unitClassName="text-xs md:text-xs"
            />
          </span>
          <span id={`${id}-desc`} className="flex flex-col gap-1.5">
            <span className="font-mono text-xs break-all text-muted-foreground">{disk.id}</span>
            <span className="flex flex-wrap gap-x-5 gap-y-1">
              <RoleLabel role={disk.role} />
              {disk.warnings.length > 0 && (
                <StatusLabel status="warn" label={t('disks.warnings', { count: disk.warnings.length })} />
              )}
            </span>
            {!selectable && has(roleHintKey) && <span className="text-sm text-muted-foreground">{td(roleHintKey)}</span>}
            {selectable &&
              disk.warnings.map((warning) => {
                const key = `disks.warning.${warning}`
                return (
                  <span key={warning} className="max-w-xl text-sm text-muted-foreground">
                    {has(key) ? td(key) : warning}
                  </span>
                )
              })}
          </span>
        </span>
      </label>
      {onWipe && disk.role === 'in_use' && !disk.usage.some((u) => u.kind === 'mounted') && (
        <div className="pb-4 pl-9">
          <Button variant="ghost" size="sm" className="-ml-3" icon={<Icon icon={Eraser} />} onClick={() => onWipe(disk)}>
            {t('wipe.offer')}
          </Button>
        </div>
      )}
    </li>
  )
}

// ---- Preview ----------------------------------------------------------------

interface PlanPreviewProps {
  layout: PoolLayout | null
  /** Preview for the current selection. */
  plan: PoolPlan | null
  /** Last known preview (shown dimmed while the next one is computed). */
  stale: PoolPlan | null
  loading: boolean
  error: unknown
  selected: number
}

function PlanPreview({ layout, plan, stale, loading, error, selected }: PlanPreviewProps) {
  const { t } = useTranslation()
  const text = usePoolText()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const headingId = useId()

  const fits = layout ? selectionFits(layout, selected) : false
  const shown = plan ?? (loading && fits ? stale : null)
  const size = shown ? format.bytesParts(shown.usableBytes) : null
  const tolerates = plan?.tolerates ?? layout?.tolerates ?? 0
  const selfHealing = plan?.selfHealing ?? layout?.selfHealing ?? false
  const countProblem =
    layout && !fits ? new ApiError(400, 'validation.disk_count', '', { min: layout.minDisks, max: layout.maxDisks }) : null

  return (
    <section aria-labelledby={headingId} className="relative flex flex-col gap-6 border-t border-border pt-6">
      <span aria-hidden="true" className="absolute -top-px left-0 h-0.5 w-16 bg-accent" />
      <h2 id={headingId} className="label-mono text-muted-foreground">
        {t('onboarding.protection.previewTitle')}
      </h2>
      <div className="flex flex-col gap-3">
        <p
          className={cn(
            'text-[length:clamp(4rem,15vw,7rem)] leading-[0.9] font-semibold tracking-tighter transition-opacity duration-150 ease-crisp',
            (!plan || loading) && 'opacity-40',
          )}
        >
          {size ? <PosterNumber value={size.value} unit={size.unit} /> : <span aria-hidden="true">—</span>}
        </p>
        <p className="min-h-6 text-sm text-muted-foreground" aria-hidden={loading || undefined}>
          {loading
            ? t('onboarding.protection.previewLoading')
            : plan
              ? t('onboarding.protection.previewRaw', { raw: format.bytes(plan.rawBytes) })
              : countProblem
                ? null
                : t('onboarding.protection.previewPick')}
        </p>
      </div>

      {layout && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 border-t border-border pt-5">
          <dt className="label-mono pt-0.5 text-muted-foreground">{t('onboarding.protection.survivesLabel')}</dt>
          <dd>{text.survives(tolerates)}</dd>
          <dt className="label-mono pt-0.5 text-muted-foreground">{t('onboarding.protection.corruptionLabel')}</dt>
          <dd>{selfHealing ? t('pools.selfHealingLong') : t('pools.detectOnlyLong')}</dd>
        </dl>
      )}

      {countProblem && <Notice status="info">{errorMessage(countProblem)}</Notice>}
      {plan &&
        plan.warnings.map((warning) => (
          <Notice key={warning} status={warning === 'no_redundancy' ? 'warn' : 'info'}>
            {text.planWarning(warning)}
          </Notice>
        ))}
      {error !== null && error !== undefined && (
        <Notice status="crit" title={t('onboarding.protection.previewFailed')}>
          {errorMessage(error)}
        </Notice>
      )}

      {/* One calm announcement per settled preview. */}
      <p className="sr-only" aria-live="polite">
        {plan && size && !loading
          ? t('onboarding.protection.previewLive', {
              size: `${size.value} ${size.unit}`,
              survives: text.survives(plan.tolerates),
            })
          : ''}
      </p>
    </section>
  )
}
