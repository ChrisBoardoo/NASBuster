#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — wipe the lab back to a fresh install: daemon state
# (accounts, sessions, settings, pools, TLS), pool mounts and md arrays, lab
# loop devices and their images. Lab only.
#   wsl -d cwnsb-lab -- bash /mnt/h/Work/repos/NASBuster/tools/lab/reset.sh
set -uo pipefail

if [ "$(hostname)" != "cwnsb-lab" ] || [ "$(id -u)" -ne 0 ]; then
  echo "refusing: run as root inside the cwnsb-lab distro only" >&2
  exit 1
fi

systemctl stop nasbusterd.service

# NAS users, share groups and Samba configuration.
members=$(getent group cwnsb-users | cut -d: -f4 | tr ',' ' ')
for u in $members; do
  smbpasswd -x "$u" >/dev/null 2>&1
  userdel "$u" 2>/dev/null
done
getent group | cut -d: -f1 | grep '^cwnsb-share-' | while read -r g; do groupdel "$g"; done
if [ -f /etc/samba/smb.conf.cwnsb-orig ]; then
  cp /etc/samba/smb.conf.cwnsb-orig /etc/samba/smb.conf
  systemctl reload-or-restart smbd 2>/dev/null
fi

# Pools: unmount and forget them.
for unit in /etc/systemd/system/srv-cwnsb-*.mount; do
  [ -e "$unit" ] || continue
  systemctl disable --now "$(basename "$unit")" 2>/dev/null
  rm -f "$unit"
done
systemctl daemon-reload
for md in /dev/md/cwnsb-*; do
  [ -e "$md" ] && mdadm --stop "$md"
done
if [ -f /etc/mdadm/mdadm.conf ]; then
  sed -i '/^# BEGIN CrossWax NasBuster pool /,/^# END CrossWax NasBuster pool /d' /etc/mdadm/mdadm.conf
fi
find /srv/cwnsb -mindepth 1 -maxdepth 1 -type d -empty -delete 2>/dev/null

# Lab disks: detach every loop backed by a lab image, then delete the images.
losetup -a | awk -F: '/\/var\/lib\/cwnsb-lab\// { print $1 }' | while read -r dev; do
  losetup -d "$dev"
done
find /var/lib/cwnsb-lab -mindepth 1 -delete
btrfs device scan --forget 2>/dev/null # drop stale multi-device registrations

# Daemon state.
find /var/lib/cwnsb -mindepth 1 -delete
systemctl start nasbusterd.service
sleep 1
echo "Lab reset. New setup code: $(cat /var/lib/cwnsb/setup-code)"
