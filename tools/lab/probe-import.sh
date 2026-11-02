#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — what a fresh system sees of CWNSB disks (ADR-0014):
# labels, md metadata and automatic assembly, a different hostname, Samba's
# user database. Lab only; cleans up after itself.
#   wsl -d cwnsb-lab -u root -- bash /mnt/h/Work/repos/NASBuster/tools/lab/probe-import.sh
set -uo pipefail
export COLUMNS=512 LINES=50
[ "$(hostname)" = "cwnsb-lab" ] && [ "$(id -u)" -eq 0 ] || { echo "lab only" >&2; exit 1; }

dir=/var/lib/cwnsb-lab/probe-import
mkdir -p "$dir" /mnt/probe-import
loops=()
cleanup() {
  umount /mnt/probe-import 2>/dev/null
  for md in /dev/md/*probe* /dev/md12[0-9]; do [ -e "$md" ] && mdadm --stop "$md" >/dev/null 2>&1; done
  for l in "${loops[@]}"; do wipefs -a "${l}p1" >/dev/null 2>&1; losetup -d "$l" 2>/dev/null; done
  btrfs device scan --forget 2>/dev/null
  rm -rf "$dir" /mnt/probe-import
}
trap cleanup EXIT
run() { echo "\$ $*"; "$@"; echo "[exit $?]"; }
attach() { LOOP=$(losetup --find --show --partscan "$dir/$1.img"); loops+=("$LOOP"); }

for n in a b c d e; do truncate -s 640M "$dir/$n.img"; done
attach a; A=$LOOP; attach b; B=$LOOP; attach c; C=$LOOP; attach d; D=$LOOP; attach e; E=$LOOP
for l in $A $B; do sgdisk -n 1:0:-100M -t 1:8300 -c 1:cwnsb-probemirror "$l" >/dev/null; done
for l in $C $D $E; do sgdisk -n 1:0:-100M -t 1:FD00 -c 1:cwnsb-probeparity "$l" >/dev/null; done
udevadm settle
mkfs.btrfs -q -L cwnsb-probemirror -d raid1 -m raid1 "${A}p1" "${B}p1"
mdadm --create /dev/md/cwnsb-probeparity --run --level=5 --raid-devices=3 --metadata=1.2 --bitmap=internal \
  --name=cwnsb-probeparity "${C}p1" "${D}p1" "${E}p1" >/dev/null 2>&1
udevadm settle
mkfs.btrfs -q -L cwnsb-probeparity -d single -m dup /dev/md/cwnsb-probeparity
mount "${A}p1" /mnt/probe-import && echo "mirror data" > /mnt/probe-import/hello.txt && umount /mnt/probe-import
mdadm --stop /dev/md/cwnsb-probeparity >/dev/null 2>&1

echo "================ the disks move to a new system (detach, re-attach) ================"
for l in "${loops[@]}"; do losetup -d "$l"; done
loops=()
btrfs device scan --forget
udevadm settle
attach a; A=$LOOP; attach b; B=$LOOP; attach c; C=$LOOP; attach d; D=$LOOP; attach e; E=$LOOP
udevadm settle; sleep 2

echo "--- what lsblk sees"
lsblk -J -o NAME,FSTYPE,LABEL,PARTLABEL,UUID,PARTUUID "$A" "$C" | tr -d '\n' | sed 's/  */ /g'; echo
echo "--- blkid -p on members"
for p in "${A}p1" "${C}p1"; do run blkid -p -o export "$p"; done
echo "--- did udev assemble the array by itself?"
cat /proc/mdstat | grep -A2 "^md" || echo "(no array)"
ls -l /dev/md/ 2>/dev/null
echo "--- mdadm --examine --scan"
run mdadm --examine --scan
echo "--- mdadm --examine --export on a member"
run mdadm --examine --export "${C}p1"
echo "--- btrfs filesystem show (unmounted, by label)"
run btrfs filesystem show cwnsb-probemirror

echo "================ assemble as a foreign host ================"
for md in /dev/md12[0-9]; do [ -e "$md" ] && mdadm --stop "$md" >/dev/null 2>&1; done
run mdadm --assemble --scan --homehost=othernas --config=/dev/null
cat /proc/mdstat | grep -A2 "^md"
ls -l /dev/md/ 2>/dev/null
for md in /dev/md12[0-9]; do [ -e "$md" ] && mdadm --stop "$md" >/dev/null 2>&1; done
run mdadm --assemble --run /dev/md/cwnsb-probeparity --uuid="$(mdadm --examine --export "${C}p1" | sed -n 's/^MD_UUID=//p')" --config=/dev/null
ls -l /dev/md/
run blkid -p -o export /dev/md/cwnsb-probeparity
mount -o ro /dev/disk/by-label/cwnsb-probemirror /mnt/probe-import 2>&1 && cat /mnt/probe-import/hello.txt && umount /mnt/probe-import

echo "================ Samba user database ================"
ls -la /var/lib/samba/private/ 2>&1 | head
run pdbedit -L -w
echo "PROBE DONE"
exit 0
