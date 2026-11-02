#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — capture what a disk failure really looks like on lab
# loops (formats and behaviours for ADR-0012): Btrfs mirror missing at boot,
# degraded mount, replace, rebalance; Btrfs disk failing while mounted
# (device-mapper error target); md RAID5 fail/remove/add and degraded assembly.
# Lab only; cleans up after itself.
#   wsl -d cwnsb-lab -u root -- bash /mnt/h/Work/repos/NASBuster/tools/lab/probe-degraded.sh
set -uo pipefail
[ "$(hostname)" = "cwnsb-lab" ] && [ "$(id -u)" -eq 0 ] || { echo "lab only" >&2; exit 1; }

dir=/var/lib/cwnsb-lab/probe-degraded
mnt=/mnt/cwnsb-probe-degraded
mkdir -p "$dir" "$mnt"
loops=()
dms=()
cleanup() {
  umount "$mnt" 2>/dev/null
  mdadm --stop /dev/md/cwnsb-probe5 2>/dev/null
  for d in "${dms[@]}"; do dmsetup remove --retry "$d" 2>/dev/null; done
  for l in "${loops[@]}"; do losetup -d "$l" 2>/dev/null; done
  btrfs device scan --forget 2>/dev/null
  rm -rf "$dir" "$mnt"
}
trap cleanup EXIT

run() { echo "\$ $*"; "$@"; echo "[exit $?]"; }
# newloop NAME: attaches a 512 MiB image and sets $LOOP. Not "$(newloop …)":
# a command substitution is a subshell, and loops+= would be lost for cleanup.
newloop() {
  truncate -s 512M "$dir/$1.img"
  LOOP=$(losetup --find --show --partscan "$dir/$1.img")
  loops+=("$LOOP")
}
part() { sgdisk -n 1:0:-100M -t "1:${2:-8300}" -c 1:cwnsb-probe "$1" >/dev/null && udevadm settle; }
fill() { dd if=/dev/urandom of="$1" bs=1M count="$2" status=none && sync; }

echo "================ A. Btrfs mirror: disk missing at boot ================"
newloop a; a=$LOOP; newloop b; b=$LOOP; newloop c; c=$LOOP
part "$a"; part "$b"; part "$c"
mkfs.btrfs -q -L cwnsb-probe -d raid1 -m raid1 "${a}p1" "${b}p1"
udevadm settle
uuid=$(blkid -s UUID -o value "${a}p1")
mount -o noatime "${a}p1" "$mnt"
fill "$mnt/before.bin" 40
sum_before=$(md5sum <"$mnt/before.bin")
umount "$mnt"

echo "--- unplug b (detach loop, forget stale registration) ---"
losetup -d "$b"
udevadm settle
run btrfs device scan --forget
run btrfs filesystem show "$uuid"
run btrfs filesystem show --raw "$uuid"
run mount -o noatime "UUID=$uuid" "$mnt"
dmesg | tail -4 | sed 's/^/dmesg: /'

