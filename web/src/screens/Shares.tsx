// SPDX-License-Identifier: AGPL-3.0-or-later
// Shares: network folders (\\host\share), who can open them (rw / ro), their snapshots
// ("Previous Versions" in Windows), and the computers connected right now.
import { Camera, FolderInput, History, Monitor, Plus, RefreshCw, Trash2, Users as UsersIcon } from 'lucide-react'
import { type ReactNode, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'wouter'
import { ApiError } from '../api/client'
import {
  createShare,
  createSnapshot,
  deleteShare,
  deleteSnapshot,
  getPools,
  getSessions,
  getShares,
  getSnapshots,
  getUsers,
  setShareAccess,
} from '../api/endpoints'
import type { Pool, SessionsResponse, Share, ShareAccess, Snapshot } from '../api/types'
import { useApi } from '../api/useApi'
import { usePolling } from '../api/usePolling'
import { AccessEditor } from '../components/AccessEditor'
import { Button, ButtonLink } from '../components/Button'
import { CopyText } from '../components/CopyText'
import { Field } from '../components/Field'
import { Icon } from '../components/Icon'
import { InlineLoading } from '../components/Loading'
import { ModeLabel } from '../components/ModeLabel'
import { Notice } from '../components/Notice'
import { ConfirmName } from '../components/ConfirmName'
import { NEW_POOL_PATH } from '../components/PoolList'
import { PoolSelect } from '../components/PoolSelect'
import { StatusLabel } from '../components/StatusLabel'
import { RECYCLE_DEFAULT_DAYS, RecycleFields, RecyclePanel, parseDays } from '../components/RecycleBin'
import { TimeMachineFields, TimeMachinePanel, parseLimit } from '../components/TimeMachine'
import { Container, PageHeader, SectionTitle } from '../components/Typography'
import { useDocumentTitle, useHashScroll, useNow } from '../lib/hooks'
import { useErrorMessage } from '../lib/messages'
import { connectionsTo, dialectLabel, sameAccess, shareAnchorId, sinceToUnix, uncPath } from '../lib/shares'
import { poolCapacity } from '../lib/pools'
import { useFormat } from '../lib/useFormat'
import { SHARE_NAME_MAX, shareNameProblem } from '../lib/validation'
import { useReadySession } from '../session/SessionContext'

const SESSIONS_POLL_MS = 5000

export default function Shares() {
  const { t } = useTranslation()
  useDocumentTitle(t('nav.shares'))
  useHashScroll()
  const errorMessage = useErrorMessage()
  const shares = useApi((signal) => getShares({ signal }), [])
  const users = useApi((signal) => getUsers({ signal }), [])
  const pools = useApi((signal) => getPools({ signal }), [])
  const sessions = usePolling((signal) => getSessions({ signal }), SESSIONS_POLL_MS)
  const [creating, setCreating] = useState(false)
  const [created, setCreated] = useState<string | null>(null)
  const [deleted, setDeleted] = useState<string | null>(null)

  const userNames = useMemo(() => (users.data?.users ?? []).map((u) => u.name), [users.data])
  const poolList = pools.data?.pools ?? []
  const hasPool = poolList.length > 0

  const refresh = () => {
    shares.reload()
    users.reload()
    pools.reload()
    sessions.refresh()
  }

  return (
    <Container>
      <PageHeader
        eyebrow={t('shares.eyebrow')}
        title={t('shares.title')}
        intro={t('shares.intro')}
        aside={
          <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
            <Button
              variant="secondary"
              size="sm"
              icon={<Icon icon={RefreshCw} />}
              onClick={refresh}
              busy={shares.loading || users.loading}
            >
              {t('common.refresh')}
            </Button>
            {hasPool && !creating && (
              <Button size="sm" icon={<Icon icon={Plus} />} onClick={() => setCreating(true)}>
                {t('shares.newShare')}
              </Button>
            )}
          </div>
        }
      />

      {creating && (
        <NewShare
          pools={poolList}
          users={userNames}
          onCancel={() => setCreating(false)}
          onCreated={(share) => {
            setCreating(false)
            setCreated(share.name)
            shares.reload()
            users.reload()
          }}
        />
      )}
      {created && !creating && (
        <Notice status="ok" className="mb-10">
          {t('shares.created', { name: created })}
        </Notice>
      )}
      {deleted && (
        <Notice status="ok" className="mb-10">
          {t('shareDelete.done', { name: deleted })}
        </Notice>
      )}

      <section aria-labelledby="shares-list" className="flex flex-col gap-6">
        <SectionTitle id="shares-list" title={t('shares.listTitle')} />
        {shares.loading && !shares.data ? (
          <InlineLoading />
        ) : shares.error && !shares.data ? (
          <Notice
            status="warn"
            title={t('shares.loadFailed')}
            actions={
              <Button variant="ghost" size="sm" className="-ml-3" onClick={shares.reload}>
                {t('common.retry')}
              </Button>
            }
          >
            {errorMessage(shares.error)}
          </Notice>
        ) : (shares.data?.shares.length ?? 0) === 0 ? (
          <Empty
            title={hasPool || !pools.data ? t('shares.empty.title') : t('shares.noPool.title')}
            body={hasPool || !pools.data ? t('shares.empty.body') : t('shares.noPool.body')}
            action={
              hasPool ? (
                !creating && (
                  <Button size="lg" icon={<Icon icon={Plus} />} onClick={() => setCreating(true)}>
                    {t('shares.newShare')}
                  </Button>
                )
              ) : pools.data ? (
                <ButtonLink href={NEW_POOL_PATH} size="lg">
                  {t('storage.createPool')}
                </ButtonLink>
              ) : null
            }
          />
        ) : (
          <ul className="border-b border-border">
            {shares.data?.shares.map((share) => (
              <ShareRow
                key={share.name}
                share={share}
                users={userNames}
                freeBytes={poolCapacity(poolList.find((p) => p.name === share.pool)?.status)?.free ?? null}
                sessions={sessions.data}
                onChanged={shares.reload}
                onDeleted={(name) => {
                  setCreated(null)
                  setDeleted(name)
                  shares.reload()
                  users.reload()
                }}
              />
            ))}
          </ul>
        )}
      </section>

      <section aria-labelledby="shares-sessions" className="mt-20 flex flex-col gap-6">
        <SectionTitle
          id="shares-sessions"
          title={t('shares.sessions.title')}
          aside={
            <span className="label-mono inline-flex items-center gap-2 text-muted-foreground">
              <span aria-hidden="true" className="h-2 w-2 animate-blink bg-status-ok" />
              {t('shares.sessions.live')}
            </span>
          }
        />
        <SessionList sessions={sessions.data} error={sessions.error} />
      </section>
    </Container>
  )
}

function Empty({ title, body, action }: { title: string; body: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-start gap-5 py-6">
      <p className="text-4xl font-semibold tracking-tight md:text-5xl">{title}</p>
      <p className="max-w-xl text-lg text-muted-foreground">{body}</p>
      {action}
    </div>
  )
}

// ---- New share ---------------------------------------------------------------

function NewShare({
  pools,
  users,
  onCancel,
  onCreated,
}: {
  pools: Pool[]
  users: string[]
  onCancel: () => void
  onCreated: (share: Share) => void
}) {
  const { t } = useTranslation()
  const { ready } = useReadySession()
  const errorMessage = useErrorMessage()
  const headingId = useId()
  const mounted = pools.filter((p) => p.status?.mounted !== false)
  const [name, setName] = useState('')
  const [pool, setPool] = useState(mounted[0]?.name ?? pools[0]?.name ?? '')
  const [access, setAccess] = useState<ShareAccess[]>(() => users.map((user) => ({ user, mode: 'rw' })))
  // On by default: it catches what the hourly snapshots miss.
  const [recycle, setRecycle] = useState(true)
  const [recycleDays, setRecycleDays] = useState(String(RECYCLE_DEFAULT_DAYS))
  const recycleParsed = parseDays(recycleDays)
  const [timeMachine, setTimeMachine] = useState(false)
  const [tmLimit, setTmLimit] = useState('')
  const tmParsed = parseLimit(tmLimit)
  const [touched, setTouched] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  const nameRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    nameRef.current?.focus()
  }, [])

  const problem = shareNameProblem(name)
  const nameError =
    error && (error.code === 'validation.share_name' || error.code === 'shares.exists')
      ? errorMessage(error)
      : problem && (touched || name.length > 0)
        ? t(`shares.nameErrors.${problem}`)
        : null

  const submit = async () => {
    setTouched(true)
    if (problem) {
      nameRef.current?.focus()
      return
    }
    if (timeMachine && tmParsed.problem) return
    if (recycle && !recycleParsed.valid) return
    setBusy(true)
    setError(null)
    try {
      onCreated(
        await createShare({
          name,
          pool,
          access,
          timeMachine,
          timeMachineMaxGiB: timeMachine ? tmParsed.value : 0,
          recycle,
          recycleDays: recycle ? recycleParsed.value : 0,
        }),
      )
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : new ApiError(0, 'unknown', String(caught)))
      setBusy(false)
    }
  }

  const generalError = error && error.code !== 'validation.share_name' && error.code !== 'shares.exists' ? error : null

  return (
    <section aria-labelledby={headingId} className="mb-16 flex flex-col gap-8 border border-border p-6 md:p-8">
      <h2 id={headingId} className="text-3xl font-semibold tracking-tight">
        {t('shares.createTitle')}
      </h2>
      <form
        noValidate
        onSubmit={(event) => {
          event.preventDefault()
          void submit()
        }}
        className="flex flex-col gap-8"
      >
        <div className="grid gap-x-12 gap-y-8 lg:grid-cols-2">
          <Field
            inputRef={nameRef}
            label={t('shares.name')}
            name="new-share"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            maxLength={SHARE_NAME_MAX + 8}
            className="[&_input]:font-mono"
            value={name}
            onChange={(event) => {
              setName(event.target.value.toLowerCase())
              setError(null)
            }}
            hint={t('shares.nameHint')}
            error={nameError}
          >
            <span className="mt-3 font-mono text-lg break-all text-muted-foreground">
              {uncPath(ready.boot.hostname, name || '…')}
            </span>
          </Field>
          <PoolSelect pools={pools} value={pool} onChange={setPool} disabled={busy} />
        </div>
        <div className="flex flex-col gap-3">
          <span className="label-mono text-muted-foreground">{t('shares.access')}</span>
          {users.length > 0 ? (
            <AccessEditor users={users} access={access} onChange={setAccess} disabled={busy} />
          ) : (
            <p className="text-muted-foreground">
              {t('shares.noUsers')}{' '}
              <Link
                href="/users"
                className="inline-flex min-h-11 items-center text-foreground underline decoration-border-hover underline-offset-4 hover:decoration-accent"
              >
                {t('nav.users')}
              </Link>
            </p>
          )}
          {users.length > 0 && access.length === 0 && <p className="text-sm text-status-warn">{t('shares.nobody')}</p>}
        </div>
        <div className="max-w-3xl">
          <RecycleFields enabled={recycle} onEnabled={setRecycle} days={recycleDays} onDays={setRecycleDays} disabled={busy} />
        </div>
        <div className="flex max-w-3xl flex-col gap-5">
          <TimeMachineFields
            enabled={timeMachine}
            onEnabled={setTimeMachine}
            limit={tmLimit}
            onLimit={setTmLimit}
            freeBytes={poolCapacity(pools.find((p) => p.name === pool)?.status)?.free ?? null}
            disabled={busy}
          />
          {timeMachine && (
            <Notice status="warn" title={t('timeMachine.restartTitle')}>
              {t('timeMachine.restartCreate')}
            </Notice>
          )}
        </div>
        {generalError && (
          <Notice status="crit" assertive>
            <span className="text-foreground">{errorMessage(generalError)}</span>
          </Notice>
        )}
        <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
          <Button type="submit" busy={busy} disabled={(timeMachine && tmParsed.problem !== null) || (recycle && !recycleParsed.valid)}>
            {busy ? t('shares.creating') : t('shares.create')}
          </Button>
          <Button variant="ghost" className="-ml-4" onClick={onCancel} disabled={busy}>
            {t('common.cancel')}
          </Button>
        </div>
      </form>
    </section>
  )
}

