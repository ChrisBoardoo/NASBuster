#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — provision the disposable cwnsb-lab WSL2 distro (Debian 13).
#
# Run as root INSIDE the lab, from PowerShell:
#   wsl -d cwnsb-lab -u root -- bash /mnt/h/Work/repos/NASBuster/tools/lab/provision.sh
# then restart the distro so systemd becomes PID 1:
#   wsl --terminate cwnsb-lab
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
export DEBIAN_FRONTEND=noninteractive

# The distro comes from a Docker image, whose policy-rc.d forbids packages to
# start their services. A real Raspberry Pi OS has none: behave like it.
rm -f /usr/sbin/policy-rc.d

apt-get update
apt-get install -y --no-install-recommends \
  systemd systemd-sysv dbus udev \
  util-linux btrfs-progs mdadm smartmontools gdisk e2fsprogs dmsetup \
  samba smbclient wsdd2 avahi-daemon avahi-utils \
  rsync sudo ca-certificates procps iproute2 usbutils kmod curl less tzdata

install -m 0644 "$here/wsl.conf" /etc/wsl.conf

# Under WSL, "/" is a private mount; systemd makes it shared on a real system,
# so that unmounts reach the services' sandboxes. Behave like a real Pi.
cat > /etc/systemd/system/cwnsb-lab-shared-root.service <<'UNIT'
[Unit]
Description=CrossWax NasBuster lab: shared mount propagation for / (as on a real system)
DefaultDependencies=no
Before=local-fs-pre.target sysinit.target

[Service]
Type=oneshot
ExecStart=/usr/bin/mount --make-rshared /

[Install]
WantedBy=sysinit.target
UNIT
systemctl enable cwnsb-lab-shared-root.service

# Lab disks (loop-device backing files) live here, and only here.
mkdir -p /var/lib/cwnsb-lab

echo "cwnsb-lab provisioned. Restart it from PowerShell: wsl --terminate cwnsb-lab"
