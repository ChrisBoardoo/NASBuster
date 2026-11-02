#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — do Btrfs read-only snapshots show up as Windows
# "Previous Versions" through Samba's shadow_copy2? Lab only; cleans up.
#   wsl -d cwnsb-lab -- bash /mnt/h/Work/repos/NASBuster/tools/lab/probe-shadow.sh
set -u
[ "$(hostname)" = "cwnsb-lab" ] && [ "$(id -u)" -eq 0 ] || { echo "lab only" >&2; exit 1; }

img=/var/lib/cwnsb-lab/probe-shadow.img
mnt=/mnt/cwnsb-shadow
truncate -s 512M "$img"
dev=$(losetup --find --show "$img")
cleanup() {
  pkill -f "smbd -s /tmp/shadow-smb.conf" 2>/dev/null
  # smbd children keep the mount busy for a moment: wait, then retry umount.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    pgrep -f "smbd -s /tmp/shadow-smb.conf" >/dev/null || break
    sleep 0.5
  done
  smbpasswd -x shadowuser >/dev/null 2>&1; userdel shadowuser 2>/dev/null
  for _ in 1 2 3 4 5; do umount "$mnt" 2>/dev/null && break; sleep 1; done
  losetup -d "$dev"; rm -f "$img" /tmp/shadow-smb.conf; rmdir "$mnt" 2>/dev/null
}
trap cleanup EXIT

mkfs.btrfs -q "$dev" && mkdir -p "$mnt" && mount "$dev" "$mnt"
btrfs subvolume create "$mnt/photos" >/dev/null
mkdir "$mnt/photos/.snapshots"
echo "version 1" > "$mnt/photos/doc.txt"
snap1="@GMT-$(date -u -d '-2 hours' +%Y.%m.%d-%H.%M.%S)"
btrfs subvolume snapshot -r "$mnt/photos" "$mnt/photos/.snapshots/$snap1" >/dev/null
echo "version 2" > "$mnt/photos/doc.txt"
snap2="@GMT-$(date -u -d '-1 hours' +%Y.%m.%d-%H.%M.%S)"
btrfs subvolume snapshot -r "$mnt/photos" "$mnt/photos/.snapshots/$snap2" >/dev/null
echo "version 3 (current)" > "$mnt/photos/doc.txt"
echo "snapshots: $(ls "$mnt/photos/.snapshots")"
echo "st_dev share=$(stat -c %d "$mnt/photos") snapshot=$(stat -c %d "$mnt/photos/.snapshots/$snap1")"

useradd --no-create-home --shell /usr/sbin/nologin shadowuser
printf 'shadow-pass-123\nshadow-pass-123\n' | smbpasswd -s -a shadowuser >/dev/null
chmod 0777 "$mnt/photos"
cat > /tmp/shadow-smb.conf <<EOF
[global]
   server min protocol = SMB2_10
   map to guest = never
   smb ports = 4455
   pid directory = /tmp
   lock directory = /tmp
   state directory = /tmp
   cache directory = /tmp
[photos]
   path = $mnt/photos
   valid users = shadowuser
   read only = no
   hide files = /.snapshots/
   vfs objects = shadow_copy2
   shadow:snapdir = .snapshots
   shadow:format = @GMT-%Y.%m.%d-%H.%M.%S
   shadow:sort = desc
   shadow:localtime = no
EOF
testparm -s /tmp/shadow-smb.conf >/dev/null 2>&1 && echo "testparm: ok"
smbd -s /tmp/shadow-smb.conf -D --no-process-group -l /tmp 2>/dev/null
sleep 1
echo "### allinfo doc.txt (look for @GMT shadow copies)"
smbclient //127.0.0.1/photos -p 4455 -U shadowuser%shadow-pass-123 -m SMB3 -c "allinfo doc.txt" 2>&1 | grep -iE "GMT|create_time|shadow" | head -10
echo "### read the 2-hours-ago version through its @GMT token"
smbclient "//127.0.0.1/photos" -p 4455 -U shadowuser%shadow-pass-123 -m SMB3 -c "get $snap1/doc.txt /tmp/old.txt" >/dev/null 2>&1
echo "old content: $(cat /tmp/old.txt 2>/dev/null || echo FAILED)"; rm -f /tmp/old.txt
echo "### is .snapshots visible in a listing?"
smbclient //127.0.0.1/photos -p 4455 -U shadowuser%shadow-pass-123 -m SMB3 -c "ls" 2>&1 | grep -E "snapshots|doc.txt"