// ---- One share -------------------------------------------------------------------

interface ShareRowProps {
  onDeleted: (name: string) => void
  share: Share
  users: string[]
  /** Free space of the share's pool (Time Machine size limit). */
  freeBytes: number | null
  sessions: SessionsResponse | undefined
  onChanged: () => void
}

function ShareRow({ share: listed, users, freeBytes, sessions, onChanged, onDeleted }: ShareRowProps) {
  const { t } = useTranslation()
  const { ready } = useReadySession()
  const snapshotsId = useId()
  const [share, setShare] = useState(listed)
  useEffect(() => setShare(listed), [listed])
  const [editing, setEditing] = useState(false)
  const [snapshotsOpen, setSnapshotsOpen] = useState(false)
  const [snapshotsLoaded, setSnapshotsLoaded] = useState(false)
  const connected = connectionsTo(share.name, sessions)
  const path = uncPath(ready.boot.hostname, share.name)

  return (
    <li id={shareAnchorId(listed.name)} className="grid scroll-mt-6 gap-x-12 gap-y-8 border-t border-border py-10 md:py-12 lg:grid-cols-12">
      <div className="flex min-w-0 flex-col gap-4 lg:col-span-5">
        <h3 className="text-5xl leading-[0.95] font-semibold tracking-tighter break-all lg:text-6xl">{share.name}</h3>
        <CopyText value={path} label={t('common.copyValue', { value: path })} />
        <dl className="grid grid-cols-[minmax(5rem,auto)_1fr] gap-x-6 gap-y-2 text-sm">
          <dt className="label-mono pt-0.5 text-muted-foreground">{t('shares.pool')}</dt>
          <dd>{share.pool}</dd>
          <dt className="label-mono pt-0.5 text-muted-foreground">{t('shares.path')}</dt>
          <dd className="font-mono break-all">{share.path}</dd>
        </dl>
        <StatusLabel
          status={connected.length > 0 ? 'ok' : 'unknown'}
          label={connected.length > 0 ? t('shares.connected', { count: connected.length }) : t('shares.noneConnected')}
        />
      </div>

      <div className="flex min-w-0 flex-col gap-8 lg:col-span-7">
        <section className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2">
            <h4 className="label-mono text-muted-foreground">{t('shares.access')}</h4>
            {!editing && (
              <Button variant="ghost" size="sm" className="-mr-3" icon={<Icon icon={UsersIcon} />} onClick={() => setEditing(true)}>
                {t('shares.editAccess')}
              </Button>
            )}
          </div>
          {editing ? (
            <AccessForm
              share={share}
              users={users}
              onCancel={() => setEditing(false)}
              onSaved={(updated) => {
                setShare(updated)
                setEditing(false)
                onChanged()
              }}
            />
          ) : share.access.length === 0 ? (
            <p className="text-status-warn">{t('shares.nobody')}</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {share.access.map((entry) => (
                <li key={entry.user} className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1 border-t border-border pt-2">
                  <span className="text-lg font-semibold tracking-tight">{entry.user}</span>
                  <ModeLabel mode={entry.mode} />
                </li>
              ))}
            </ul>
          )}
        </section>

        <RecyclePanel
          share={share}
          onSaved={(updated) => {
            setShare(updated)
            onChanged()
          }}
        />

        <TimeMachinePanel
          share={share}
          connected={sessions?.sessions.length ?? 0}
          freeBytes={freeBytes}
          onSaved={(updated) => {
            setShare(updated)
            onChanged()
          }}
        />

        <div className="flex flex-wrap items-start gap-x-4">
          <ButtonLink href={`/import?share=${encodeURIComponent(share.name)}`} variant="ghost" size="sm" className="-ml-3" icon={<Icon icon={FolderInput} />}>
            {t('copy.fromDisk')}
          </ButtonLink>
        </div>

        <div>
          <Button
            variant="ghost"
            size="sm"
            className="-ml-3"
            icon={<Icon icon={History} />}
            aria-expanded={snapshotsOpen}
            aria-controls={snapshotsId}
            onClick={() => {
              setSnapshotsOpen((open) => !open)
              setSnapshotsLoaded(true)
            }}
          >
            {snapshotsOpen ? t('shares.snapshots.hide') : t('shares.snapshots.button')}
          </Button>
          <div id={snapshotsId} hidden={!snapshotsOpen} className="pt-4">
            {snapshotsLoaded && <SnapshotsPanel share={share.name} timeMachine={share.timeMachine} />}
          </div>
        </div>

        <DeleteShare share={share} connected={connected} onDeleted={() => onDeleted(share.name)} />
      </div>
    </li>
  )
}

