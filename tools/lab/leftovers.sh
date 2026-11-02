#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# CrossWax NasBuster — list what CWNSB left on the lab system (read-only).
#   wsl -d cwnsb-lab -- bash /mnt/h/Work/repos/NASBuster/tools/lab/leftovers.sh
echo "NAS users:      $(getent group cwnsb-users | cut -d: -f4)"
echo "share groups:   $(getent group | cut -d: -f1 | grep -c '^cwnsb-share-')"
echo "pool units:     $(ls /etc/systemd/system | grep -c '^srv-cwnsb-')"
echo "md arrays:      $(ls /dev/md 2>/dev/null | grep -c '^cwnsb-')"
echo "lab loops:      $(losetup -a | grep -c /var/lib/cwnsb-lab/)"
echo "btrfs mounts:   $(findmnt -t btrfs -n | grep -c /srv/cwnsb/)"
echo "smb.conf owner: $(head -1 /etc/samba/smb.conf 2>/dev/null | cut -c1-45)"
echo "docker:         $(dpkg-query -W -f '${db:Status-Status}' docker.io 2>/dev/null || echo absent)"
echo "job units:      $(systemctl list-units 'cwnsb-job-*' --all --no-legend 2>/dev/null | wc -l)"
