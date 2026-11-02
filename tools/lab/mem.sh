#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — memory breakdown of the CWNSB processes (read-only).
#   wsl -d cwnsb-lab -- bash /mnt/h/Work/repos/NASBuster/tools/lab/mem.sh
# RssAnon = heap/stacks (what we control); RssFile = mapped binary pages
# (shared, reclaimable). Pss splits shared pages fairly between processes.
set -u
for name in nasbusterd nbhelper; do
  for pid in $(pgrep -x "$name"); do
    printf '%s (pid %s)\n' "$name" "$pid"
    grep -E '^(VmRSS|VmHWM|RssAnon|RssFile):' "/proc/$pid/status" | sed 's/^/  /'
    awk '/^Pss:/ { s += $2 } END { printf "  Pss:\t%8d kB\n", s }' "/proc/$pid/smaps_rollup"
  done
done
