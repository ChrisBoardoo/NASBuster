// SPDX-License-Identifier: AGPL-3.0-or-later
// Technical constants shown verbatim in the UI (commands, paths): not translatable.

/** Ways to recover the setup code on the Pi (shown on the account step). */
export const SETUP_CODE_COMMANDS = [
  'sudo journalctl -u nasbusterd',
  'sudo cat /var/lib/cwnsb/setup-code',
] as const

/** Console way to set a forgotten admin password (ADR-0037), typed on the Pi or over SSH. */
export const RESET_ADMIN_COMMAND = 'sudo nasbusterd -reset-admin'

/** Temperature thresholds, same as the `temperature.*` health check codes. */
export const TEMP_WARN_CELSIUS = 70
export const TEMP_CRIT_CELSIUS = 80

/** Metrics history window kept client-side: 15 minutes at one point every 2 seconds. */
export const METRICS_WINDOW_SECONDS = 15 * 60
export const METRICS_MAX_POINTS = METRICS_WINDOW_SECONDS / 2

export const AUDIT_LIMIT = 100

/** Pools are mounted at /srv/cwnsb/<name> (ADR-0008). */
export const POOL_MOUNT_ROOT = '/srv/cwnsb'
export const DEFAULT_POOL_NAME = 'tank'

/** Capacity preview: wait for the selection to settle before asking the server. */
export const PLAN_DEBOUNCE_MS = 250
/** The creation screen stays at least this long, so a fast creation doesn't flash. */
export const CREATE_MIN_DISPLAY_MS = 1200
/** Pool status polling while a scrub or an md sync runs: every 3 s, for 5 minutes at most. */
export const POOL_POLL_MS = 3000
export const POOL_POLL_MAX_MS = 5 * 60 * 1000
/** Pool usage thresholds (share of the usable space). */
export const POOL_USAGE_WARN = 0.8
export const POOL_USAGE_CRIT = 0.9

/** The full-screen "new pool" flow. */
export const NEW_POOL_PATH = '/storage/pools/new'

/** Apps (ADR-0013): the list every 5 s, every 2 s while a job (deployment, update) runs. */
export const APPS_POLL_MS = 5000
export const JOB_POLL_MS = 2000
/** Docker's own RAM once installed, even with no app (ADR-0013, measured in the lab). */
export const DOCKER_RAM_MB = 150
export const APP_LOG_TAIL = 200
