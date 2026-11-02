#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — does the udev database follow writes to a block device?
# Lab only; cleans up after itself.
#   wsl -d cwnsb-lab -- bash /mnt/h/Work/repos/NASBuster/tools/lab/probe-udev.sh
set -u
[ "$(hostname)" = "cwnsb-lab" ] && [ "$(id -u)" -eq 0 ] || { echo "lab only" >&2; exit 1; }

img=/var/lib/cwnsb-lab/probe-udev.img
truncate -s 128M "$img"
dev=$(losetup --find --show --partscan "$img")
trap 'losetup -d "$dev"; rm -f "$img"' EXIT
udevadm settle

state() {
  printf '%-28s lsblk=%-6s udev=%-6s blkid-p=%s\n' "$1" \
    "$(lsblk -n -d -o FSTYPE "$dev" | tr -d ' ')" \
    "$(udevadm info --query=property --name="$dev" | sed -n 's/^ID_FS_TYPE=//p')" \
    "$(blkid -p -s TYPE -o value "$dev")"
}
state "fresh loop"
mkfs.ext4 -q -F "$dev"
udevadm settle
state "after mkfs.ext4 + settle"
sleep 2
state "after 2 s more"
udevadm trigger --action=change --settle "$dev"
state "after trigger change"
grep -h watch /usr/lib/udev/rules.d/60-block.rules 2>/dev/null | head -3
