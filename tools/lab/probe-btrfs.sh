#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — capture real btrfs/mdadm/sgdisk outputs on lab loops
# (formats for parsers). Lab only; cleans up after itself.
#   wsl -d cwnsb-lab -- bash /mnt/h/Work/repos/NASBuster/tools/lab/probe-btrfs.sh
set -uo pipefail
[ "$(hostname)" = "cwnsb-lab" ] && [ "$(id -u)" -eq 0 ] || { echo "lab only" >&2; exit 1; }

dir=/var/lib/cwnsb-lab/probe
mkdir -p "$dir" /mnt/cwnsb-probe
loops=()
cleanup() {
  umount /mnt/cwnsb-probe 2>/dev/null
  for l in "${loops[@]}"; do losetup -d "$l" 2>/dev/null; done
  rm -rf "$dir" /mnt/cwnsb-probe
}
trap cleanup EXIT

for i in 1 2; do
  truncate -s 512M "$dir/d$i.img"
  loops+=("$(losetup --find --show --partscan "$dir/d$i.img")")
done
a=${loops[0]} b=${loops[1]}

echo "### sgdisk partition"
sgdisk -n 1:0:-100M -t 1:8300 -c 1:cwnsb-probe "$a" && sgdisk -n 1:0:-100M -t 1:8300 -c 1:cwnsb-probe "$b"
udevadm settle
ls -l "${a}p1" "${b}p1"
lsblk -J -b -o NAME,TYPE,SIZE,PTTYPE,PARTLABEL,PARTTYPE "$a"

echo "### mkfs.btrfs raid1"
mkfs.btrfs -L cwnsb-probe -d raid1 -m raid1 "${a}p1" "${b}p1" | tail -5
udevadm settle
echo "uuid: $(blkid -s UUID -o value "${a}p1")"
mount -o noatime,compress=zstd:1 "${a}p1" /mnt/cwnsb-probe

echo "### btrfs --format json device stats"
btrfs --format json device stats /mnt/cwnsb-probe
echo "### btrfs --format json filesystem df"
btrfs --format json filesystem df /mnt/cwnsb-probe
echo "### btrfs filesystem usage -b"
btrfs filesystem usage -b /mnt/cwnsb-probe
echo "### btrfs filesystem show --raw"
btrfs filesystem show --raw /mnt/cwnsb-probe
echo "### btrfs scrub start -B"
btrfs scrub start -B /mnt/cwnsb-probe
echo "### btrfs scrub status -R"
btrfs scrub status -R /mnt/cwnsb-probe
echo "### findmnt -J"
findmnt -J -o TARGET,SOURCE,FSTYPE,OPTIONS,UUID /mnt/cwnsb-probe
umount /mnt/cwnsb-probe

echo "### mdadm raid1 --detail --export"
wipefs -a "${a}p1" "${b}p1" >/dev/null
mdadm --create /dev/md/cwnsb-probe --run --level=1 --raid-devices=2 --metadata=1.2 --bitmap=internal --name=cwnsb-probe "${a}p1" "${b}p1" 2>&1 | tail -2
udevadm settle
mdadm --detail --export /dev/md/cwnsb-probe
echo "### mdadm --detail --brief"
mdadm --detail --brief /dev/md/cwnsb-probe
echo "### /proc/mdstat"
cat /proc/mdstat
mdadm --stop /dev/md/cwnsb-probe
wipefs -a "${a}p1" "${b}p1" >/dev/null
