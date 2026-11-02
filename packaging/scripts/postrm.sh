#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — after removal.
#
# DATA SAFETY: removing (even purging) the package NEVER touches pools, shares,
# their data, NAS user accounts or mount units. Only the daemon's own state
# (UI accounts, settings, TLS certificate) is deleted, and only on purge.
set -e
if [ "$1" = "purge" ]; then
  rm -rf /var/lib/cwnsb
  # The helper's record of the mdadm.conf the initramfs was built with (ADR-0033).
  rm -rf /var/lib/cwnsb-helper
fi
case "$1" in
  remove|purge)
    # The system journal goes back to the distribution's choice (ADR-0031).
    # The journal files already written stay: they are the system's logs.
    if [ -f /etc/systemd/journald.conf.d/60-cwnsb-persistent.conf ]; then
      rm -f /etc/systemd/journald.conf.d/60-cwnsb-persistent.conf
      systemctl try-restart systemd-journald.service 2>/dev/null || true
    fi
    ;;
esac
systemctl daemon-reload 2>/dev/null || true
exit 0