echo "--- mount degraded ---"
run mount -o noatime,degraded "UUID=$uuid" "$mnt"
run findmnt -J -o TARGET,SOURCE,FSTYPE,OPTIONS,UUID "$mnt"
run btrfs filesystem show --raw "$mnt"
run btrfs filesystem usage -b "$mnt"
run btrfs --format json device stats "$mnt"
run btrfs --format json filesystem df "$mnt"
ls /sys/fs/btrfs/"$uuid"/devinfo/ 2>/dev/null | sed 's/^/devinfo: /'
for d in /sys/fs/btrfs/"$uuid"/devinfo/*; do echo "devinfo $(basename "$d"): missing=$(cat "$d/missing" 2>/dev/null) in_fs_metadata=$(cat "$d/in_fs_metadata" 2>/dev/null)"; done

echo "--- write while degraded ---"
fill "$mnt/during.bin" 30
run btrfs --format json filesystem df "$mnt"
run btrfs replace status -1 "$mnt"

echo "--- replace missing devid with c ---"
missing=$(btrfs filesystem show --raw "$mnt" | awk '/MISSING|missing/ { for (i = 1; i <= NF; i++) if ($i == "devid") print $(i + 1) }' | head -1)
echo "missing devid: ${missing:-?}"
run btrfs replace start "${missing:-2}" "${c}p1" "$mnt"
for _ in $(seq 1 60); do
  s=$(btrfs replace status -1 "$mnt" 2>&1)
  echo "status: $s"
  case "$s" in *finished*|*Never*|*cancel*|*ERROR*) break ;; esac
  sleep 0.5
done
run btrfs filesystem show --raw "$mnt"
run btrfs filesystem usage -b "$mnt"
run btrfs --format json filesystem df "$mnt"
for d in /sys/fs/btrfs/"$uuid"/devinfo/*; do echo "devinfo $(basename "$d"): missing=$(cat "$d/missing" 2>/dev/null)"; done

echo "--- restore the profile of chunks written while degraded ---"
run btrfs balance start -dconvert=raid1,soft -mconvert=raid1,soft "$mnt"
run btrfs --format json filesystem df "$mnt"
run btrfs scrub start -B "$mnt"
[ "$(md5sum <"$mnt/before.bin")" = "$sum_before" ] && echo "DATA INTACT" || echo "DATA CHANGED"
umount "$mnt"
echo "--- remount without degraded ---"
run mount -o noatime "UUID=$uuid" "$mnt"
umount "$mnt"

echo "================ B. Btrfs mirror: disk failing while mounted (dm error target) ================"
newloop d; d=$LOOP; newloop e; e=$LOOP
sectors=$(blockdev --getsz "$d")
dmsetup create cwnsb-probe-d --table "0 $sectors linear $d 0" && dms+=(cwnsb-probe-d)
dmsetup create cwnsb-probe-e --table "0 $sectors linear $e 0" && dms+=(cwnsb-probe-e)
udevadm settle
mkfs.btrfs -q -L cwnsb-probe2 -d raid1 -m raid1 /dev/mapper/cwnsb-probe-d /dev/mapper/cwnsb-probe-e
udevadm settle
mount -o noatime /dev/mapper/cwnsb-probe-d "$mnt"
fill "$mnt/before.bin" 40
sum_before=$(md5sum <"$mnt/before.bin")
echo 3 >/proc/sys/vm/drop_caches

echo "--- e starts failing every I/O ---"
dmsetup suspend cwnsb-probe-e
dmsetup reload cwnsb-probe-e --table "0 $sectors error"
dmsetup resume cwnsb-probe-e
fill "$mnt/during.bin" 20
echo 3 >/proc/sys/vm/drop_caches
[ "$(md5sum <"$mnt/before.bin")" = "$sum_before" ] && echo "READ OK FROM THE OTHER COPY" || echo "READ FAILED"
run btrfs --format json device stats "$mnt"
run btrfs filesystem show --raw "$mnt"
run btrfs filesystem usage -b "$mnt"
dmesg | grep -i btrfs | tail -5 | sed 's/^/dmesg: /'
for dv in /sys/fs/btrfs/*/devinfo/*; do case "$dv" in *"$(blkid -s UUID -o value /dev/mapper/cwnsb-probe-d)"*) echo "devinfo $(basename "$dv"): missing=$(cat "$dv/missing" 2>/dev/null)" ;; esac; done

echo "--- e comes back (cable reseated) ---"
dmsetup suspend cwnsb-probe-e
dmsetup reload cwnsb-probe-e --table "0 $sectors linear $e 0"
dmsetup resume cwnsb-probe-e
run btrfs scrub start -B "$mnt"
run btrfs scrub status -R "$mnt"
run btrfs --format json device stats "$mnt"
echo "--- reset the counters once repaired ---"
run btrfs device stats -z "$mnt"
run btrfs --format json device stats "$mnt"
umount "$mnt"
for x in cwnsb-probe-d cwnsb-probe-e; do dmsetup remove --retry "$x"; done
dms=()

echo "================ C. md RAID5: fail, remove, add, degraded assembly ================"
newloop f; f=$LOOP; newloop g; g=$LOOP; newloop h; h=$LOOP; newloop s; s=$LOOP
for x in "$f" "$g" "$h" "$s"; do part "$x" fd00; done
mdadm --create /dev/md/cwnsb-probe5 --run --level=5 --raid-devices=3 --metadata=1.2 --bitmap=internal --name=cwnsb-probe5 "${f}p1" "${g}p1" "${h}p1" 2>&1 | tail -1
mdadm --wait /dev/md/cwnsb-probe5
udevadm settle
md=$(basename "$(readlink -f /dev/md/cwnsb-probe5)")
echo "kernel name: $md"
run mdadm --manage /dev/md/cwnsb-probe5 --fail "${g}p1"
grep -A3 "^$md" /proc/mdstat
run mdadm --detail --export /dev/md/cwnsb-probe5
run mdadm --manage /dev/md/cwnsb-probe5 --remove failed
grep -A3 "^$md" /proc/mdstat
run mdadm --detail --export /dev/md/cwnsb-probe5
echo "--- add the spare: recovery ---"
echo 1000 >/proc/sys/dev/raid/speed_limit_max
run mdadm --manage /dev/md/cwnsb-probe5 --add "${s}p1"
sleep 0.5
grep -A3 "^$md" /proc/mdstat
run mdadm --detail --export /dev/md/cwnsb-probe5
cat /sys/block/"$md"/md/sync_action /sys/block/"$md"/md/sync_completed 2>/dev/null | sed 's/^/sysfs: /'
echo 200000 >/proc/sys/dev/raid/speed_limit_max
mdadm --wait /dev/md/cwnsb-probe5
grep -A3 "^$md" /proc/mdstat

echo "--- missing at boot: stop, unplug h, assemble ---"
mdadm --stop /dev/md/cwnsb-probe5
losetup -d "$h"
udevadm settle
run mdadm --assemble /dev/md/cwnsb-probe5 "${f}p1" "${s}p1"
grep -A3 "^md" /proc/mdstat
mdadm --stop /dev/md/cwnsb-probe5 2>/dev/null
run mdadm --assemble --run /dev/md/cwnsb-probe5 "${f}p1" "${s}p1"
grep -A3 "^md" /proc/mdstat
run mdadm --detail --export /dev/md/cwnsb-probe5
run mdadm --manage /dev/md/cwnsb-probe5 --remove detached
mdadm --stop /dev/md/cwnsb-probe5
for x in "$f" "$g" "$s"; do mdadm --zero-superblock "${x}p1" 2>/dev/null; done
echo "PROBE DONE"
exit 0
