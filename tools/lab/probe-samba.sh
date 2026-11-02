#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — install the share stack in the lab and capture real
# Samba formats (testparm, smbstatus --json). Lab only; leaves no user behind.
#   wsl -d cwnsb-lab -- bash /mnt/h/Work/repos/NASBuster/tools/lab/probe-samba.sh
set -u
[ "$(hostname)" = "cwnsb-lab" ] && [ "$(id -u)" -eq 0 ] || { echo "lab only" >&2; exit 1; }

export DEBIAN_FRONTEND=noninteractive
apt-cache search --names-only wsdd
apt-cache policy wsdd wsdd2 2>/dev/null | grep -E "^[a-z]|Candidate"
apt-get install -y --no-install-recommends samba smbclient avahi-daemon dmsetup >/tmp/cwnsb-apt.log 2>&1
echo "apt exit: $?"
smbd --version
command -v testparm pdbedit smbpasswd smbstatus smbcontrol wsdd dmsetup

echo "### default smb.conf global section (testparm -s, first lines)"
testparm -s 2>/dev/null | head -20

echo "### a session, for smbstatus --json"
dir=$(mktemp -d /srv/probe-share.XXXX)
chmod 0777 "$dir"
useradd --no-create-home --shell /usr/sbin/nologin probeuser 2>/dev/null
printf 'probe-pass-123\nprobe-pass-123\n' | smbpasswd -s -a probeuser >/dev/null
cat > /tmp/probe-smb.conf <<EOF
[global]
   server min protocol = SMB2_10
   map to guest = never
[probe]
   path = $dir
   valid users = probeuser
   read only = no
EOF
smbd -s /tmp/probe-smb.conf -D --no-process-group -l /tmp 2>/dev/null || true
sleep 1
(echo "put /etc/hostname hello.txt"; sleep 3) | smbclient //127.0.0.1/probe -U probeuser%probe-pass-123 -s /tmp/probe-smb.conf -m SMB3 >/dev/null 2>&1 &
sleep 1.5
smbstatus --json -s /tmp/probe-smb.conf 2>&1 | head -60
wait
echo "### file written over SMB:"; ls -l "$dir"

pkill -f "smbd -s /tmp/probe-smb.conf"
smbpasswd -x probeuser >/dev/null 2>&1
userdel probeuser 2>/dev/null
rm -rf "$dir" /tmp/probe-smb.conf
