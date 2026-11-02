#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — install the linux/amd64 build into the cwnsb-lab distro.
#
# From PowerShell, after building (scripts/build.ps1 -Arch amd64):
#   wsl -d cwnsb-lab -- bash /mnt/h/Work/repos/NASBuster/tools/lab/install.sh
set -euo pipefail

if [ "$(hostname)" != "cwnsb-lab" ] || [ "$(id -u)" -ne 0 ]; then
  echo "refusing: run as root inside the cwnsb-lab distro only" >&2
  exit 1
fi

repo="$(cd "$(dirname "$0")/../.." && pwd)"
bin="$repo/bin/linux-amd64"

install -D -m 0755 "$bin/nasbusterd" /usr/bin/nasbusterd
install -D -m 0755 "$bin/nbhelper" /usr/libexec/cwnsb/nbhelper
install -D -m 0644 "$repo/packaging/sysusers.d/cwnsb.conf" /usr/lib/sysusers.d/cwnsb.conf
install -m 0644 -t /usr/lib/systemd/system \
  "$repo/packaging/systemd/nasbusterd.service" \
  "$repo/packaging/systemd/nbhelper.service" \
  "$repo/packaging/systemd/nbhelper.socket"
install -D -m 0644 "$repo/tools/lab/nasbusterd-lab.conf" /etc/systemd/system/nasbusterd.service.d/lab.conf
install -D -m 0644 "$repo/tools/lab/nbhelper-lab.conf" /etc/systemd/system/nbhelper.service.d/lab.conf
mkdir -p /var/lib/cwnsb-lab

systemd-sysusers /usr/lib/sysusers.d/cwnsb.conf
systemctl daemon-reload
systemctl enable --now nbhelper.socket
systemctl enable nasbusterd.service
systemctl restart nbhelper.service 2>/dev/null || true
systemctl restart nasbusterd.service

echo "Installed $(/usr/bin/nasbusterd -version). UI: https://localhost:8443"
if [ -f /var/lib/cwnsb/setup-code ]; then
  echo "Setup code: $(cat /var/lib/cwnsb/setup-code)"
fi
