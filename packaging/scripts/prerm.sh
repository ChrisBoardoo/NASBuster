#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — before removal: stop the services. On upgrade they keep
# running; postinst restarts them.
set -e
case "$1" in
  remove|purge|0)
    systemctl disable --now nasbusterd.service nbhelper.socket nbhelper.service 2>/dev/null || true
    ;;
esac
exit 0