function AccessForm({
  share,
  users,
  onCancel,
  onSaved,
}: {
  share: Share
  users: string[]
  onCancel: () => void
  onSaved: (share: Share) => void
}) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  // Users listed on the share but unknown now still appear, so nothing is dropped silently.
  const everyone = useMemo(() => [...new Set([...users, ...share.access.map((a) => a.user)])], [users, share.access])
  const [access, setAccess] = useState<ShareAccess[]>(share.access)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const unchanged = sameAccess(access, share.access)

  const save = async () => {
    setBusy(true)
    setError(null)
    try {
      onSaved(await setShareAccess(share.name, access))
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  if (everyone.length === 0) {
    return (
      <div className="flex flex-col gap-3">
        <p className="text-muted-foreground">{t('shares.noUsers')}</p>
        <div>
          <Button variant="ghost" size="sm" className="-ml-3" onClick={onCancel}>
            {t('common.cancel')}
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <AccessEditor users={everyone} access={access} onChange={setAccess} disabled={busy} />
      {access.length === 0 && <p className="text-sm text-status-warn">{t('shares.nobody')}</p>}
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
      <p className="text-sm text-status-warn empty:hidden" role="status">
        {!unchanged && !busy ? t('shares.unsavedAccess') : ''}
      </p>
      <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
        <Button onClick={() => void save()} busy={busy} disabled={unchanged} attention={!unchanged}>
          {busy ? t('common.saving') : t('shares.saveAccess')}
        </Button>
        <Button variant="ghost" className="-ml-4" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </Button>
      </div>
    </div>
  )
}

// ---- Snapshots ---------------------------------------------------------------------

function SnapshotsPanel({ share, timeMachine }: { share: string; timeMachine: boolean }) {
  const { t } = useTranslation()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const now = useNow(30_000)
  const list = useApi((signal) => getSnapshots(share, { signal }), [share])
  const [taking, setTaking] = useState(false)
  const [message, setMessage] = useState<string | null>(null)
  const [error, setError] = useState<unknown>(null)

  const take = async () => {
    setTaking(true)
    setError(null)
    setMessage(null)
    try {
      await createSnapshot(share)
      setMessage(t('shares.snapshots.taken'))
      list.reload()
    } catch (caught) {
      setError(caught)
    } finally {
      setTaking(false)
    }
  }

  const snapshots = list.data?.snapshots ?? []

  return (
    <div className="flex flex-col gap-5">
      {timeMachine ? (
        <Notice status="info" title={t('timeMachine.snapshotsTitle')}>
          {t('timeMachine.snapshotsBody')}
        </Notice>
      ) : (
        <p className="max-w-xl text-sm text-muted-foreground">{t('shares.snapshots.hint')}</p>
      )}
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
        <Button variant="secondary" size="sm" icon={<Icon icon={Camera} />} onClick={() => void take()} busy={taking}>
          {taking ? t('shares.snapshots.taking') : t('shares.snapshots.takeNow')}
        </Button>
        {list.data && (
          <span className="label-mono text-muted-foreground tabular-nums">
            {t('shares.snapshots.count', { count: snapshots.length })}
          </span>
        )}
      </div>
      <p className="text-sm text-foreground empty:hidden" role="status">
        {message ?? ''}
      </p>
      {error !== null && (
        <Notice status="crit" assertive>
          <span className="text-foreground">{errorMessage(error)}</span>
        </Notice>
      )}
      {list.loading && !list.data ? (
        <InlineLoading label={t('shares.snapshots.loading')} />
      ) : list.error && !list.data ? (
        <Notice status="warn">{errorMessage(list.error)}</Notice>
      ) : snapshots.length === 0 ? (
        <p className="text-muted-foreground">{t('shares.snapshots.empty')}</p>
      ) : (
        <ul className="border-b border-border">
          {snapshots.map((snapshot) => (
            <SnapshotRow
              key={snapshot.name}
              share={share}
              snapshot={snapshot}
              now={now}
              onDeleted={() => {
                setMessage(t('shares.snapshots.deleted', { date: format.dateTime(snapshot.time) }))
                list.reload()
              }}
            />
          ))}
        </ul>
      )}
    </div>
  )
}

function SnapshotRow({
  share,
  snapshot,
  now,
  onDeleted,
}: {
  share: string
  snapshot: Snapshot
  now: number
  onDeleted: () => void
}) {
  const { t } = useTranslation()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const confirmRef = useRef<HTMLDivElement>(null)
  const date = format.dateTime(snapshot.time)

  useEffect(() => {
    if (confirming) confirmRef.current?.querySelector('button')?.focus()
  }, [confirming])

  const remove = async () => {
    setBusy(true)
    setError(null)
    try {
      await deleteSnapshot(share, snapshot.name)
      onDeleted()
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <li className="flex flex-col gap-3 border-t border-border py-3">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1">
        <span className="flex min-w-0 flex-col">
          <time dateTime={new Date(snapshot.time * 1000).toISOString()} className="font-semibold tabular-nums">
            {date}
          </time>
          <span className="font-mono text-xs break-all text-muted-foreground">
            {format.relative(snapshot.time, now)} · {snapshot.name}
          </span>
        </span>
        {!confirming && (
          <Button
            variant="ghost"
            size="sm"
            className="-mr-3"
            icon={<Icon icon={Trash2} />}
            onClick={() => setConfirming(true)}
            aria-label={t('shares.snapshots.deleteLabel', { date })}
          >
            {t('shares.snapshots.delete')}
          </Button>
        )}
      </div>
      {confirming && (
        <div className="flex flex-col gap-3 border-l-2 border-status-crit pl-4">
          <p className="text-sm text-foreground">{t('shares.snapshots.confirmDelete', { date })}</p>
          <div ref={confirmRef} className="flex flex-wrap items-center gap-x-6 gap-y-1">
            <Button size="sm" busy={busy} onClick={() => void remove()}>
              {t('shares.snapshots.confirm')}
            </Button>
            <Button variant="ghost" size="sm" className="-ml-3" onClick={() => setConfirming(false)} disabled={busy}>
              {t('common.cancel')}
            </Button>
          </div>
          {error !== null && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
        </div>
      )}
    </li>
  )
}

// ---- Connected now ---------------------------------------------------------------------

function SessionList({ sessions, error }: { sessions: SessionsResponse | undefined; error: unknown }) {
  const { t } = useTranslation()
  const format = useFormat()
  const errorMessage = useErrorMessage()
  const now = useNow(30_000)

  if (!sessions) {
    return error ? <Notice status="warn">{errorMessage(error)}</Notice> : <InlineLoading />
  }
  if (sessions.sessions.length === 0) {
    return <p className="py-4 text-lg text-muted-foreground">{t('shares.sessions.none')}</p>
  }
  return (
    <ul className="border-b border-border">
      {sessions.sessions.map((session, index) => {
        const since = sinceToUnix(session.since)
        const onShares = sessions.connections.filter((c) => c.machine === session.machine).map((c) => c.share)
        return (
          <li
            key={`${session.machine}-${session.address}-${index}`}
            className="grid gap-x-8 gap-y-2 border-t border-border py-5 md:grid-cols-12"
          >
            <p className="flex items-center gap-3 text-2xl font-semibold tracking-tight break-all md:col-span-5">
              <Icon icon={Monitor} size={20} className="shrink-0 text-muted-foreground" />
              {session.machine || session.address}
            </p>
            <div className="flex min-w-0 flex-col gap-1 text-sm md:col-span-7">
              <p className="flex flex-wrap items-center gap-x-4 gap-y-1">
                <span className="font-semibold">{session.user}</span>
                {onShares.length > 0 && <span className="font-mono">{onShares.join(', ')}</span>}
                <StatusLabel
                  status={session.encrypted ? 'ok' : 'warn'}
                  label={session.encrypted ? t('shares.sessions.encrypted') : t('shares.sessions.notEncrypted')}
                />
              </p>
              <p className="font-mono text-xs break-all text-muted-foreground">
                {[dialectLabel(session.dialect), session.address, since ? t('shares.sessions.since', { time: format.relative(since, now) }) : null]
                  .filter(Boolean)
                  .join(' · ')}
              </p>
            </div>
          </li>
        )
      })}
    </ul>
  )
}

// ---- Danger zone: delete the share and its files ----------------------------------------------

function DeleteShare({ share, connected, onDeleted }: { share: Share; connected: ReturnType<typeof connectionsTo>; onDeleted: () => void }) {
  const { t } = useTranslation()
  const errorMessage = useErrorMessage()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const confirmed = typed === share.name

  const remove = async () => {
    if (!confirmed) return
    setBusy(true)
    setError(null)
    try {
      await deleteShare(share.name, typed)
      onDeleted()
    } catch (caught) {
      setError(caught)
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-4 border-t border-border pt-6">
      <h4 className="label-mono text-status-crit">{t('destroy.zone')}</h4>
      {!open ? (
        <div>
          <Button variant="ghost" size="sm" className="-ml-3 text-status-crit" icon={<Icon icon={Trash2} />} onClick={() => setOpen(true)}>
            {t('shareDelete.button')}
          </Button>
        </div>
      ) : (
        <form
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            void remove()
          }}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && event.target instanceof HTMLInputElement) event.preventDefault()
          }}
          className="flex flex-col gap-5 border-l-2 border-status-crit pl-5"
        >
          <p className="text-2xl font-semibold tracking-tight">{t('shareDelete.title', { name: share.name })}</p>
          <Notice status="crit" title={t('shareDelete.warningTitle')}>
            {t('shareDelete.warningBody')}
          </Notice>
          <p className="max-w-2xl text-muted-foreground">{t('shareDelete.copyFirst')}</p>
          {share.timeMachine && <p className="max-w-2xl text-muted-foreground">{t('timeMachine.restartDelete')}</p>}
          {connected.length > 0 && (
            <Notice status="warn" title={t('shareDelete.connected', { count: connected.length })}>
              <ul className="flex flex-col gap-1">
                {connected.map(({ connection, session }) => (
                  <li key={connection.machine} className="font-mono text-sm">
                    {connection.machine}
                    {session?.user ? ` · ${session.user}` : ''}
                  </li>
                ))}
              </ul>
            </Notice>
          )}
          <ConfirmName name={share.name} value={typed} onChange={setTyped} />
          {error !== null && (
            <Notice status="crit" assertive>
              <span className="text-foreground">{errorMessage(error)}</span>
            </Notice>
          )}
          <div className="flex flex-wrap items-center gap-x-8 gap-y-2">
            <Button type="submit" icon={<Icon icon={Trash2} />} busy={busy} disabled={!confirmed}>
              {busy ? t('shareDelete.deleting') : t('shareDelete.confirm', { name: share.name })}
            </Button>
            <Button variant="ghost" className="-ml-4" onClick={() => setOpen(false)} disabled={busy}>
              {t('common.cancel')}
            </Button>
          </div>
        </form>
      )}
    </section>
  )
}
